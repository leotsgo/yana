package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func putSource(t *testing.T, e *env, id string, body map[string]any) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("PUT", e.ts.URL+"/api/notes/"+id+"/source", strings.NewReader(mustJSON(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestNoteSourceSave(t *testing.T) {
	ce := newContentEnv(t)
	id := "01ARZHOSTILE0000000000000A"

	var n NoteResponse
	if code := ce.get(t, "/api/notes/"+id, &n); code != 200 {
		t.Fatal(code)
	}
	baseHash := n.ContentHash

	// Save a new body built on what we read.
	code, out := putSource(t, ce.env, id, map[string]any{
		"source":    "<h1>Hostile v2</h1><p>edited</p>\n",
		"base_hash": baseHash,
	})
	if code != 200 || out["ok"] != true {
		t.Fatalf("save %d %v", code, out)
	}
	if out["conflict_copy"] != nil {
		t.Fatalf("unexpected conflict: %v", out)
	}
	raw, err := os.ReadFile(filepath.Join(ce.dir, "work", "hostile.html"))
	if err != nil {
		t.Fatal(err)
	}
	// The frontmatter rides along untouched above the new body.
	if !strings.HasPrefix(string(raw), "---\nid: "+id+"\n") || !strings.Contains(string(raw), "<h1>Hostile v2</h1>") {
		t.Fatalf("saved file: %q", raw)
	}
	// The index saw it.
	if code := ce.get(t, "/api/notes/"+id, &n); code != 200 || !strings.Contains(n.Preview, "edited") {
		t.Fatalf("note after save: %d %+v", code, n.Note)
	}

	// A markdown note refuses source saves.
	var tree struct {
		Spaces []SpaceTree
	}
	ce.get(t, "/api/tree?space=home", &tree)
	mdID := ""
	for _, sp := range tree.Spaces {
		for _, c := range sp.Children {
			if c.Kind == "md" {
				mdID = c.ID
			}
		}
	}
	if mdID == "" {
		t.Fatal("no md note found")
	}
	if code, _ := putSource(t, ce.env, mdID, map[string]any{"source": "x"}); code != 400 {
		t.Fatalf("md save %d", code)
	}
}

// Last write wins, but a save that does not build on the current file
// parks the overwritten version as name.conflict-<ts>.html first.
func TestNoteSourceConflictCopy(t *testing.T) {
	ce := newContentEnv(t)
	id := "01ARZHOSTILE0000000000000A"

	var n NoteResponse
	ce.get(t, "/api/notes/"+id, &n)
	stale := n.ContentHash

	// Someone else rewrites the file out from under the editor.
	p := filepath.Join(ce.dir, "work", "hostile.html")
	other := "---\nid: " + id + "\n---\n<h1>Rewritten elsewhere</h1>\n"
	if err := os.WriteFile(p, []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := putSource(t, ce.env, id, map[string]any{
		"source":    "<h1>Local edit</h1>\n",
		"base_hash": stale,
	})
	if code != 200 {
		t.Fatalf("conflicting save %d %v", code, out)
	}
	conflict, _ := out["conflict_copy"].(string)
	if conflict == "" || !strings.Contains(conflict, "hostile.conflict-") || !strings.HasSuffix(conflict, ".html") {
		t.Fatalf("conflict copy path: %v", out)
	}

	// The diverged version is kept beside the note, minus the id line
	// (a copy carrying the same id would steal the note's identity on
	// the next scan); it may already have grown one of its own.
	kept, err := os.ReadFile(filepath.Join(ce.dir, filepath.FromSlash(conflict)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kept), "<h1>Rewritten elsewhere</h1>") || strings.Contains(string(kept), id) {
		t.Fatalf("conflict copy content: %q", kept)
	}
	// Last write wins: the note itself holds the local edit (and its
	// frontmatter).
	raw, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(raw), "---\nid: "+id+"\n---\n<h1>Local edit</h1>\n") {
		t.Fatalf("note after conflicting save: %q", raw)
	}

	// The conflict copy is a note: fresh files wait out the settle
	// window before they take an id, so give it that and rescan.
	time.Sleep(10 * time.Millisecond)
	if _, err := ce.srv.Deps.Scanner.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, sp := range globNotes(t, ce, "work") {
		if sp.RelPath == conflict {
			found = sp.ID
		}
	}
	if found == "" || found == id {
		t.Fatalf("conflict copy not indexed under its own id: %q", found)
	}

	// Saving again with the fresh hash is clean.
	ce.get(t, "/api/notes/"+id, &n)
	code, out = putSource(t, ce.env, id, map[string]any{
		"source":    "<h1>Local edit</h1><p>more</p>\n",
		"base_hash": n.ContentHash,
	})
	if code != 200 || out["conflict_copy"] != nil {
		t.Fatalf("clean save %d %v", code, out)
	}
}

func globNotes(t *testing.T, ce *contentEnv, space string) []NoteResponse {
	t.Helper()
	var tree struct {
		Spaces []SpaceTree
	}
	if code := ce.get(t, "/api/tree?space="+space, &tree); code != 200 {
		t.Fatal(code)
	}
	var out []NoteResponse
	var walk func(nodes []*TreeNode)
	walk = func(nodes []*TreeNode) {
		for _, c := range nodes {
			if c.Type == "note" && c.ID != "" {
				var n NoteResponse
				if code := ce.get(t, "/api/notes/"+c.ID, &n); code == 200 {
					out = append(out, n)
				}
			}
			if c.Children != nil {
				walk(c.Children)
			}
		}
	}
	for _, sp := range tree.Spaces {
		walk(sp.Children)
	}
	return out
}

// The trust endpoint flips frontmatter and the index follows.
func TestTrustEndpoint(t *testing.T) {
	ce := newContentEnv(t)
	id := "01ARZCANVAS00000000000000X"

	if code := ce.post(t, "/api/notes/"+id+"/trust", map[string]any{"trusted": false}, nil); code != 200 {
		t.Fatalf("untrust %d", code)
	}
	raw, _ := os.ReadFile(filepath.Join(ce.dir, "work", "canvas.html"))
	if !strings.Contains(string(raw), "trusted: false") {
		t.Fatalf("frontmatter: %q", raw)
	}
	var n NoteResponse
	if code := ce.get(t, "/api/notes/"+id, &n); code != 200 || n.Trusted {
		t.Fatalf("index after untrust: %d %+v", code, n.Note)
	}

	// Trusting a markdown note is refused.
	var tree struct {
		Spaces []SpaceTree
	}
	ce.get(t, "/api/tree?space=home", &tree)
	mdID := ""
	for _, sp := range tree.Spaces {
		for _, c := range sp.Children {
			if c.Kind == "md" {
				mdID = c.ID
			}
		}
	}
	if code := ce.post(t, "/api/notes/"+mdID+"/trust", map[string]any{"trusted": true}, nil); code != 400 {
		t.Fatalf("md trust %d", code)
	}
}

// The view endpoint speaks JSON with a url field, and a hostile note's
// source still ships through the note endpoint for the editor.
func TestNoteSourceShips(t *testing.T) {
	ce := newContentEnv(t)
	var n NoteResponse
	if code := ce.get(t, "/api/notes/01ARZHOSTILE0000000000000A", &n); code != 200 {
		t.Fatal(code)
	}
	if n.Source == "" || !strings.Contains(n.Source, "<script>") {
		t.Fatalf("source missing: %+v", n.Note)
	}
	if n.HTML != "" {
		t.Fatalf("html notes must not carry rendered html: %+v", n.Note)
	}
	resp, err := http.Get(ce.ts.URL + "/api/notes/01ARZHOSTILE0000000000000A")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `"trusted":false`) {
		t.Fatalf("trusted flag missing from payload: %s", b)
	}
}
