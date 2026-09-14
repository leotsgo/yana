// Package rt is the realtime relay: a WebSocket endpoint that moves CRDT
// updates between the clients editing a note and the reconciliation loop.
//
// The relay is deliberately dumb. It routes opaque byte payloads by note id
// and never parses, merges, or interprets them; merging is the CRDT
// library's job on each client. One logical room exists per note id, a
// connection may join many rooms, and everything a connection sends is
// bounded by per-connection, per-room, and per-author limits.
package rt

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
)

// Options bound the relay. Zero values take the defaults noted on each
// field.
type Options struct {
	// MaxConnections caps concurrent connections (default 256).
	MaxConnections int
	// MaxRoomsPerConn caps how many note rooms one connection may join
	// (default 16).
	MaxRoomsPerConn int
	// MaxMessageBytes caps one inbound WebSocket frame (default 1 MiB).
	MaxMessageBytes int64
	// OutBuffer is the outbound queue per connection in messages (default
	// 256). A connection whose queue fills is closed as too slow; that is
	// what keeps memory bounded under sustained fan-out.
	OutBuffer int
	// EventBuffer sizes the channel carrying reconciliation events to the
	// fan-out loop (default 1024).
	EventBuffer int
	// PingInterval is how often the server pings each connection to detect
	// dead peers (default 30s).
	PingInterval time.Duration
	// WriteTimeout bounds one outbound frame write (default 10s).
	WriteTimeout time.Duration
	// Limiter bounds upd and aw messages per author identity, reusing the
	// token-bucket semantics of the path-safety package. The default is
	// 1200/min for users and 300/min for agents: a client that batches
	// keystrokes on a 50ms timer peaks at 20 messages a second.
	Limiter *pathsafe.RateLimiter
	// Verify checks the access token carried by the WebSocket handshake
	// (?token= or Authorization: Bearer). nil lets any connection in and
	// trusts client-declared authors.
	Verify TokenVerifier
}

func (o *Options) defaults() {
	if o.MaxConnections <= 0 {
		o.MaxConnections = 256
	}
	if o.MaxRoomsPerConn <= 0 {
		o.MaxRoomsPerConn = 16
	}
	if o.MaxMessageBytes <= 0 {
		o.MaxMessageBytes = 1 << 20
	}
	if o.OutBuffer <= 0 {
		o.OutBuffer = 256
	}
	if o.EventBuffer <= 0 {
		o.EventBuffer = 1024
	}
	if o.PingInterval <= 0 {
		o.PingInterval = 30 * time.Second
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 10 * time.Second
	}
}

// Stats is a snapshot of the relay's counters. Pending write-backs are
// reported separately by the reconciler's own stats; /api/status carries
// both.
type Stats struct {
	Connections      int   `json:"connections"`
	Rooms            int   `json:"rooms"`
	Subscriptions    int   `json:"subscriptions"`
	UpdatesRelayed   int64 `json:"updates_relayed"`
	UpdatesDropped   int64 `json:"updates_dropped"`
	AwarenessRelayed int64 `json:"awareness_relayed"`
	EventsDropped    int64 `json:"events_dropped"`
	SlowClosed       int64 `json:"slow_closed"`
}

// Identity is the verified account behind a WebSocket connection. The
// zero value means the relay runs without accounts (the pre-auth state
// or a test).
type Identity struct {
	UserID    string
	Username  string
	Owner     bool
	SessionID string
}

// TokenVerifier checks an access token presented at the WebSocket
// handshake. nil disables connection authentication.
type TokenVerifier func(token string) (Identity, error)

// Role constants mirror the spaces package's roles.
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// Authorizer decides whether a connection's declared author may
// subscribe to a note, and with which role. The default allows
// everything; Phase 4 replaces it with the spaces and sessions lookup
// so subscriptions can be refused per member.
type Authorizer interface {
	AuthorizeSubscribe(ctx context.Context, author, noteID string) (string, error)
	// NoteSpace reports which space a note lives in, so a membership
	// change can re-check the rooms it affects.
	NoteSpace(ctx context.Context, noteID string) (string, error)
}

type allowAll struct{}

func (allowAll) AuthorizeSubscribe(context.Context, string, string) (string, error) {
	return RoleOwner, nil
}

func (allowAll) NoteSpace(context.Context, string) (string, error) { return "", nil }

// AllowAll returns the permissive Authorizer used until Phase 4.
func AllowAll() Authorizer { return allowAll{} }

// room is one note's set of subscribers.
type room struct {
	id      string
	members map[*conn]struct{}
	unpin   func()
}

// Hub routes CRDT updates between note rooms and the reconciliation loop.
// It implements http.Handler; mount it at GET /ws.
type Hub struct {
	rec     *reconcile.Reconciler
	authz   Authorizer
	verify  TokenVerifier
	opts    Options
	limiter *pathsafe.RateLimiter
	log     *slog.Logger

	events chan reconcile.Event

	// mu guards rooms, conns, and each member's room set. It is never held
	// while waiting on a note lock held by the fan-out loop's senders: the
	// only lock ordering in the process is hub.mu → note locks.
	mu    sync.Mutex
	rooms map[string]*room
	conns map[*conn]struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	updates, awareness, updDropped, evDropped, slow atomic.Int64
}

// New builds a hub over rec and starts its fan-out loop. Call Close when
// the HTTP server has shut down and before the reconciler is closed.
func New(rec *reconcile.Reconciler, authz Authorizer, opts Options, log *slog.Logger) *Hub {
	opts.defaults()
	if authz == nil {
		authz = AllowAll()
	}
	if opts.Limiter == nil {
		opts.Limiter = pathsafe.NewRateLimiter(
			pathsafe.Rate{N: 1200, Window: time.Minute},
			pathsafe.Rate{N: 300, Window: time.Minute},
		)
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{
		rec:     rec,
		authz:   authz,
		verify:  opts.Verify,
		opts:    opts,
		limiter: opts.Limiter,
		log:     log.With("component", "rt"),
		events:  make(chan reconcile.Event, opts.EventBuffer),
		rooms:   map[string]*room{},
		conns:   map[*conn]struct{}{},
		ctx:     ctx,
		cancel:  cancel,
	}
	h.wg.Add(1)
	go h.pump()
	return h
}

// Close closes every connection and stops the fan-out loop. It returns
// even if a peer refuses to finish the close handshake.
func (h *Hub) Close() {
	h.cancel()
	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		h.log.Warn("relay close timed out; some connections may linger")
	}
}

// Stats returns the relay's counters.
func (h *Hub) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := Stats{Connections: len(h.conns), Rooms: len(h.rooms)}
	for _, r := range h.rooms {
		s.Subscriptions += len(r.members)
	}
	s.UpdatesRelayed = h.updates.Load()
	s.UpdatesDropped = h.updDropped.Load()
	s.AwarenessRelayed = h.awareness.Load()
	s.EventsDropped = h.evDropped.Load()
	s.SlowClosed = h.slow.Load()
	return s
}

// pump moves reconciliation events to the fan-out loop without ever
// blocking the reconciler: its subscribers run with the note locked, so the
// callback only tries the buffered channel.
func (h *Hub) pump() {
	defer h.wg.Done()
	unsub := h.rec.Subscribe(func(ev reconcile.Event) {
		select {
		case h.events <- ev:
		default:
			h.evDropped.Add(1)
			h.log.Warn("event queue full; dropping event", "note", ev.NoteID, "kind", ev.Kind)
		}
	})
	defer unsub()
	for {
		select {
		case <-h.ctx.Done():
			return
		case ev := <-h.events:
			h.fanout(ev)
		}
	}
}

// fanout delivers one reconciliation event to the note's room. Update
// payloads reach every member except the connection the update came from;
// the originator already holds that state.
func (h *Hub) fanout(ev reconcile.Event) {
	h.mu.Lock()
	r := h.rooms[ev.NoteID]
	if r == nil {
		h.mu.Unlock()
		return
	}
	members := make([]*conn, 0, len(r.members))
	for c := range r.members {
		members = append(members, c)
	}
	h.mu.Unlock()

	var msg ServerMessage
	switch ev.Kind {
	case reconcile.EventUpdate:
		msg = ServerMessage{Type: msgUpdate, Note: ev.NoteID, Update: ev.Update, Author: ev.Author}
	case reconcile.EventMoved:
		msg = ServerMessage{Type: msgMoved, Note: ev.NoteID, Path: ev.Path}
	case reconcile.EventDeleted:
		msg = ServerMessage{Type: msgDeleted, Note: ev.NoteID}
	default:
		return
	}
	for _, c := range members {
		if ev.Source == any(c) {
			continue
		}
		if ev.Kind == reconcile.EventUpdate {
			h.updates.Add(1)
		}
		c.trySend(msg)
	}
}

// join adds c to the note's room, pins the note while the room is live, and
// returns the subscribed reply carrying everything the client's state
// vector is missing. It runs on the connection's read loop.
func (h *Hub) join(ctx context.Context, c *conn, noteID string, sv []byte, role string) (ServerMessage, error) {
	h.mu.Lock()
	if len(c.rooms) >= h.opts.MaxRoomsPerConn {
		h.mu.Unlock()
		return ServerMessage{}, errRoomLimit
	}

	r := h.rooms[noteID]
	if r == nil {
		unpin, err := h.rec.Pin(ctx, noteID)
		if err != nil {
			h.mu.Unlock()
			return ServerMessage{}, mapRecErr(err)
		}
		r = &room{id: noteID, members: map[*conn]struct{}{}, unpin: unpin}
		h.rooms[noteID] = r
	}
	r.members[c] = struct{}{}
	c.rooms[noteID] = roomRole{role: role}
	h.mu.Unlock()

	// The client may already receive live updates for this note before the
	// diff lands in its queue. That is fine: CRDT updates are idempotent
	// and commutative, so base state and live traffic apply in any order.
	diff, err := h.rec.Diff(ctx, noteID, sv)
	if err != nil {
		h.leave(c, noteID)
		return ServerMessage{}, mapRecErr(err)
	}
	return ServerMessage{Type: msgSubscribed, Note: noteID, Update: diff}, nil
}

// leave removes c from one room and unpins an emptied room.
func (h *Hub) leave(c *conn, noteID string) {
	h.mu.Lock()
	r := h.rooms[noteID]
	if r != nil {
		delete(r.members, c)
		if len(r.members) == 0 {
			delete(h.rooms, noteID)
		}
	}
	delete(c.rooms, noteID)
	h.mu.Unlock()
	if r != nil && len(r.members) == 0 {
		r.unpin()
	}
}

// removeConn drops a closing connection from every room.
func (h *Hub) removeConn(c *conn) {
	h.mu.Lock()
	rooms := make([]*room, 0, len(c.rooms))
	for id := range c.rooms {
		if r := h.rooms[id]; r != nil {
			delete(r.members, c)
			if len(r.members) == 0 {
				delete(h.rooms, id)
				rooms = append(rooms, r)
			}
		}
	}
	c.rooms = map[string]roomRole{}
	delete(h.conns, c)
	h.mu.Unlock()
	for _, r := range rooms {
		r.unpin()
	}
}

// broadcastAwareness relays a presence payload to the note's other members.
// It never touches the reconciliation loop or the log.
func (h *Hub) broadcastAwareness(c *conn, noteID string, payload []byte) {
	h.mu.Lock()
	r := h.rooms[noteID]
	if r == nil {
		h.mu.Unlock()
		return
	}
	members := make([]*conn, 0, len(r.members))
	for m := range r.members {
		members = append(members, m)
	}
	h.mu.Unlock()
	msg := ServerMessage{Type: msgAwareness, Note: noteID, Payload: payload}
	for _, m := range members {
		if m == c {
			continue
		}
		h.awareness.Add(1)
		m.trySend(msg)
	}
}

var errRoomLimit = errors.New("rt: too many rooms on this connection")

// roomRole is a connection's standing in one room.
type roomRole struct{ role string }

// RecheckSpace re-runs the subscribe authorization for every connection
// in the rooms of one space and severs the ones that no longer pass:
// editing .space.yml takes effect on open subscriptions within one
// watcher cycle.
func (h *Hub) RecheckSpace(ctx context.Context, space string) {
	if space == "" {
		return
	}
	type target struct {
		c    *conn
		note string
	}
	h.mu.Lock()
	var targets []target
	for noteID, r := range h.rooms {
		for c := range r.members {
			targets = append(targets, target{c: c, note: noteID})
		}
	}
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, t := range targets {
		noteSpace, err := h.authz.NoteSpace(ctx, t.note)
		if err != nil || noteSpace != space {
			continue
		}
		if _, err := h.authz.AuthorizeSubscribe(ctx, t.c.author, t.note); err != nil {
			h.log.Info("membership changed; severing subscription", "conn", t.c.author, "note", t.note, "space", space)
			h.leave(t.c, t.note)
			t.c.trySend(ServerMessage{
				Type: msgError, Note: t.note, Code: errForbidden,
				Reason: "access to this note's space was revoked",
			})
		}
	}
}

// KickSession closes every connection authenticated with a session id,
// after the session was revoked.
func (h *Hub) KickSession(sessionID string) {
	h.mu.Lock()
	var kicked []*conn
	for c := range h.conns {
		if c.identity != nil && c.identity.SessionID == sessionID {
			kicked = append(kicked, c)
		}
	}
	h.mu.Unlock()
	for _, c := range kicked {
		h.log.Info("session revoked; closing connection", "conn", c.author)
		c.shutdown(websocket.StatusPolicyViolation, "this session was revoked")
	}
}

func mapRecErr(err error) error {
	if errors.Is(err, reconcile.ErrNotFound) {
		return notFoundErr{}
	}
	return err
}

type notFoundErr struct{}

func (notFoundErr) Error() string { return "no note with that id" }
