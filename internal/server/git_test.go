package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// gitEnv is an env with a reconciliation loop and a live git history
// layer, wired the way main wires them.
type gitEnv struct {
	*env
	rec *reconcile.Reconciler
	gl  *git.Layer
	dir string
}

func newGitEnv(t *testing.T) *gitEnv {
	t.Helper()
	dir := t.TempDir()
	abs := func(rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }
	write := func(rel, content string) {
		os.MkdirAll(filepath.Dir(abs(rel)), 0o755)
		os.WriteFile(abs(rel), []byte(content), 0o644)
		old := time.Now().Add(-time.Minute)
		os.Chtimes(abs(rel), old, old)
	}
	write("home/hist.md", "first version\n")

	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sc := scanner.New(root, db, scanner.Options{SettleTime: time.Millisecond}, nil)
	if _, err := sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	gl := git.New(dir, git.Options{Quiet: time.Hour, Interval: time.Hour, DB: db}, nil)
	if err := gl.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec := reconcile.New(root, db, sc, reconcile.Options{
		IdleTime: 60 * time.Millisecond, Debounce: 20 * time.Millisecond,
		SettleTime: 150 * time.Millisecond, UnloadAfter: -1, OnTreeChange: gl.Notify,
	}, nil)
	if err := rec.Start(); err != nil {
		t.Fatal(err)
	}
	gl.Attach(rec)
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>YANA/</title>")}}
	srv := New(Deps{DB: db, Root: root, Web: web, Version: "test", Scanner: sc, Sync: rec, Git: gl})
	srv.SetReady(true)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	t.Cleanup(func() {
		rec.Close()
		gl.Close()
	})
	return &gitEnv{env: &env{dir: dir, srv: srv, ts: ts, db: db}, rec: rec, gl: gl, dir: dir}
}

func (e *gitEnv) post(t *testing.T, path string, body any, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(e.ts.URL+path, "application/json", &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && resp.StatusCode < 500 {
			t.Fatalf("%s: decode: %v", path, err)
		}
	}
	return resp.StatusCode
}

func (e *gitEnv) fileBody() string {
	b, err := os.ReadFile(filepath.Join(e.dir, "home", "hist.md"))
	if err != nil {
		return "<missing>"
	}
	return string(frontmatter.Parse(b).Body)
}

func (e *gitEnv) eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("condition not reached; file=%q", e.fileBody())
}

func TestGitEndpointsUnavailableWithoutLayer(t *testing.T) {
	e := newEnv(t)
	if code := e.get(t, "/api/notes/01JQ8X4K2M9P7R3T5V6W8Y0Z1A/history", nil); code != 501 {
		t.Fatalf("history without git layer: %d", code)
	}
	if code := e.get(t, "/api/notes/01JQ8X4K2M9P7R3T5V6W8Y0Z1A/history/diff?from=aaaaaa&to=bbbbbb", nil); code != 501 {
		t.Fatalf("diff without git layer: %d", code)
	}
	resp, err := http.Post(e.ts.URL+"/api/git/snapshot", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 501 {
		t.Fatalf("snapshot without git layer: %d", resp.StatusCode)
	}
}

func TestNoteHistoryDiffAndRestore(t *testing.T) {
	e := newGitEnv(t)
	ctx := context.Background()

	// Baseline commit, then an agent rewrite, then another commit.
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	note, err := e.db.GetNoteByPath(ctx, "home/hist.md")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.rec.SetText(ctx, note.ID, "agent version\n", "agent:claude"); err != nil {
		t.Fatal(err)
	}
	e.eventually(t, func() bool { return e.fileBody() == "agent version\n" })
	if _, err := e.gl.Snapshot(ctx); err != nil {
		t.Fatal(err)
	}

	var hist struct {
		Entries []git.LogEntry `json:"entries"`
	}
	if code := e.get(t, "/api/notes/"+note.ID+"/history", &hist); code != 200 {
		t.Fatalf("history: %d", code)
	}
	if len(hist.Entries) != 2 {
		t.Fatalf("history entries: %+v", hist.Entries)
	}
	if hist.Entries[0].Name != "claude" || hist.Entries[0].Email != "agent@local" {
		t.Fatalf("newest entry not the agent's: %+v", hist.Entries[0])
	}
	old := hist.Entries[len(hist.Entries)-1]
	if old.Name != "yana user" {
		t.Fatalf("oldest entry not the human's: %+v", old)
	}

	var df struct{ Diff string }
	url := "/api/notes/" + note.ID + "/history/diff?from=" + old.Hash + "&to=" + hist.Entries[0].Hash
	if code := e.get(t, url, &df); code != 200 {
		t.Fatalf("diff: %d", code)
	}
	if !strings.Contains(df.Diff, "+agent version") || !strings.Contains(df.Diff, "-first version") {
		t.Fatalf("diff content:\n%s", df.Diff)
	}
	if code := e.get(t, "/api/notes/"+note.ID+"/history/diff?from=HEAD&to="+old.Hash, nil); code != 400 {
		t.Fatalf("diff accepted a non-hash revision")
	}

	// Restore the oldest revision: the old text returns as an edit through
	// the CRDT, so file, index, and document converge on it.
	var res map[string]any
	if code := e.post(t, "/api/notes/"+note.ID+"/history/restore", map[string]string{"revision": old.Hash, "path": old.Path}, &res); code != 200 {
		t.Fatalf("restore: %d %v", code, res)
	}
	idx, err := e.db.Body(ctx, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.eventually(t, func() bool {
		doc, err := e.rec.Text(ctx, note.ID)
		if err != nil {
			return false
		}
		idx, err = e.db.Body(ctx, note.ID)
		if err != nil {
			return false
		}
		return e.fileBody() == "first version\n" && doc == "first version\n" && idx == "first version\n"
	})
}

func TestGitSnapshotEndpoint(t *testing.T) {
	e := newGitEnv(t)
	var res map[string]any
	if code := e.post(t, "/api/git/snapshot", map[string]string{}, &res); code != 200 {
		t.Fatalf("snapshot: %d", code)
	}
	if res["ok"] != true {
		t.Fatalf("snapshot response: %v", res)
	}
	var st map[string]any
	e.get(t, "/api/status", &st)
	gitSt, ok := st["git"].(map[string]any)
	if !ok || gitSt["available"] != true {
		t.Fatalf("status carries no git block: %v", st)
	}
}
