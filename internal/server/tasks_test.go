package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// newSyncEnv is the tasks flavour of the test environment: a scanner, a
// reconciliation loop, and a note with tasks to tick.
func newSyncEnv(t *testing.T) (*env, *reconcile.Reconciler, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
		old := time.Now().Add(-time.Minute)
		os.Chtimes(p, old, old)
	}
	id := scanner.NewID(time.Now())
	write("home/list.md", "---\nid: "+id+"\n---\n# List\n\n## Shopping\n\n- [ ] buy milk\n- [x] bread\n  - [ ] nested\n")
	write("work/plain.md", "# Plain\n\nnothing\n")

	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sc := scanner.New(root, db, scanner.Options{SettleTime: time.Millisecond}, log)
	rec := reconcile.New(root, db, sc, reconcile.Options{UnloadAfter: -1}, log)
	if err := rec.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := rec.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>YANA/</title>")}}
	srv := New(Deps{DB: db, Root: root, Web: web, Version: "test", Sync: rec, Log: log})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &env{dir: dir, srv: srv, ts: ts, db: db}, rec, id
}

func (e *env) patch(t *testing.T, path string, body any) int {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPatch, e.ts.URL+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestTasksListed(t *testing.T) {
	e, _, _ := newSyncEnv(t)
	var res struct {
		Tasks []index.Task
	}
	if code := e.get(t, "/api/tasks", &res); code != 200 {
		t.Fatalf("tasks %d", code)
	}
	if len(res.Tasks) != 2 {
		t.Fatalf("tasks: %+v", res.Tasks)
	}
	var open index.Task
	for _, tk := range res.Tasks {
		if tk.Line == 4 {
			open = tk
		}
	}
	if open.Note.ID == "" || open.Heading != "Shopping" || open.Text == "" {
		t.Fatalf("task on line 4: %+v", open)
	}
	var count struct {
		Count int
	}
	if code := e.get(t, "/api/tasks?count=1", &count); code != 200 || count.Count != 2 {
		t.Fatalf("count %d %+v", code, count)
	}
	var doneRes struct {
		Tasks []index.Task
	}
	if code := e.get(t, "/api/tasks?done=true", &doneRes); code != 200 || len(doneRes.Tasks) != 1 {
		t.Fatalf("done %d %+v", code, doneRes.Tasks)
	}
	var bySpace struct {
		Tasks []index.Task
	}
	if code := e.get(t, "/api/tasks?space=work", &bySpace); code != 200 || len(bySpace.Tasks) != 0 {
		t.Fatalf("space filter %d %+v", code, bySpace.Tasks)
	}
	// A space with nothing in it reads as empty, the way the tree does
	// without accounts; membership is the authed server's gate.
	if code := e.get(t, "/api/tasks?space=nope", &bySpace); code != 200 || len(bySpace.Tasks) != 0 {
		t.Fatalf("unknown space %d", code)
	}
}

func TestTaskTickFlipsTheFile(t *testing.T) {
	e, rec, id := newSyncEnv(t)
	code := e.patch(t, "/api/tasks", map[string]any{"note": id, "line": 4, "done": true})
	if code != 200 {
		t.Fatalf("tick %d", code)
	}
	if err := rec.Flush(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, "home", "list.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "- [x] buy milk") {
		t.Fatalf("file not ticked:\n%s", raw)
	}
	var res struct {
		Tasks []index.Task
	}
	e.get(t, "/api/tasks?done=true", &res)
	if len(res.Tasks) != 2 {
		t.Fatalf("done after tick: %+v", res.Tasks)
	}

	// Untick works the same way, through the same write.
	code = e.patch(t, "/api/tasks", map[string]any{"note": id, "line": 4, "done": false})
	if code != 200 {
		t.Fatalf("untick %d", code)
	}
	if err := rec.Flush(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(e.dir, "home", "list.md"))
	if !strings.Contains(string(raw), "- [ ] buy milk") {
		t.Fatalf("file not unticked:\n%s", raw)
	}

	// A line that is not a box refuses with 409 instead of writing.
	if code := e.patch(t, "/api/tasks", map[string]any{"note": id, "line": 0, "done": true}); code != 409 {
		t.Fatalf("non-task line %d", code)
	}
	if code := e.patch(t, "/api/tasks", map[string]any{"note": id, "line": 99, "done": true}); code != 409 {
		t.Fatalf("missing line %d", code)
	}
	// An already-done box set to done is the no-op the client retries on.
	if code := e.patch(t, "/api/tasks", map[string]any{"note": id, "line": 5, "done": true}); code != 200 {
		t.Fatalf("idempotent tick %d", code)
	}
}

func TestTaskTickNeedsRealtime(t *testing.T) {
	// The plain env has no reconciliation loop: the tick answers 501.
	e := newEnv(t)
	var note struct {
		ID string
	}
	var tree struct {
		Spaces []SpaceTree
	}
	e.get(t, "/api/tree", &tree)
	for _, sp := range tree.Spaces {
		if sp.Name == "home" {
			note.ID = findNoteID(sp.Children)
		}
	}
	if note.ID == "" {
		t.Fatal("no note found")
	}
	if code := e.patch(t, "/api/tasks", map[string]any{"note": note.ID, "line": 1, "done": true}); code != 501 {
		t.Fatalf("tick without sync %d", code)
	}
}

func findNoteID(nodes []*TreeNode) string {
	for _, n := range nodes {
		if n.Type == "note" && n.ID != "" {
			return n.ID
		}
		if id := findNoteID(n.Children); id != "" {
			return id
		}
	}
	return ""
}
