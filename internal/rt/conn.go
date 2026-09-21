package rt

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/madeofpendletonwool/yana/internal/reconcile"
)

// conn is one WebSocket connection. Its read loop owns the connection's
// author and room membership requests; the write loop drains the outbound
// queue. Frames are binary msgpack, one message per frame.
type conn struct {
	hub *Hub
	ws  *websocket.Conn
	log *slog.Logger

	out    chan ServerMessage
	closed chan struct{}
	once   sync.Once

	// rooms is guarded by hub.mu. Each entry carries the role the
	// connection's single subscribe-time lookup granted.
	rooms map[string]roomRole
	// spaces is the set of watched spaces, guarded by hub.mu.
	spaces map[string]struct{}
	// author is written only by the read loop and read by the same loop
	// (and by RecheckSpace under hub.mu; reads race only with a write
	// that replaces it with the same value).
	author string
	// identity is the verified account behind the connection, set once
	// at the handshake; nil when the relay runs without accounts.
	identity *Identity
}

// ServeHTTP upgrades to WebSocket and runs the connection until it drops.
// It implements the GET /ws endpoint.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var identity *Identity
	if h.verify != nil {
		token := r.URL.Query().Get("token")
		if token == "" {
			token = bearerToken(r.Header.Get("Authorization"))
		}
		id, err := h.verify(token)
		if err != nil {
			http.Error(w, "a valid access token is required (token query parameter or Authorization header)", http.StatusUnauthorized)
			return
		}
		identity = &id
	}
	h.mu.Lock()
	full := len(h.conns) >= h.opts.MaxConnections
	h.mu.Unlock()
	if full {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already answered the handshake
	}
	ws.SetReadLimit(h.opts.MaxMessageBytes)

	c := &conn{
		hub:      h,
		ws:       ws,
		log:      h.log,
		out:      make(chan ServerMessage, h.opts.OutBuffer),
		closed:   make(chan struct{}),
		rooms:    map[string]roomRole{},
		spaces:   map[string]struct{}{},
		identity: identity,
	}
	if identity != nil {
		c.author = "user:" + identity.Username
	}
	h.mu.Lock()
	if len(h.conns) >= h.opts.MaxConnections {
		h.mu.Unlock()
		ws.CloseNow()
		return
	}
	h.conns[c] = struct{}{}
	h.mu.Unlock()

	h.wg.Add(2)
	go func() { defer h.wg.Done(); c.writeLoop(h.ctx) }()
	go func() {
		defer h.wg.Done()
		defer h.removeConn(c)
		defer c.drop() // the reader is gone; stop the writer too
		c.readLoop(h.ctx)
	}()
}

// bearerToken strips the Bearer prefix, if any.
func bearerToken(header string) string {
	return strings.TrimPrefix(header, "Bearer ")
}

// readLoop decodes and dispatches client messages until the connection
// ends.
func (c *conn) readLoop(ctx context.Context) {
	for {
		typ, r, err := c.ws.Reader(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			c.shutdown(websocket.StatusUnsupportedData, "frames must be binary")
			return
		}
		data, err := io.ReadAll(r)
		if err != nil {
			c.shutdown(websocket.StatusMessageTooBig, "frame exceeds the message limit")
			return
		}
		var msg ClientMessage
		if err := msgpack.Unmarshal(data, &msg); err != nil {
			c.shutdown(websocket.StatusUnsupportedData, "frame is not a valid message")
			return
		}
		if !c.handle(ctx, msg) {
			return
		}
	}
}

// handle dispatches one client message. It returns false when the
// connection must close.
func (c *conn) handle(ctx context.Context, msg ClientMessage) bool {
	switch msg.Type {
	case msgPing:
		c.trySend(ServerMessage{Type: msgPong})
		return true

	case msgSubscribe:
		if !validNoteID(msg.Note) {
			c.replyError(msgSubscribe, msg.Note, errInvalid, "note id must be a 26-character ULID")
			return true
		}
		if c.hub.verify != nil {
			// The connection's identity is fixed at the handshake; the
			// server names the author, the client does not.
			msg.Author = c.author
		} else if msg.Author != "" {
			if !validAuthor(msg.Author) {
				c.replyError(msgSubscribe, msg.Note, errInvalidAuthor, "author must be user:<name> or agent:<label>")
				return true
			}
			c.author = msg.Author
		}
		role, err := c.hub.authz.AuthorizeSubscribe(ctx, c.author, msg.Note)
		if err != nil {
			c.replyError(msgSubscribe, msg.Note, errForbidden, err.Error())
			return true
		}
		subd, err := c.hub.join(ctx, c, msg.Note, msg.SV, role)
		if err != nil {
			code, reason := errReply(err)
			c.replyError(msgSubscribe, msg.Note, code, reason)
			return true
		}
		c.trySend(subd)
		return true

	case msgUnsubscribe:
		if validNoteID(msg.Note) {
			c.hub.leave(c, msg.Note)
		}
		return true

	case msgWatch:
		space := msg.Space
		if space == "" || strings.Contains(space, "/") || len(space) > 128 {
			c.replyError(msgWatch, msg.Note, errInvalid, "space must be a single directory name")
			return true
		}
		if c.hub.verify != nil {
			msg.Author = c.author
		} else if msg.Author != "" {
			if !validAuthor(msg.Author) {
				c.replyError(msgWatch, msg.Note, errInvalidAuthor, "author must be user:<name> or agent:<label>")
				return true
			}
			c.author = msg.Author
		}
		if sa, ok := c.hub.authz.(SpaceAuthorizer); ok {
			if err := sa.AuthorizeWatch(ctx, c.author, space); err != nil {
				c.replyError(msgWatch, msg.Note, errForbidden, "you are not a member of that space")
				return true
			}
		}
		if err := c.hub.joinWatch(ctx, c, space); err != nil {
			code, reason := errReply(err)
			c.replyError(msgWatch, msg.Note, code, reason)
			return true
		}
		c.trySend(ServerMessage{Type: msgWatched, Space: space})
		return true

	case msgUpdate:
		if !validNoteID(msg.Note) {
			c.replyError(msgUpdate, msg.Note, errInvalid, "note id must be a 26-character ULID")
			return true
		}
		if len(msg.Update) == 0 {
			c.replyError(msgUpdate, msg.Note, errInvalid, "update payload is empty")
			return true
		}
		if c.hub.verify != nil {
			// Only a subscribed room's writers may push updates; the
			// role was fixed by the subscribe-time lookup.
			c.hub.mu.Lock()
			rr, member := c.rooms[msg.Note]
			c.hub.mu.Unlock()
			if !member {
				c.replyError(msgUpdate, msg.Note, errForbidden, "subscribe before sending updates")
				return true
			}
			if rr.role != RoleOwner && rr.role != RoleEditor {
				c.replyError(msgUpdate, msg.Note, errForbidden, "this space is read-only for your account")
				return true
			}
			msg.Author = c.author
		} else if validAuthor(msg.Author) {
			c.author = msg.Author
		} else {
			c.replyError(msgUpdate, msg.Note, errInvalidAuthor, "author must be user:<name> or agent:<label>")
			return true
		}
		if !c.hub.limiter.Allow(msg.Author) {
			c.hub.updDropped.Add(1)
			c.replyError(msgUpdate, msg.Note, errRateLimited, "update rate exceeded; batch more per message")
			return true
		}
		// The relay hands the opaque payload straight to the loop, which
		// records it in the log, marks the note dirty for write-back, and
		// emits the event that fans the update out to the room.
		err := c.hub.rec.ApplyUpdate(ctx, msg.Note, msg.Update, msg.Author, c)
		if err != nil {
			if errors.Is(err, reconcile.ErrNotFound) {
				c.replyError(msgUpdate, msg.Note, errNotFound, "no note with that id")
				return true
			}
			c.hub.log.Error("apply update", "note", msg.Note, "err", err)
			c.replyError(msgUpdate, msg.Note, errInternal, "the update could not be applied")
			return true
		}
		return true

	case msgAwareness:
		if !validNoteID(msg.Note) {
			return true
		}
		if len(msg.Payload) == 0 {
			return true
		}
		// Only room members broadcast into a room.
		c.hub.mu.Lock()
		_, member := c.rooms[msg.Note]
		c.hub.mu.Unlock()
		if !member {
			return true
		}
		if c.hub.limiter.Allow(c.author) {
			c.hub.broadcastAwareness(c, msg.Note, msg.Payload)
		} else {
			c.hub.updDropped.Add(1)
		}
		return true

	default:
		c.shutdown(websocket.StatusUnsupportedData, "unknown message type")
		return false
	}
}

// writeLoop drains the outbound queue, keeps the connection alive with
// pings, and closes cleanly on shutdown.
func (c *conn) writeLoop(ctx context.Context) {
	ping := time.NewTicker(c.hub.opts.PingInterval)
	defer ping.Stop()
	for {
		select {
		case msg := <-c.out:
			if err := c.write(ctx, msg); err != nil {
				c.drop()
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, c.hub.opts.PingInterval)
			err := c.ws.Ping(pctx)
			cancel()
			if err != nil {
				c.drop()
				return
			}
		case <-ctx.Done():
			c.shutdown(websocket.StatusGoingAway, "server shutting down")
			return
		case <-c.closed:
			return
		}
	}
}

func (c *conn) write(ctx context.Context, msg ServerMessage) error {
	wctx, cancel := context.WithTimeout(ctx, c.hub.opts.WriteTimeout)
	defer cancel()
	w, err := c.ws.Writer(wctx, websocket.MessageBinary)
	if err != nil {
		return err
	}
	if err := msgpack.NewEncoder(w).Encode(&msg); err != nil {
		return err
	}
	return w.Close()
}

// trySend queues a message, closing the connection as too slow when its
// queue is full. It never blocks the caller.
func (c *conn) trySend(msg ServerMessage) {
	select {
	case c.out <- msg:
	default:
		c.hub.slow.Add(1)
		c.hub.log.Warn("outbound queue full; closing slow connection", "conn", c.author)
		c.drop()
	}
}

func (c *conn) replyError(reqType, note, code, reason string) {
	c.trySend(ServerMessage{Type: msgError, Note: note, Code: code, Reason: reason})
}

// drop closes the connection immediately, without a handshake, from any
// goroutine.
func (c *conn) drop() {
	c.once.Do(func() {
		close(c.closed)
		c.ws.CloseNow()
	})
}

// shutdown closes the connection with a close frame, bounded by a timeout
// so an unresponsive peer cannot stall the relay.
func (c *conn) shutdown(code websocket.StatusCode, reason string) {
	c.once.Do(func() {
		close(c.closed)
		go func() {
			done := make(chan struct{})
			go func() {
				c.ws.Close(code, reason)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				c.ws.CloseNow()
			}
		}()
	})
}

func errReply(err error) (code, reason string) {
	var nf notFoundErr
	if errors.As(err, &nf) {
		return errNotFound, nf.Error()
	}
	if errors.Is(err, errRoomLimit) {
		return errTooManyRooms, "too many rooms on this connection"
	}
	if errors.Is(err, errSpaceLimit) {
		return errTooManyRooms, "too many watched spaces on this connection"
	}
	return errInternal, "subscribe failed"
}
