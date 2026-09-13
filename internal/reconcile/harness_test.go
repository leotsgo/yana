package reconcile

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/ydoc"
)

// Timings for tests: fast enough to keep the suite short, slow enough
// that the debounce and settle windows still mean something.
const (
	tIdle     = 100 * time.Millisecond
	tDebounce = 30 * time.Millisecond
	tSettle   = 300 * time.Millisecond
)

type harness struct {
	t    testing.TB
	ctx  context.Context
	dir  string
	root *pathsafe.Root
	db   *index.DB
	sc   *scanner.Scanner
	rec  *Reconciler
	log  *slog.Logger
	opts Options
}

func testLogger(t testing.TB) *slog.Logger {
	if os.Getenv("YANA_TEST_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testOptions() Options {
	return Options{IdleTime: tIdle, Debounce: tDebounce, SettleTime: tSettle, UnloadAfter: -1, CompactAfter: 500}
}

// newHarness opens a reconciler over a fresh temp tree (or dir when set).
func newHarness(t testing.TB, dir string, opts Options) *harness {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	log := testLogger(t)
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	sc := scanner.New(root, db, scanner.Options{SettleTime: opts.SettleTime}, log)
	h := &harness{t: t, ctx: context.Background(), dir: dir, root: root, db: db, sc: sc, log: log, opts: opts}
	h.rec = New(root, db, sc, opts, log)
	if err := h.rec.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(h.ctx); err != nil {
		t.Fatal(err)
	}
	h.rec.SweepOrphans(h.ctx)
	return h
}

// close flushes and shuts everything down.
func (h *harness) close() {
	h.t.Helper()
	if err := h.rec.Close(); err != nil {
		h.t.Error(err)
	}
	if err := h.db.Close(); err != nil {
		h.t.Error(err)
	}
}

func (h *harness) abs(rel string) string { return filepath.Join(h.dir, filepath.FromSlash(rel)) }

// newNote writes a note file with an id already assigned, indexes it, and
// returns the id.
func (h *harness) newNote(rel, body string) string {
	h.t.Helper()
	id := scanner.NewID(time.Now())
	content := "---\nid: " + id + "\ncreated: 2026-09-12T14:02:11Z\n---\n" + body
	if err := os.MkdirAll(filepath.Dir(h.abs(rel)), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(h.abs(rel), []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	if err := h.sc.ScanOne(h.ctx, rel); err != nil {
		h.t.Fatal(err)
	}
	return id
}

func (h *harness) read(rel string) string {
	h.t.Helper()
	b, err := os.ReadFile(h.abs(rel))
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

func (h *harness) fileBody(rel string) string {
	b, err := os.ReadFile(h.abs(rel))
	if err != nil {
		return "<missing: " + err.Error() + ">"
	}
	return string(frontmatter.Parse(b).Body)
}

// appendFile is `echo >> note.md`.
func (h *harness) appendFile(rel, s string) {
	h.t.Helper()
	f, err := os.OpenFile(h.abs(rel), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		h.t.Fatal(err)
	}
	f.Close()
}

// replaceFile is a truncate+write editor save of the whole file, keeping
// the existing frontmatter.
func (h *harness) replaceFile(rel, body string) {
	h.t.Helper()
	cur, err := os.ReadFile(h.abs(rel))
	if err != nil {
		h.t.Fatal(err)
	}
	head := frontmatter.Parse(cur).Head
	if err := os.WriteFile(h.abs(rel), append(append([]byte{}, head...), body...), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

// eventually polls cond until it holds or the deadline passes.
func (h *harness) eventually(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// converged waits for the document, the file, and the index to agree and
// returns the agreed body. It fails the test otherwise.
func (h *harness) converged(id string, clients ...*client) string {
	h.t.Helper()
	var doc, file, idx string
	var rel string
	ok := h.eventually(20*tSettle, func() bool {
		var err error
		rel, err = h.rec.Path(h.ctx, id)
		if err != nil {
			return false
		}
		doc, err = h.rec.Text(h.ctx, id)
		if err != nil {
			return false
		}
		file = h.fileBody(rel)
		idx, err = h.db.Body(h.ctx, id)
		if err != nil {
			return false
		}
		if doc != file || idx != doc {
			return false
		}
		for _, c := range clients {
			if c.text() != doc {
				return false
			}
		}
		return true
	})
	if !ok {
		var cs []string
		for _, c := range clients {
			cs = append(cs, fmt.Sprintf("%s=%q", c.name, c.text()))
		}
		h.t.Fatalf("not converged for %s at %s:\n doc=%q\n file=%q\n index=%q\n clients=%s", id, rel, doc, file, idx, strings.Join(cs, " "))
	}
	return doc
}

// client is a headless Yjs peer talking to the reconciler the way a
// browser would through the Phase 3 relay: its own document, updates
// pushed with ApplyUpdate, and everything else received through Subscribe.
type client struct {
	h      *harness
	name   string
	id     string
	doc    *ydoc.Doc
	mu     sync.Mutex
	cancel func()
}

func (h *harness) newClient(name, id string) *client {
	h.t.Helper()
	state, err := h.rec.State(h.ctx, id)
	if err != nil {
		h.t.Fatal(err)
	}
	doc, err := ydoc.Load(state)
	if err != nil {
		h.t.Fatal(err)
	}
	c := &client{h: h, name: name, id: id, doc: doc}
	c.cancel = h.rec.Subscribe(func(ev Event) {
		if ev.NoteID != id || ev.Kind != EventUpdate || ev.Source == c {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if _, err := c.doc.Apply(ev.Update, "remote"); err != nil {
			h.t.Errorf("%s: apply broadcast: %v", name, err)
		}
	})
	return c
}

func (c *client) close() { c.cancel() }

func (c *client) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.doc.Text()
}

// send pushes an update to the server under this client's author.
func (c *client) send(u []byte) {
	if u == nil {
		return
	}
	if err := c.h.rec.ApplyUpdate(c.h.ctx, c.id, u, "user:"+c.name, c); err != nil {
		c.h.t.Errorf("%s: apply update: %v", c.name, err)
	}
}

// insertAt types s at a UTF-16 position clamped to the current length.
func (c *client) insertAt(pos int, s string) {
	c.mu.Lock()
	n := utf16Len(c.doc.Text())
	if pos > n {
		pos = n
	}
	u := c.doc.Insert(pos, s, "local")
	c.mu.Unlock()
	c.send(u)
}

// insertLine types a whole line at the start of a random line.
func (c *client) insertLine(rng *rand.Rand, s string) {
	c.mu.Lock()
	cur := c.doc.Text()
	starts := []int{0}
	pos := 0
	for _, r := range cur {
		if r >= 0x10000 {
			pos += 2
		} else {
			pos++
		}
		if r == '\n' {
			starts = append(starts, pos)
		}
	}
	u := c.doc.Insert(starts[rng.Intn(len(starts))], s, "local")
	c.mu.Unlock()
	c.send(u)
}

// appendText types s at the end.
func (c *client) appendText(s string) {
	c.mu.Lock()
	u := c.doc.Insert(utf16Len(c.doc.Text()), s, "local")
	c.mu.Unlock()
	c.send(u)
}

// deleteAt removes n units at pos, clamped.
func (c *client) deleteAt(pos, n int) {
	c.mu.Lock()
	l := utf16Len(c.doc.Text())
	if pos >= l {
		c.mu.Unlock()
		return
	}
	if pos+n > l {
		n = l - pos
	}
	u := c.doc.Delete(pos, n, "local")
	c.mu.Unlock()
	c.send(u)
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

// loadForTest decodes a sidecar and returns its text.
func loadForTest(state []byte) (string, error) {
	d, err := ydoc.Load(state)
	if err != nil {
		return "", err
	}
	defer d.Close()
	return d.Text(), nil
}
