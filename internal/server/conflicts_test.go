package server

import (
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

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// newConflictEnv builds a tree with one survivor and two flavours of
// conflict copy beside it: the timestamp shape the trash restore
// writes, and the dashed shape (with a -2) the HTML save writes.
func newConflictEnv(t *testing.T) *trashEnv {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
		old := time.Now().Add(-time.Minute)
		os.Chtimes(p, old, old)
	}
	write("main/a.md", "# A\n\nthe current text\n")
	write("main/a.conflict-20260923T121212.md", "# A\n\nthe older text\n")
	write("main/dash.html", "<h1>Dash</h1>\n<p>new</p>")
	write("main/dash.conflict-20260923-121212-2.html", "<h1>Dash</h1>\n<p>old</p>")
	write("main/plain.md", "# Plain\n")

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
	rec := reconcile.New(root, db, sc, reconcile.Options{
		IdleTime: 60 * time.Millisecond, Debounce: 20 * time.Millisecond,
		SettleTime: 150 * time.Millisecond, UnloadAfter: -1,
	}, nil)
	if err := rec.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rec.Close() })

	srv := New(Deps{
		DB: db, Root: root, Web: fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>YANA/</title>")}},
		Version: "test", Scanner: sc, Sync: rec,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &trashEnv{env: env{dir: dir, srv: srv, ts: ts, db: db}, rec: rec}
}

// post body for the resolve endpoint.
func (e *trashEnv) resolve(t *testing.T, id, action string, out any) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"action": action})
	resp, err := http.Post(e.ts.URL+"/api/conflicts/"+id+"/resolve", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && resp.StatusCode < 500 {
			t.Fatalf("resolve %s: decode: %v", action, err)
		}
	}
	return resp.StatusCode
}

func TestConflictsRecognised(t *testing.T) {
	e := newConflictEnv(t)
	ctx := context.Background()

	a, err := e.db.GetNoteByPath(ctx, "main/a.md")
	if err != nil {
		t.Fatal(err)
	}
	copy, err := e.db.GetNoteByPath(ctx, "main/a.conflict-20260923T121212.md")
	if err != nil {
		t.Fatal(err)
	}
	if copy.ConflictOf != a.ID {
		t.Fatalf("conflict_of = %q, want %q", copy.ConflictOf, a.ID)
	}
	if a.ConflictOf != "" {
		t.Fatalf("survivor marked as a conflict: %q", a.ConflictOf)
	}

	var list struct {
		Conflicts []index.ConflictRow `json:"conflicts"`
	}
	if code := e.get(t, "/api/conflicts", &list); code != 200 || len(list.Conflicts) != 2 {
		t.Fatalf("list %d %+v", code, list)
	}
	if list.Conflicts[0].Of == nil || list.Conflicts[0].Of.RelPath != "main/a.md" {
		t.Fatalf("row 1 survivor: %+v", list.Conflicts[0].Of)
	}

	var of struct {
		Conflicts []index.Note `json:"conflicts"`
	}
	if code := e.get(t, "/api/notes/"+a.ID+"/conflicts", &of); code != 200 || len(of.Conflicts) != 1 {
		t.Fatalf("of-note %d %+v", code, of)
	}

	var note struct {
		ConflictCount int `json:"conflict_count"`
	}
	if code := e.get(t, "/api/notes/"+a.ID, &note); code != 200 || note.ConflictCount != 1 {
		t.Fatalf("note payload %d %+v", code, note)
	}

	var status map[string]any
	e.get(t, "/api/status", &status)
	if status["conflicts"].(float64) != 2 {
		t.Fatalf("status conflicts: %v", status["conflicts"])
	}
}

func TestConflictTreeNesting(t *testing.T) {
	e := newConflictEnv(t)
	var tree struct {
		Spaces []SpaceTree `json:"spaces"`
	}
	if code := e.get(t, "/api/tree", &tree); code != 200 {
		t.Fatalf("tree %d", code)
	}
	var main *SpaceTree
	for i := range tree.Spaces {
		if tree.Spaces[i].Name == "main" {
			main = &tree.Spaces[i]
		}
	}
	if main == nil {
		t.Fatalf("no main space: %+v", tree.Spaces)
	}
	// a.md and dash.html each carry one nested conflict; plain.md is a
	// plain sibling; no .conflict- name sits at the directory level.
	for _, c := range main.Children {
		if c.Type == "note" && strings.Contains(c.Name, ".conflict-") && !c.Conflict {
			t.Fatalf("conflict listed as a sibling: %+v", c)
		}
		if c.Path == "main/a.md" {
			if len(c.Children) != 1 || !c.Children[0].Conflict || c.Children[0].Path != "main/a.conflict-20260923T121212.md" {
				t.Fatalf("a.md children: %+v", c.Children)
			}
		}
	}
}

func TestConflictDiff(t *testing.T) {
	e := newConflictEnv(t)
	copy, err := e.db.GetNoteByPath(context.Background(), "main/a.conflict-20260923T121212.md")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Diff   string                `json:"diff"`
		Mine   struct{ Path string } `json:"mine"`
		Theirs struct{ Path string } `json:"theirs"`
	}
	if code := e.get(t, "/api/conflicts/"+copy.ID+"/diff", &out); code != 200 {
		t.Fatalf("diff %d", code)
	}
	for _, want := range []string{"the current text", "the older text", "-the current text", "+the older text", "--- main/a.md", "+++ main/a.conflict-20260923T121212.md"} {
		if !strings.Contains(out.Diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, out.Diff)
		}
	}
	if out.Mine.Path != "main/a.md" || out.Theirs.Path != "main/a.conflict-20260923T121212.md" {
		t.Fatalf("diff sides: %+v %+v", out.Mine, out.Theirs)
	}
}

func TestConflictResolveBoth(t *testing.T) {
	e := newConflictEnv(t)
	ctx := context.Background()
	copy, err := e.db.GetNoteByPath(ctx, "main/a.conflict-20260923T121212.md")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
	}
	if code := e.resolve(t, copy.ID, "both", &res); code != 200 {
		t.Fatalf("resolve both %d", code)
	}
	if res.Path != "main/a (older).md" {
		t.Fatalf("renamed to %q", res.Path)
	}
	if _, err := e.db.GetNoteByPath(ctx, "main/a (older).md"); err != nil {
		t.Fatalf("renamed note not indexed: %v", err)
	}
	if _, err := e.db.GetNoteByPath(ctx, "main/a.conflict-20260923T121212.md"); err == nil {
		t.Fatal("conflict path still indexed")
	}
	// Two ordinary notes, nothing nested under the survivor.
	var tree struct {
		Spaces []SpaceTree `json:"spaces"`
	}
	e.get(t, "/api/tree", &tree)
	for _, sp := range tree.Spaces {
		if sp.Name != "main" {
			continue
		}
		for _, c := range sp.Children {
			if strings.Contains(c.Path, ".conflict-") {
				t.Fatalf("conflict name left in the tree: %+v", c)
			}
			if c.Path == "main/a.md" && len(c.Children) != 0 {
				t.Fatalf("survivor still holds conflicts: %+v", c.Children)
			}
		}
	}
}

func TestConflictResolveMine(t *testing.T) {
	e := newConflictEnv(t)
	copy, err := e.db.GetNoteByPath(context.Background(), "main/a.conflict-20260923T121212.md")
	if err != nil {
		t.Fatal(err)
	}
	if code := e.resolve(t, copy.ID, "mine", nil); code != 200 {
		t.Fatalf("resolve mine %d", code)
	}
	// The copy is in the trash, never deleted, and the survivor keeps
	// its own text.
	if _, err := os.Stat(filepath.Join(e.dir, ".trash", "main", "a.conflict-20260923T121212.md")); err != nil {
		t.Fatalf("trash copy: %v", err)
	}
	if _, err := e.db.GetNoteByPath(context.Background(), "main/a.conflict-20260923T121212.md"); err == nil {
		t.Fatal("conflict still indexed")
	}
	if b, err := os.ReadFile(filepath.Join(e.dir, "main", "a.md")); err != nil || !strings.Contains(string(b), "the current text") {
		t.Fatalf("survivor changed: %v", err)
	}
	var trash struct {
		Entries []struct {
			Path string `json:"path"`
		} `json:"entries"`
	}
	if code := e.get(t, "/api/trash", &trash); code != 200 || len(trash.Entries) != 1 {
		t.Fatalf("trash %d %+v", code, trash)
	}
}

func TestConflictResolveTheirs(t *testing.T) {
	e := newConflictEnv(t)
	ctx := context.Background()
	a, err := e.db.GetNoteByPath(ctx, "main/a.md")
	if err != nil {
		t.Fatal(err)
	}
	copy, err := e.db.GetNoteByPath(ctx, "main/a.conflict-20260923T121212.md")
	if err != nil {
		t.Fatal(err)
	}
	if code := e.resolve(t, copy.ID, "theirs", nil); code != 200 {
		t.Fatalf("resolve theirs %d", code)
	}
	// The conflict's body becomes the survivor's, as an edit that
	// reaches the file.
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, rerr := os.ReadFile(filepath.Join(e.dir, "main", "a.md"))
		if rerr == nil && strings.Contains(string(b), "the older text") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("survivor never took the copy's text: %v", rerr)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := e.db.GetNoteByPath(ctx, "main/a.conflict-20260923T121212.md"); err == nil {
		t.Fatal("conflict still indexed")
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".trash", "main", "a.conflict-20260923T121212.md")); err != nil {
		t.Fatalf("trash copy: %v", err)
	}
	kept, err := e.db.GetNote(ctx, a.ID)
	if err != nil {
		t.Fatalf("survivor row gone: %v", err)
	}
	if kept.ConflictOf != "" {
		t.Fatalf("survivor marked as conflict")
	}
}

func TestConflictSurvivorGone(t *testing.T) {
	e := newConflictEnv(t)
	ctx := context.Background()
	// Remove the survivor by hand and rescan: the copy becomes a plain
	// note with no pointer.
	if err := os.Remove(filepath.Join(e.dir, "main", "a.md")); err != nil {
		t.Fatal(err)
	}
	sc := scanner.New(mustRoot(t, e.dir), e.db, scanner.Options{SettleTime: time.Millisecond}, nil)
	if _, err := sc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	copy, err := e.db.GetNoteByPath(ctx, "main/a.conflict-20260923T121212.md")
	if err != nil {
		t.Fatal(err)
	}
	if copy.ConflictOf != "" {
		t.Fatalf("conflict_of survived the survivor: %q", copy.ConflictOf)
	}
	var list struct {
		Conflicts []index.ConflictRow `json:"conflicts"`
	}
	if code := e.get(t, "/api/conflicts", &list); code != 200 {
		t.Fatalf("list %d", code)
	}
	found := false
	for _, c := range list.Conflicts {
		if c.Note.RelPath != "main/a.conflict-20260923T121212.md" {
			continue
		}
		found = true
		if c.Of != nil {
			t.Fatalf("orphan still points at a survivor: %+v", c.Of)
		}
	}
	if !found {
		t.Fatal("orphan dropped from the listing; it is still a conflict by name")
	}
	// The diff endpoint says so instead of guessing.
	var out map[string]any
	if code := e.get(t, "/api/conflicts/"+copy.ID+"/diff", &out); code != 409 {
		t.Fatalf("diff after the survivor is gone: %d", code)
	}
}

func TestConflictRejectsBadAction(t *testing.T) {
	e := newConflictEnv(t)
	copy, err := e.db.GetNoteByPath(context.Background(), "main/a.conflict-20260923T121212.md")
	if err != nil {
		t.Fatal(err)
	}
	if code := e.resolve(t, copy.ID, "explode", nil); code != 400 {
		t.Fatalf("bad action %d", code)
	}
	plain, err := e.db.GetNoteByPath(context.Background(), "main/plain.md")
	if err != nil {
		t.Fatal(err)
	}
	if code := e.resolve(t, plain.ID, "mine", nil); code != 409 {
		t.Fatalf("non-conflict resolve %d", code)
	}
}

func mustRoot(t *testing.T, dir string) *pathsafe.Root {
	t.Helper()
	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
