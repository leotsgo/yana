package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
)

func (e *linksEnv) del(t *testing.T, path string, out any) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, e.ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && resp.StatusCode < 500 {
			t.Fatalf("DELETE %s: decode: %v", path, err)
		}
	}
	return resp.StatusCode
}

func TestDirCreateShowsInTree(t *testing.T) {
	e := newLinksEnv(t, true, nil)
	if code := e.post(t, "/api/dirs", map[string]string{"path": "main/projects"}, nil); code != 201 {
		t.Fatalf("mkdir = %d", code)
	}
	if info, err := os.Stat(filepath.Join(e.dir, "main", "projects")); err != nil || !info.IsDir() {
		t.Fatalf("folder not on disk: %v", err)
	}
	var tree struct{ Spaces []SpaceTree }
	if code := e.get(t, "/api/tree", &tree); code != 200 {
		t.Fatalf("tree = %d", code)
	}
	found := false
	for _, sp := range tree.Spaces {
		if sp.Name != "main" {
			continue
		}
		for _, c := range sp.Children {
			if c.Type == "dir" && c.Path == "main/projects" && len(c.Children) == 0 {
				found = true
			}
		}
	}
	if !found {
		b, _ := json.Marshal(tree)
		t.Fatalf("empty folder missing from the tree: %s", b)
	}
	// Refusals: again, a bare space, an escape, under _assets.
	if code := e.post(t, "/api/dirs", map[string]string{"path": "main/projects"}, nil); code != 409 {
		t.Fatalf("again = %d", code)
	}
	if code := e.post(t, "/api/dirs", map[string]string{"path": "newspace"}, nil); code != 400 {
		t.Fatalf("bare space = %d", code)
	}
	if code := e.post(t, "/api/dirs", map[string]string{"path": "main/../../x"}, nil); code != 400 {
		t.Fatalf("escape = %d", code)
	}
	if code := e.post(t, "/api/dirs", map[string]string{"path": "main/_assets/x"}, nil); code != 400 {
		t.Fatalf("assets = %d", code)
	}
}

// Renaming a folder of 50 notes with 200 inbound links leaves zero
// unresolved links: every link is rewritten through the reconciler.
func TestDirMoveRewritesEveryLink(t *testing.T) {
	e := newLinksEnv(t, true, nil)
	write := func(rel, content string) {
		p := filepath.Join(e.dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
		old := time.Now().Add(-time.Minute)
		os.Chtimes(p, old, old)
	}
	// 50 notes in main/team, half in a subfolder; 4 linkers each, in
	// every link style, from inside and outside the folder.
	for i := 0; i < 50; i++ {
		rel := fmt.Sprintf("main/team/n%02d.md", i)
		if i%2 == 1 {
			rel = fmt.Sprintf("main/team/sub/n%02d.md", i)
		}
		write(rel, fmt.Sprintf("# Note %02d\n\nbody\n", i))
	}
	var b strings.Builder
	for i := 0; i < 50; i++ {
		if i%2 == 1 {
			fmt.Fprintf(&b, "- [[team/sub/n%02d]] rooted\n", i)
			fmt.Fprintf(&b, "- [[n%02d|by name]]\n", i)
		} else {
			fmt.Fprintf(&b, "- [[team/n%02d.md]] with extension\n", i)
			fmt.Fprintf(&b, "- [[n%02d]] bare\n", i)
		}
	}
	write("main/index.md", "# Index\n\n"+b.String())
	b.Reset()
	for i := 0; i < 50; i++ {
		if i%2 == 1 {
			fmt.Fprintf(&b, "- [[sub/n%02d]] relative\n", i)
		} else {
			fmt.Fprintf(&b, "- [[n%02d]] sibling\n", i)
		}
	}
	write("main/team/readme.md", "# Readme\n\n"+b.String())
	b.Reset()
	for i := 0; i < 50; i++ {
		if i%2 == 1 {
			fmt.Fprintf(&b, "- [[n%02d]] sibling in sub\n", i)
		} else {
			fmt.Fprintf(&b, "- [[../n%02d]] up one\n", i)
		}
	}
	write("main/team/sub/notes.md", "# Sub notes\n\n"+b.String())
	if _, err := e.sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	var un struct{ Unresolved []index.UnresolvedLink }
	if code := e.get(t, "/api/links/unresolved?space=main", &un); code != 200 {
		t.Fatalf("unresolved = %d", code)
	}
	before := len(un.Unresolved) // the fixture's own [[missing]]

	var res struct {
		Path      string `json:"path"`
		Moved     int    `json:"moved"`
		Total     int    `json:"total"`
		Rewritten int    `json:"rewritten"`
		Broken    int    `json:"broken"`
	}
	if code := e.post(t, "/api/dirs/move", map[string]string{"path": "main/team", "to": "main/crew"}, &res); code != 200 {
		t.Fatalf("move = %d %+v", code, res)
	}
	if res.Moved != 52 || res.Total != 52 || res.Broken != 0 {
		t.Fatalf("move result = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "main", "team")); !os.IsNotExist(err) {
		t.Fatalf("old folder still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "main", "crew", "sub", "n01.md")); err != nil {
		t.Fatalf("note not moved: %v", err)
	}
	// Once the rewrites reach the files, a fresh scan resolves everything.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := e.sc.Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if code := e.get(t, "/api/links/unresolved?space=main", &un); code != 200 {
			t.Fatalf("unresolved = %d", code)
		}
		if len(un.Unresolved) == before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d links unresolved after the folder move: %+v", len(un.Unresolved)-before, un.Unresolved[:3])
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Errors: into itself, same path, missing.
	if code := e.post(t, "/api/dirs/move", map[string]string{"path": "main/crew", "to": "main/crew/inner"}, nil); code != 400 {
		t.Fatalf("into itself = %d", code)
	}
	if code := e.post(t, "/api/dirs/move", map[string]string{"path": "main/crew", "to": "main/crew"}, nil); code != 400 {
		t.Fatalf("same = %d", code)
	}
	if code := e.post(t, "/api/dirs/move", map[string]string{"path": "main/nowhere", "to": "main/x"}, nil); code != 404 {
		t.Fatalf("missing = %d", code)
	}
}

func TestDirMoveCarriesAssets(t *testing.T) {
	e := newLinksEnv(t, true, nil)
	p := filepath.Join(e.dir, "main", "docs", "_assets", "pic.png")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("PNG"), 0o644)
	if code := e.post(t, "/api/dirs/move", map[string]string{"path": "main/docs", "to": "main/manual"}, nil); code != 200 {
		t.Fatalf("move = %d", code)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "main", "manual", "_assets", "pic.png")); err != nil {
		t.Fatalf("asset did not follow: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "main", "docs")); !os.IsNotExist(err) {
		t.Fatalf("old folder still there: %v", err)
	}
}

func TestDirDeleteTrashesNotes(t *testing.T) {
	e := newLinksEnv(t, true, nil)
	if code := e.del(t, "/api/dirs?path=main/docs", nil); code != 200 {
		t.Fatalf("delete = %d", code)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "main", "docs")); !os.IsNotExist(err) {
		t.Fatalf("folder still there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".trash", "main", "docs", "guide.md")); err != nil {
		t.Fatalf("note not in the trash: %v", err)
	}
	if _, err := e.db.GetNoteByPath(context.Background(), "main/docs/guide.md"); err == nil {
		t.Fatal("note still indexed")
	}
	if code := e.del(t, "/api/dirs?path=main/docs", nil); code != 404 {
		t.Fatalf("again = %d", code)
	}
}

func TestDirEndpointsDenied(t *testing.T) {
	e := newLinksEnv(t, true, func(r *http.Request, space string) error {
		if space == "main" {
			return context.Canceled
		}
		return nil
	})
	if code := e.post(t, "/api/dirs", map[string]string{"path": "main/x"}, nil); code != 403 {
		t.Fatalf("mkdir = %d", code)
	}
	if code := e.post(t, "/api/dirs/move", map[string]string{"path": "main/docs", "to": "main/x"}, nil); code != 403 {
		t.Fatalf("move = %d", code)
	}
	if code := e.del(t, "/api/dirs?path=main/docs", nil); code != 403 {
		t.Fatalf("delete = %d", code)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "main", "docs", "guide.md")); err != nil {
		t.Fatalf("note touched despite denial: %v", err)
	}
}

func TestTagsEndpoints(t *testing.T) {
	e := newEnv(t)
	var tags struct{ Tags []index.TagCount }
	if code := e.get(t, "/api/tags", &tags); code != 200 {
		t.Fatalf("tags = %d", code)
	}
	if len(tags.Tags) != 1 || tags.Tags[0].Tag != "greeting" || tags.Tags[0].Count != 1 {
		t.Fatalf("tags = %+v", tags.Tags)
	}
	var notes struct {
		Tag   string
		Notes []index.Note
	}
	if code := e.get(t, "/api/tags/Greeting", &notes); code != 200 {
		t.Fatalf("tag = %d", code)
	}
	if notes.Tag != "greeting" || len(notes.Notes) != 1 || notes.Notes[0].RelPath != "home/hello.md" {
		t.Fatalf("tag notes = %+v", notes)
	}
	if code := e.get(t, "/api/tags/nothing", &notes); code != 200 || len(notes.Notes) != 0 {
		t.Fatalf("unknown tag = %d %+v", code, notes)
	}
	// The tree carries each note's tags for the switcher.
	var tree struct{ Spaces []SpaceTree }
	e.get(t, "/api/tree", &tree)
	for _, sp := range tree.Spaces {
		for _, c := range sp.Children {
			if c.Path == "home/hello.md" && (len(c.Tags) != 1 || c.Tags[0] != "greeting") {
				t.Fatalf("tree tags = %+v", c.Tags)
			}
		}
	}
	// A rendered note carries clickable tags.
	var note struct{ HTML string }
	hello, _ := e.db.GetNoteByPath(context.Background(), "home/hello.md")
	e.get(t, "/api/notes/"+hello.ID, &note)
	if !strings.Contains(note.HTML, `<span class="tag" data-tag="greeting">#greeting</span>`) {
		t.Fatalf("html = %s", note.HTML)
	}
}
