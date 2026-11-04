package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/madeofpendletonwool/yana/internal/drawing"
	"github.com/madeofpendletonwool/yana/internal/frontmatter"
)

const textScene = `{"type":"excalidraw","version":2,"elements":[{"type":"text","text":"zebrafish","originalText":"zebrafish","isDeleted":false,"link":null}],"appState":{},"files":{}}`

// newDrawing makes main/Map.excalidraw.md through the API and returns its id.
func newDrawing(t *testing.T, e *linksEnv) string {
	t.Helper()
	var res struct{ ID string }
	if code := e.post(t, "/api/notes", map[string]string{"path": "main/Map.excalidraw.md"}, &res); code != 201 {
		t.Fatalf("create drawing: %d", code)
	}
	return res.ID
}

func TestDrawingPayload(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	id := newDrawing(t, e)
	var n NoteResponse
	if code := e.get(t, "/api/notes/"+id, &n); code != 200 {
		t.Fatal(code)
	}
	if n.Kind != "drawing" || n.ContentHash == "" || n.SceneError != "" {
		t.Fatalf("payload: %+v", n)
	}
	var sc struct{ Elements []any }
	if err := json.Unmarshal([]byte(n.Scene), &sc); err != nil || len(sc.Elements) != 0 {
		t.Fatalf("scene %q: %v", n.Scene, err)
	}
	raw, _ := os.ReadFile(filepath.Join(e.dir, "main", "Map.excalidraw.md"))
	if string(frontmatter.Parse(raw).Body) != string(drawing.Encode([]byte(drawing.EmptyScene))) {
		t.Fatalf("created body: %q", raw)
	}
}

func TestDrawingPayloadUnreadable(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	id := newDrawing(t, e)
	p := filepath.Join(e.dir, "main", "Map.excalidraw.md")
	if err := os.WriteFile(p, []byte("---\nid: "+id+"\n---\n# no\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e.sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n NoteResponse
	if code := e.get(t, "/api/notes/"+id, &n); code != 200 {
		t.Fatal(code)
	}
	if n.Scene != "" || n.SceneError == "" {
		t.Fatalf("payload: scene=%q err=%q", n.Scene, n.SceneError)
	}
}

func TestDrawingSave(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	id := newDrawing(t, e)
	p := filepath.Join(e.dir, "main", "Map.excalidraw.md")
	before, _ := os.ReadFile(p)
	var n NoteResponse
	e.get(t, "/api/notes/"+id, &n)

	code, out := putSource(t, e.env, id, map[string]any{"source": textScene, "base_hash": n.ContentHash})
	if code != 200 || out["ok"] != true || out["conflict_copy"] != nil {
		t.Fatalf("save %d %v", code, out)
	}
	raw, _ := os.ReadFile(p)
	want := string(frontmatter.Parse(before).Head) + string(drawing.Encode([]byte(textScene)))
	if string(raw) != want {
		t.Fatalf("file:\n%q\nwant:\n%q", raw, want)
	}
	var found struct {
		Hits []struct{ Note struct{ ID string } }
	}
	e.get(t, "/api/search?q=zebrafish", &found)
	if len(found.Hits) != 1 || found.Hits[0].Note.ID != id {
		t.Fatalf("search: %+v", found)
	}
}

func TestDrawingSaveRejectsNonScene(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	id := newDrawing(t, e)
	p := filepath.Join(e.dir, "main", "Map.excalidraw.md")
	before, _ := os.ReadFile(p)
	if code, _ := putSource(t, e.env, id, map[string]any{"source": "hello"}); code != 400 {
		t.Fatalf("save %d", code)
	}
	if after, _ := os.ReadFile(p); string(after) != string(before) {
		t.Fatalf("file changed: %q", after)
	}
}

// Two devices save from the same base: the second save merges with the
// first instead of moving it to a conflict copy, and its reply carries the
// merged scene, which the client did not have.
func TestDrawingSaveMerges(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	id := newDrawing(t, e)
	p := filepath.Join(e.dir, "main", "Map.excalidraw.md")
	var n NoteResponse
	e.get(t, "/api/notes/"+id, &n)
	rect := func(id string) string {
		return fmt.Sprintf(`{"id":%q,"type":"rectangle","version":1,"versionNonce":1,"isDeleted":false,"index":"a%s"}`, id, id)
	}
	sceneOf := func(els ...string) string {
		return `{"type":"excalidraw","version":2,"elements":[` + strings.Join(els, ",") + `],"appState":{},"files":{}}`
	}

	code, out := putSource(t, e.env, id, map[string]any{"source": sceneOf(rect("A")), "base_hash": n.ContentHash})
	if code != 200 || out["scene"] != nil {
		t.Fatalf("first save %d %v", code, out)
	}
	code, out = putSource(t, e.env, id, map[string]any{"source": sceneOf(rect("B")), "base_hash": n.ContentHash})
	if code != 200 || out["conflict_copy"] != nil {
		t.Fatalf("second save %d %v", code, out)
	}
	scene, _ := out["scene"].(string)
	if !strings.Contains(scene, `"A"`) || !strings.Contains(scene, `"B"`) {
		t.Fatalf("reply scene: %q", scene)
	}
	raw, _ := os.ReadFile(p)
	if strings.Count(string(raw), `"id": "A"`) != 1 || strings.Count(string(raw), `"id": "B"`) != 1 {
		t.Fatalf("file:\n%s", raw)
	}
	if out["hash"] != hashContent(raw) {
		t.Fatalf("hash %v is not the file's", out["hash"])
	}
	if matches, _ := filepath.Glob(filepath.Join(e.dir, "main", "*conflict*")); len(matches) != 0 {
		t.Fatalf("conflict copies: %v", matches)
	}
	// A body that is not a scene is still refused, merge or not.
	bad := `{"type":"excalidraw","elements":[{"id":"x","version":"one"}]}`
	if code, _ := putSource(t, e.env, id, map[string]any{"source": bad}); code != 400 {
		t.Fatalf("bad elements: %d", code)
	}
	if after, _ := os.ReadFile(p); string(after) != string(raw) {
		t.Fatal("a refused save changed the file")
	}
}

func TestDrawingSaveTooLarge(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	id := newDrawing(t, e)
	p := filepath.Join(e.dir, "main", "Map.excalidraw.md")
	before, _ := os.ReadFile(p)
	big := `{"type":"excalidraw","pad":"` + strings.Repeat("x", int(e.srv.Root.Limits().MaxNoteSize)) + `"}`
	if code, _ := putSource(t, e.env, id, map[string]any{"source": big}); code != 413 {
		t.Fatalf("save %d", code)
	}
	if after, _ := os.ReadFile(p); string(after) != string(before) {
		t.Fatal("file changed")
	}
}

func TestDrawingEmbed(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	id := newDrawing(t, e)
	var res struct{ ID string }
	if code := e.post(t, "/api/notes", map[string]string{"path": "main/n.md", "content": "# N\n\n![[Map]]\n"}, &res); code != 201 {
		t.Fatal(code)
	}
	var n NoteResponse
	e.get(t, "/api/notes/"+res.ID, &n)
	if !strings.Contains(n.HTML, `class="drawing-embed" data-id="`+id+`"`) || !strings.Contains(n.HTML, `data-target="Map"`) {
		t.Fatalf("embed html:\n%s", n.HTML)
	}
}

// A drawing on disk that does not parse is never overwritten unseen: the
// save parks it as a conflict copy even when the base hash matches.
func TestDrawingSaveKeepsUnreadable(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	id := newDrawing(t, e)
	p := filepath.Join(e.dir, "main", "Map.excalidraw.md")
	old := []byte("# no scene here\n")
	if err := os.WriteFile(p, old, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := putSource(t, e.env, id, map[string]any{"source": textScene, "base_hash": hashContent(old)})
	conflict, _ := out["conflict_copy"].(string)
	if code != 200 || conflict == "" {
		t.Fatalf("save %d %v", code, out)
	}
	kept, _ := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(conflict)))
	// The scanner may have given the copy an id of its own; the body is the old file's.
	if string(frontmatter.Parse(kept).Body) != string(old) {
		t.Fatalf("conflict copy: %q", kept)
	}
	raw, _ := os.ReadFile(p)
	if string(frontmatter.Parse(raw).Body) != string(drawing.Encode([]byte(textScene))) {
		t.Fatalf("file: %q", raw)
	}
}

// The cap applies to the file that gets written, frontmatter included.
func TestDrawingSaveFileOverCap(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	id := newDrawing(t, e)
	p := filepath.Join(e.dir, "main", "Map.excalidraw.md")
	before, _ := os.ReadFile(p)
	max := int(e.srv.Root.Limits().MaxNoteSize)
	// Under the cap as a source, over it once the fence and head are added.
	src := `{"type":"excalidraw","pad":"` + strings.Repeat("x", max-60) + `"}`
	if len(src) > max {
		t.Fatal("test source must fit the source cap")
	}
	if code, _ := putSource(t, e.env, id, map[string]any{"source": src}); code != 413 {
		t.Fatalf("save %d", code)
	}
	if after, _ := os.ReadFile(p); string(after) != string(before) {
		t.Fatal("file changed")
	}
}
