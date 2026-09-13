package rt

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/vmihailenco/msgpack/v5"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/ydoc"
)

// Timings mirrored from the reconcile harness: fast enough for a short
// suite, slow enough that the write-back and watcher windows mean
// something.
const (
	tIdle     = 100 * time.Millisecond
	tDebounce = 30 * time.Millisecond
	tSettle   = 300 * time.Millisecond
	waitFor   = 10 * time.Second
)

func testLogger(t testing.TB) *slog.Logger {
	if os.Getenv("YANA_TEST_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stack is a full server-side environment: notes root, index, scanner,
// reconciliation loop, relay, and an HTTP server to reach it.
type stack struct {
	t    *testing.T
	ctx  context.Context
	dir  string
	root *pathsafe.Root
	db   *index.DB
	sc   *scanner.Scanner
	rec  *reconcile.Reconciler
	hub  *Hub
	hs   *httptest.Server
	url  string
}

func testHubOptions() Options { return Options{} }

func newStack(t *testing.T, hubOpts Options) *stack {
	t.Helper()
	dir := t.TempDir()
	st := openStack(t, dir, hubOpts)
	t.Cleanup(st.close)
	return st
}

// openStack builds the pieces over dir without registering cleanup, so a
// restart test can close and reopen the same tree.
func openStack(t *testing.T, dir string, hubOpts Options) *stack {
	t.Helper()
	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	log := testLogger(t)
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	sc := scanner.New(root, db, scanner.Options{SettleTime: tSettle}, log)
	rec := reconcile.New(root, db, sc, reconcile.Options{
		IdleTime: tIdle, Debounce: tDebounce, SettleTime: tSettle, UnloadAfter: -1,
	}, log)
	if err := rec.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec.SweepOrphans(context.Background())
	hub := New(rec, nil, hubOpts, log)
	hs := httptest.NewServer(hub)
	st := &stack{
		t: t, ctx: context.Background(), dir: dir, root: root, db: db, sc: sc,
		rec: rec, hub: hub, hs: hs,
		url: "ws" + strings.TrimPrefix(hs.URL, "http") + "/ws",
	}
	return st
}

func (st *stack) close() {
	st.t.Helper()
	st.hs.Close()
	st.hub.Close()
	if err := st.rec.Close(); err != nil {
		st.t.Error(err)
	}
	if err := st.db.Close(); err != nil {
		st.t.Error(err)
	}
}

func (st *stack) abs(rel string) string { return filepath.Join(st.dir, filepath.FromSlash(rel)) }

func (st *stack) newNote(rel, body string) string {
	st.t.Helper()
	id := scanner.NewID(time.Now())
	content := "---\nid: " + id + "\ncreated: 2026-09-12T14:02:11Z\n---\n" + body
	if err := os.MkdirAll(filepath.Dir(st.abs(rel)), 0o755); err != nil {
		st.t.Fatal(err)
	}
	if err := os.WriteFile(st.abs(rel), []byte(content), 0o644); err != nil {
		st.t.Fatal(err)
	}
	if err := st.sc.ScanOne(st.ctx, rel); err != nil {
		st.t.Fatal(err)
	}
	return id
}

func (st *stack) fileBody(rel string) string {
	b, err := os.ReadFile(st.abs(rel))
	if err != nil {
		return "<missing: " + err.Error() + ">"
	}
	return string(frontmatter.Parse(b).Body)
}

func (st *stack) appendFile(rel, s string) {
	st.t.Helper()
	f, err := os.OpenFile(st.abs(rel), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		st.t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		st.t.Fatal(err)
	}
	f.Close()
}

func (st *stack) eventually(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// converged waits until the document, the file, the index, and every peer
// agree, then returns the agreed body.
func (st *stack) converged(id string, peers ...*peer) string {
	st.t.Helper()
	var body string
	ok := st.eventually(20*tSettle, func() bool {
		for _, p := range peers {
			p.drainPending()
		}
		rel, err := st.rec.Path(st.ctx, id)
		if err != nil {
			return false
		}
		doc, err := st.rec.Text(st.ctx, id)
		if err != nil {
			return false
		}
		idx, err := st.db.Body(st.ctx, id)
		if err != nil {
			return false
		}
		if doc != st.fileBody(rel) || idx != doc {
			return false
		}
		for _, p := range peers {
			if p.text() != doc {
				return false
			}
		}
		body = doc
		return true
	})
	if !ok {
		var texts []string
		for _, p := range peers {
			texts = append(texts, fmt.Sprintf("%s=%q", p.author, p.text()))
		}
		st.t.Fatalf("not converged for %s:\n peers=%s", id, strings.Join(texts, "\n "))
	}
	return body
}

// peer is a headless Yjs client speaking the relay protocol over a real
// WebSocket: it keeps its own document, sends updates under its author, and
// applies everything it receives.
type peer struct {
	t      *testing.T
	author string
	note   string
	doc    *ydoc.Doc
	mu     sync.Mutex

	ws    *websocket.Conn
	ctx   context.Context
	reset func()
	msgs  chan ServerMessage
}

func newPeer(t *testing.T, author string) *peer {
	return &peer{t: t, author: author, doc: ydoc.New(), msgs: make(chan ServerMessage, 512)}
}

func (p *peer) dial(url string) {
	p.t.Helper()
	if p.reset != nil {
		p.reset() // a redial ends the previous connection's pump
	}
	ctx, cancel := context.WithCancel(context.Background())
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		p.t.Fatal(err)
	}
	p.ws, p.ctx, p.reset = ws, ctx, cancel
	p.msgs = make(chan ServerMessage, 512)
	go func() {
		defer close(p.msgs)
		for {
			typ, r, err := ws.Reader(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageBinary {
				continue
			}
			data, err := io.ReadAll(r)
			if err != nil {
				return
			}
			var m ServerMessage
			if err := msgpack.Unmarshal(data, &m); err != nil {
				continue
			}
			select {
			case p.msgs <- m:
			default: // drop rather than stall; tests that care drain fast
			}
		}
	}()
}

// close ends the connection, as a browser tab would.
func (p *peer) close() {
	if p.reset != nil {
		p.reset()
		_ = p.ws.Close(websocket.StatusNormalClosure, "bye")
	}
}

// send writes one protocol message.
func (p *peer) send(m ClientMessage) {
	p.t.Helper()
	wctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	w, err := p.ws.Writer(wctx, websocket.MessageBinary)
	if err != nil {
		p.t.Fatal(err)
	}
	if err := msgpack.NewEncoder(w).Encode(&m); err != nil {
		p.t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		p.t.Fatal(err)
	}
}

// subscribe joins the note's room; sv nil asks for the whole document.
func (p *peer) subscribe(note string, sv []byte) ServerMessage {
	p.t.Helper()
	p.note = note
	p.send(ClientMessage{Type: msgSubscribe, Note: note, SV: sv, Author: p.author})
	m, ok := p.waitFor(msgSubscribed, waitFor)
	if !ok {
		p.t.Fatalf("%s: no subscribed reply for %s", p.author, note)
	}
	return m
}

func (p *peer) waitFor(kind string, timeout time.Duration) (ServerMessage, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case m, ok := <-p.msgs:
			if !ok {
				return ServerMessage{}, false
			}
			p.absorb(m)
			if m.Type == kind {
				return m, true
			}
		case <-deadline:
			return ServerMessage{}, false
		}
	}
}

// absorb applies relay output to the local document: live updates and the
// base state carried by the subscribed reply.
func (p *peer) absorb(m ServerMessage) {
	if m.Type != msgUpdate && m.Type != msgSubscribed {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := p.doc.Apply(m.Update, "remote"); err != nil {
		p.t.Errorf("%s: apply relayed update: %v", p.author, err)
	}
}

// drainUntil polls the peer's document until want holds, consuming relay
// output along the way.
func (p *peer) drainUntil(timeout time.Duration, want func(*peer) bool) bool {
	if want(p) {
		return true
	}
	deadline := time.After(timeout)
	for {
		select {
		case m, ok := <-p.msgs:
			if !ok {
				return false
			}
			p.absorb(m)
			if want(p) {
				return true
			}
		case <-deadline:
			return want(p)
		}
	}
}

func (p *peer) text() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doc.Text()
}

func (p *peer) stateVector() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doc.StateVector()
}

// type inserts s at the end of the document and sends the update, the way
// a browser sends a batched keystroke buffer.
func (p *peer) typeText(s string) {
	u := p.insertLocal(s)
	if u == nil {
		return
	}
	p.send(ClientMessage{Type: msgUpdate, Note: p.note, Update: u, Author: p.author})
}

// insertLocal mutates the local document without sending anything, the way
// an offline client edits. It returns the update to send later.
func (p *peer) insertLocal(s string) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doc.Insert(utf16Len(p.doc.Text()), s, "local")
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// drainPending absorbs anything already queued without waiting.
func (p *peer) drainPending() {
	for {
		select {
		case m, ok := <-p.msgs:
			if !ok {
				return
			}
			p.absorb(m)
		default:
			return
		}
	}
}

// quiet reports whether nothing arrives for d.
func (p *peer) quiet(d time.Duration) bool {
	select {
	case m, ok := <-p.msgs:
		if !ok {
			return true
		}
		p.absorb(m)
		p.t.Errorf("%s: unexpected message %q", p.author, m.Type)
		return false
	case <-time.After(d):
		return true
	}
}

// --- tests ---------------------------------------------------------------

// TestLiveEditingConverges: two peers on one note, per-update fan-out both
// ways, an external filesystem edit broadcast to the room, and three-way
// convergence of document, file, and index at the WebSocket layer.
func TestLiveEditingConverges(t *testing.T) {
	st := newStack(t, testHubOptions())
	id := st.newNote("alpha/one.md", "hello world\n")

	a := newPeer(t, "user:ada")
	a.dial(st.url)
	defer a.close()
	if m := a.subscribe(id, nil); len(m.Update) == 0 {
		t.Fatal("empty document state in subscribed reply")
	}
	if a.text() != "hello world\n" {
		t.Fatalf("ada got %q", a.text())
	}

	b := newPeer(t, "user:ben")
	b.dial(st.url)
	defer b.close()
	b.subscribe(id, nil)
	if b.text() != "hello world\n" {
		t.Fatalf("ben got %q", b.text())
	}

	a.typeText("ada types this.\n")
	if !b.drainUntil(waitFor, func(p *peer) bool { return p.text() == "hello world\nada types this.\n" }) {
		t.Fatalf("ben did not get ada's update: %q", b.text())
	}
	b.typeText("ben replies.\n")
	if !a.drainUntil(waitFor, func(p *peer) bool { return p.text() == "hello world\nada types this.\nben replies.\n" }) {
		t.Fatalf("ada did not get ben's update: %q", a.text())
	}
	st.converged(id, a, b)

	// An external `echo >> note.md` reaches the room as an update authored
	// by the filesystem.
	st.appendFile("alpha/one.md", "from disk\n")
	want := "hello world\nada types this.\nben replies.\nfrom disk\n"
	for _, p := range []*peer{a, b} {
		if !p.drainUntil(waitFor, func(p *peer) bool { return p.text() == want }) {
			t.Fatalf("%s missed the filesystem edit: %q", p.author, p.text())
		}
	}
	st.converged(id, a, b)

	// The sender does not receive its own update back.
	a.typeText("echo check\n")
	if !a.quiet(250 * time.Millisecond) {
		t.Fatal("ada received her own update back")
	}
}

// TestReconnectDeltaAndOfflineMerge: a peer that drops, misses edits, makes
// its own offline edits, and reconnects receives only the delta and merges
// both directions.
func TestReconnectDeltaAndOfflineMerge(t *testing.T) {
	st := newStack(t, testHubOptions())
	body := strings.Repeat("initial state fills the document so a full state is clearly larger than a delta.\n", 20)
	id := st.newNote("alpha/two.md", body)

	a := newPeer(t, "user:ada")
	a.dial(st.url)
	a.subscribe(id, nil)
	b := newPeer(t, "user:ben")
	b.dial(st.url)
	defer b.close()
	b.subscribe(id, nil)

	a.typeText("first edit\n")
	st.converged(id, a, b)
	a.close() // ada drops

	// While ada is offline, ben and the filesystem keep editing.
	b.typeText("ben kept typing.\n")
	st.appendFile("alpha/two.md", "disk grew too.\n")
	st.converged(id, b)

	// Ada also edits offline, without a connection.
	offline := a.insertLocal("ada was offline.\n")

	// Reconnect: subscribe with the state vector, receive only the delta.
	a.dial(st.url)
	defer a.close()
	m := a.subscribe(id, a.stateVector())
	full := len(a.doc.State()) + len(body)
	if len(m.Update) >= full {
		t.Fatalf("delta (%d bytes) is not smaller than the document (%d)", len(m.Update), full)
	}
	if !strings.Contains(a.text(), "ben kept typing.") || !strings.Contains(a.text(), "disk grew too.") {
		t.Fatalf("delta did not bring ada up to date: %q", a.text())
	}
	// Both directions: ada pushes the edit made while offline.
	a.send(ClientMessage{Type: msgUpdate, Note: id, Update: offline, Author: a.author})
	if !b.drainUntil(waitFor, func(p *peer) bool {
		return strings.Contains(p.text(), "ada was offline.")
	}) {
		t.Fatalf("ben missed ada's offline edit: %q", b.text())
	}
	st.converged(id, a, b)
}

// TestServerRestartConverges: killing the server mid-session loses nothing.
// Peers keep their local documents, reconnect to a fresh process over the
// same tree, and converge; editing keeps working afterwards.
func TestServerRestartConverges(t *testing.T) {
	dir := t.TempDir()
	st := openStack(t, dir, testHubOptions())
	id := st.newNote("alpha/three.md", "before the crash\n")

	a := newPeer(t, "user:ada")
	a.dial(st.url)
	b := newPeer(t, "user:ben")
	b.dial(st.url)
	a.subscribe(id, nil)
	b.subscribe(id, nil)
	a.typeText("ada typed.\n")
	b.typeText("ben typed.\n")
	before := st.converged(id, a, b)

	// Kill the server: listener, relay, loop, index — everything.
	st.close()

	// Restart over the same tree.
	st2 := openStack(t, dir, testHubOptions())
	defer st2.close()
	a.dial(st2.url)
	defer a.close()
	b.dial(st2.url)
	defer b.close()
	a.subscribe(id, a.stateVector())
	b.subscribe(id, b.stateVector())
	if a.text() != before || b.text() != before {
		t.Fatalf("lost text across restart: ada=%q ben=%q want=%q", a.text(), b.text(), before)
	}
	// Editing still works after the restart.
	a.typeText("after restart.\n")
	if !b.drainUntil(waitFor, func(p *peer) bool {
		return strings.Contains(p.text(), "after restart.")
	}) {
		t.Fatalf("ben missed the post-restart edit: %q", b.text())
	}
	st2.converged(id, a, b)
}

// TestAwarenessRelayedNotPersisted: presence payloads broadcast to the room
// but never touch the update log.
func TestAwarenessRelayedNotPersisted(t *testing.T) {
	st := newStack(t, testHubOptions())
	id := st.newNote("alpha/four.md", "body\n")

	a := newPeer(t, "user:ada")
	a.dial(st.url)
	defer a.close()
	a.subscribe(id, nil)
	b := newPeer(t, "user:ben")
	b.dial(st.url)
	defer b.close()
	b.subscribe(id, nil)

	_, rows, err := st.db.LogStats(st.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	cursor := []byte(`{"cursor":{"from":3,"to":7},"user":{"name":"Ada","color":"#f60"}}`)
	a.send(ClientMessage{Type: msgAwareness, Note: id, Payload: cursor})
	m, ok := b.waitFor(msgAwareness, waitFor)
	if !ok {
		t.Fatal("ben never saw the awareness payload")
	}
	if string(m.Payload) != string(cursor) {
		t.Fatalf("awareness payload altered: %q", m.Payload)
	}
	if !a.quiet(250 * time.Millisecond) {
		t.Fatal("ada received her own awareness back")
	}
	if _, rowsNow, err := st.db.LogStats(st.ctx, id); err != nil {
		t.Fatal(err)
	} else if rowsNow != rows {
		t.Fatalf("awareness was persisted: %d log rows became %d", rows, rowsNow)
	}
}

// TestUpdateRateLimit: a burst past the per-author limit is refused with an
// error and counted, not silently dropped.
func TestUpdateRateLimit(t *testing.T) {
	st := newStack(t, Options{Limiter: pathsafe.NewRateLimiter(
		pathsafe.Rate{N: 3, Window: time.Minute}, pathsafe.Rate{N: 3, Window: time.Minute},
	)})
	id := st.newNote("alpha/five.md", "body\n")
	a := newPeer(t, "user:ada")
	a.dial(st.url)
	defer a.close()
	a.subscribe(id, nil)

	rateLimited := false
	for i := 0; i < 8; i++ {
		a.typeText(fmt.Sprintf("burst %d\n", i))
	}
	deadline := time.After(waitFor)
	for !rateLimited {
		select {
		case m, ok := <-a.msgs:
			if !ok {
				t.Fatal("connection closed before a rate-limit error")
			}
			a.absorb(m)
			if m.Type == msgError && m.Code == errRateLimited {
				rateLimited = true
			}
		case <-deadline:
			t.Fatal("no rate-limit error after a burst")
		}
	}
	if s := st.hub.Stats(); s.UpdatesDropped == 0 {
		t.Fatal("rate-limited updates not counted in stats")
	}
}

// TestConnectionLimits: the per-connection room cap and the inbound frame
// size cap are enforced.
func TestConnectionLimits(t *testing.T) {
	st := newStack(t, Options{MaxRoomsPerConn: 2, MaxMessageBytes: 4096})
	ids := []string{
		st.newNote("a/one.md", "one\n"),
		st.newNote("a/two.md", "two\n"),
		st.newNote("a/three.md", "three\n"),
	}

	// A connection may join at most two rooms; the third is refused. This
	// peer only checks the error replies, so it shares one document across
	// rooms deliberately.
	rooms := newPeer(t, "user:roomy")
	rooms.dial(st.url)
	defer rooms.close()
	rooms.subscribe(ids[0], nil)
	rooms.subscribe(ids[1], nil)
	rooms.send(ClientMessage{Type: msgSubscribe, Note: ids[2], Author: rooms.author})
	m, ok := rooms.waitFor(msgError, waitFor)
	if !ok || m.Code != errTooManyRooms {
		t.Fatalf("expected a too_many_rooms error, got %+v", m)
	}

	// An oversized frame closes the connection; the hub survives it and
	// the peer count drops.
	big := make([]byte, 8192)
	for i := range big {
		big[i] = 'x'
	}
	b := newPeer(t, "user:ben")
	b.dial(st.url)
	b.subscribe(ids[0], nil)
	b.send(ClientMessage{Type: msgUpdate, Note: ids[0], Update: big, Author: b.author})
	if !st.eventually(waitFor, func() bool { return st.hub.Stats().Connections == 1 }) {
		t.Fatalf("connection survived an oversized frame: %+v", st.hub.Stats())
	}

	// Live editing keeps working.
	a := newPeer(t, "user:ada")
	a.dial(st.url)
	defer a.close()
	a.subscribe(ids[0], nil)
	a.typeText("still here\n")
	st.converged(ids[0], a)
}

// TestUnsubscribeAndRoomLifecycle: leaving stops delivery; the room and its
// pin go away when the last member leaves.
func TestUnsubscribeAndRoomLifecycle(t *testing.T) {
	st := newStack(t, testHubOptions())
	id := st.newNote("a/lifecycle.md", "body\n")

	a := newPeer(t, "user:ada")
	a.dial(st.url)
	defer a.close()
	a.subscribe(id, nil)
	b := newPeer(t, "user:ben")
	b.dial(st.url)
	defer b.close()
	b.subscribe(id, nil)
	if s := st.hub.Stats(); s.Rooms != 1 || s.Subscriptions != 2 {
		t.Fatalf("room state: %+v", s)
	}

	a.send(ClientMessage{Type: msgUnsubscribe, Note: id})
	if !st.eventually(waitFor, func() bool {
		s := st.hub.Stats()
		return s.Subscriptions == 1 && s.Rooms == 1
	}) {
		t.Fatalf("unsubscribe did not leave one member: %+v", st.hub.Stats())
	}
	b.typeText("ben alone\n")
	if !a.quiet(250 * time.Millisecond) {
		t.Fatal("ada received updates after unsubscribing")
	}
	st.converged(id, b)

	b.send(ClientMessage{Type: msgUnsubscribe, Note: id})
	if !st.eventually(waitFor, func() bool { return st.hub.Stats().Rooms == 0 }) {
		t.Fatalf("empty room lingered: %+v", st.hub.Stats())
	}
}

// TestPingPongAndAuthorValidation covers the small protocol pieces.
func TestPingPongAndAuthorValidation(t *testing.T) {
	st := newStack(t, testHubOptions())
	id := st.newNote("a/misc.md", "body\n")
	a := newPeer(t, "user:ada")
	a.dial(st.url)
	defer a.close()
	a.subscribe(id, nil)

	a.send(ClientMessage{Type: msgPing})
	if _, ok := a.waitFor(msgPong, waitFor); !ok {
		t.Fatal("no pong for ping")
	}

	for _, author := range []string{"filesystem", "nolabel", "user:", "user:has space"} {
		a.send(ClientMessage{Type: msgUpdate, Note: id, Update: []byte{0, 1, 2}, Author: author})
		m, ok := a.waitFor(msgError, waitFor)
		if !ok || m.Code != errInvalidAuthor {
			t.Fatalf("author %q accepted: %+v", author, m)
		}
	}

	a.send(ClientMessage{Type: msgUpdate, Note: "not-a-real-id", Update: []byte{0, 1}, Author: "user:ada"})
	m, ok := a.waitFor(msgError, waitFor)
	if !ok || m.Code != errInvalid {
		t.Fatalf("bad note id accepted: %+v", m)
	}
}

// TestTwentyConnectionsSustainedTyping: 20 connections across 5 notes with
// sustained typing converge, keep bounded room state, and leave no
// goroutines behind.
func TestTwentyConnectionsSustainedTyping(t *testing.T) {
	st := newStack(t, testHubOptions())
	const notes, peersPerNote, batches = 5, 4, 20

	base := runtime.NumGoroutine()
	var wg sync.WaitGroup
	type room struct {
		id    string
		peers []*peer
	}
	rooms := make([]room, notes)
	for i := range rooms {
		rooms[i].id = st.newNote(fmt.Sprintf("load/n%d.md", i), fmt.Sprintf("note %d\n", i))
		for j := 0; j < peersPerNote; j++ {
			p := newPeer(t, fmt.Sprintf("user:p%d-%d", i, j))
			p.dial(st.url)
			p.subscribe(rooms[i].id, nil)
			rooms[i].peers = append(rooms[i].peers, p)
		}
	}
	if s := st.hub.Stats(); s.Connections != notes*peersPerNote || s.Rooms != notes {
		t.Fatalf("unexpected hub state: %+v", s)
	}

	// Every peer types its own batches into its note, concurrently.
	for i := range rooms {
		for _, p := range rooms[i].peers {
			wg.Add(1)
			go func(p *peer, i int) {
				defer wg.Done()
				for k := 0; k < batches; k++ {
					p.typeText(fmt.Sprintf("w%s-%d ", p.author, k))
					time.Sleep(2 * time.Millisecond)
				}
			}(p, i)
		}
	}
	wg.Wait()

	for i := range rooms {
		for _, p := range rooms[i].peers {
			got := p
			note := i
			ok := got.drainUntil(waitFor, func(p *peer) bool {
				want := fmt.Sprintf("note %d\n", note)
				for k := 0; k < batches; k++ {
					want += fmt.Sprintf("w%s-%d ", p.author, k)
				}
				return p.text() == want
			})
			if !ok {
				t.Fatalf("peer %s did not settle on note %d: %q", got.author, note, got.text())
			}
		}
		st.converged(rooms[i].id, rooms[i].peers...)
	}

	// Close every peer; connections and rooms drain to zero and no
	// goroutines linger.
	for i := range rooms {
		for _, p := range rooms[i].peers {
			p.close()
		}
	}
	if !st.eventually(waitFor, func() bool { return st.hub.Stats().Connections == 0 && st.hub.Stats().Rooms == 0 }) {
		t.Fatalf("connections did not drain: %+v", st.hub.Stats())
	}
	if !st.eventually(waitFor, func() bool { return runtime.NumGoroutine() <= base+5 }) {
		t.Fatalf("goroutines leaked: %d at start, %d after", base, runtime.NumGoroutine())
	}
}
