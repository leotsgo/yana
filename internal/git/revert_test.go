package git_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/ydoc"
)

func quietLog() *slog.Logger {
	if os.Getenv("YANA_TEST_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// stack is a reconciler plus a git layer over one temp tree, wired the way
// main wires them.
type stack struct {
	dir  string
	rec  *reconcile.Reconciler
	db   *index.DB
	sc   *scanner.Scanner
	gl   *git.Layer
	log  *slog.Logger
	ctx  context.Context
	rel  string
	id   string
	opts git.Options
}

func newStack(t testing.TB, opts git.Options, recOpts reconcile.Options) *stack {
	t.Helper()
	dir := t.TempDir()
	log := quietLog()
	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	sc := scanner.New(root, db, scanner.Options{SettleTime: recOpts.SettleTime}, log)
	opts.DB = db
	gl := git.New(dir, opts, log)
	ctx := context.Background()
	if err := gl.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	recOpts.OnTreeChange = gl.Notify
	rec := reconcile.New(root, db, sc, recOpts, log)
	if err := rec.Start(); err != nil {
		t.Fatal(err)
	}
	gl.Attach(rec)
	if _, err := sc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	return &stack{dir: dir, rec: rec, db: db, sc: sc, gl: gl, log: log, ctx: ctx, opts: opts}
}

func (s *stack) close(t testing.TB) {
	t.Helper()
	if err := s.rec.Close(); err != nil {
		t.Fatal(err)
	}
	s.gl.Close()
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
}

// newNote writes an id-carrying note and indexes it.
func (s *stack) newNote(t testing.TB, rel, body string) string {
	t.Helper()
	id := scanner.NewID(time.Now())
	content := "---\nid: " + id + "\ncreated: 2026-09-12T14:02:11Z\n---\n" + body
	abs := filepath.Join(s.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.sc.ScanOne(s.ctx, rel); err != nil {
		t.Fatal(err)
	}
	s.rel, s.id = rel, id
	return id
}

func (s *stack) fileBody() string {
	b, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(s.rel)))
	if err != nil {
		return "<missing>"
	}
	return string(frontmatter.Parse(b).Body)
}

func (s *stack) eventually(t testing.TB, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not reached in %v: file=%q doc-set", d, s.fileBody())
}

// converged waits for the document, the file, and the index to hold body.
func (s *stack) converged(t testing.TB, body string) {
	t.Helper()
	s.eventually(t, 5*time.Second, func() bool {
		doc, err := s.rec.Text(s.ctx, s.id)
		if err != nil {
			return false
		}
		idx, err := s.db.Body(s.ctx, s.id)
		if err != nil {
			return false
		}
		return doc == body && s.fileBody() == body && idx == body
	})
}

// openClient stands in for a browser with the note open: its own document,
// receiving everything the reconciliation loop broadcasts.
type openClient struct {
	doc *ydoc.Doc
	mu  sync.Mutex
}

func (s *stack) newClient(t testing.TB) *openClient {
	t.Helper()
	state, err := s.rec.State(s.ctx, s.id)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ydoc.Load(state)
	if err != nil {
		t.Fatal(err)
	}
	c := &openClient{doc: doc}
	unsub := s.rec.Subscribe(func(ev reconcile.Event) {
		if ev.NoteID != s.id || ev.Kind != reconcile.EventUpdate {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if _, err := c.doc.Apply(ev.Update, "remote"); err != nil {
			t.Errorf("client apply: %v", err)
		}
	})
	t.Cleanup(unsub)
	return c
}

func (c *openClient) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.doc.Text()
}

func (s *stack) clientConverged(t testing.TB, c *openClient, body string) {
	t.Helper()
	s.eventually(t, 5*time.Second, func() bool { return c.text() == body })
}

// TestRevertOfAgentCommitConverges is the Phase 7 acceptance: reverting an
// agent commit from the shell yields the right tree, and a client with the
// note open converges to the reverted text.
func TestRevertOfAgentCommitConverges(t *testing.T) {
	s := newStack(t, git.Options{Quiet: 60 * time.Millisecond, Interval: time.Hour},
		reconcile.Options{IdleTime: 100 * time.Millisecond, Debounce: 30 * time.Millisecond, SettleTime: 300 * time.Millisecond, UnloadAfter: -1})
	defer s.close(t)

	id := s.newNote(t, "home/revert.md", "original text\n")
	client := s.newClient(t)

	if err := s.rec.SetText(s.ctx, id, "original text\n", "user:fox"); err != nil {
		t.Fatal(err)
	}
	s.converged(t, "original text\n")
	if _, err := s.gl.Snapshot(s.ctx); err != nil {
		t.Fatal(err)
	}

	// The agent rewrites the note.
	if err := s.rec.SetText(s.ctx, id, "agent rewrite\n", "agent:claude"); err != nil {
		t.Fatal(err)
	}
	s.converged(t, "agent rewrite\n")
	s.clientConverged(t, client, "agent rewrite\n")
	n, err := s.gl.Snapshot(s.ctx)
	if err != nil || n != 1 {
		t.Fatalf("snapshot after the agent edit: %d commits, err %v", n, err)
	}
	got := authors(t, s.dir)
	// The agent edit is its own commit, and the file-made changes now
	// carry the filesystem identity rather than the human one.
	if len(got) != 3 || got[0] != "claude <agent@local>" || got[1] != "yana user <user@yana.local>" || got[2] != "filesystem <"+git.FilesystemEmail+">" {
		t.Fatalf("git log does not distinguish the agent edit: %v", got)
	}

	// The user reverts the agent commit from the shell.
	out := runGit(t, s.dir, "-c", "user.name=shell", "-c", "user.email=shell@local", "revert", "--no-edit", "HEAD")
	t.Log(out)
	// The watcher delivers the change; Sync stands in for it deterministically.
	s.rec.Sync(s.ctx, s.rel)

	s.converged(t, "original text\n")
	s.clientConverged(t, client, "original text\n")
}

// TestAttributionSurvivesRestart covers the durable half of attribution:
// a layer that did not see the edits live still splits authors using the
// CRDT log.
func TestAttributionSurvivesRestart(t *testing.T) {
	s := newStack(t, git.Options{Quiet: 60 * time.Millisecond, Interval: time.Hour},
		reconcile.Options{IdleTime: 100 * time.Millisecond, Debounce: 30 * time.Millisecond, SettleTime: 300 * time.Millisecond, UnloadAfter: -1})
	defer s.close(t)

	id := s.newNote(t, "home/two.md", "start\n")
	if err := s.rec.SetText(s.ctx, id, "start\n", "user:fox"); err != nil {
		t.Fatal(err)
	}
	s.converged(t, "start\n")
	s.gl.Snapshot(s.ctx)
	if err := s.rec.SetText(s.ctx, id, "start\nagent line\n", "agent:claude"); err != nil {
		t.Fatal(err)
	}
	s.converged(t, "start\nagent line\n")

	// A fresh layer over the same tree: no live events, only the log.
	fresh := git.New(s.dir, s.opts, s.log)
	if err := fresh.Ensure(s.ctx); err != nil {
		t.Fatal(err)
	}
	n, err := fresh.Snapshot(s.ctx)
	if err != nil || n != 1 {
		t.Fatalf("restart snapshot: %d commits, err %v", n, err)
	}
	got := authors(t, s.dir)
	if len(got) != 3 || got[0] != "claude <agent@local>" {
		t.Fatalf("restart attribution lost the agent: %v", got)
	}
}
