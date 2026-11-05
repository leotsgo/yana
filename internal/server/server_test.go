package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/search"
)

type env struct {
	dir string
	srv *Server
	ts  *httptest.Server
	db  *index.DB
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
		old := time.Now().Add(-time.Minute)
		os.Chtimes(p, old, old)
	}
	write("home/hello.md", "# Hello\n\nSee ![pic](_assets/pic.png) and #greeting\n")
	write("home/sub/second.md", "---\norder: 1\n---\n# Second\n\nunique-phrase-xyz\n")
	write("home/_assets/pic.png", "PNGDATA")
	write("work/dash.html", "<h1>Dash</h1><p>stats</p>")
	write("loose.md", "# Loose\n")

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
	web := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>YANA/</title>")},
		"assets/app.js":        {Data: []byte("console.log(1)")},
		"sw.js":                {Data: []byte("self.addEventListener('install', () => {})")},
		"manifest.webmanifest": {Data: []byte(`{"name":"YANA/","start_url":"/"}`)},
		"icon-192.png":         {Data: []byte("PNG")},
		"apple-touch-icon.png": {Data: []byte("PNG")},
	}
	srv := New(Deps{
		DB: db, Root: root, Ripgrep: search.NewRipgrep(dir, true, 2*time.Second), Web: web, Version: "test",
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &env{dir: dir, srv: srv, ts: ts, db: db}
}

func (e *env) get(t *testing.T, path string, out any) int {
	t.Helper()
	resp, err := http.Get(e.ts.URL + path)
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

func TestHealthAndReady(t *testing.T) {
	e := newEnv(t)
	if code := e.get(t, "/healthz", nil); code != 200 {
		t.Fatalf("healthz %d", code)
	}
	if code := e.get(t, "/readyz", nil); code != 503 {
		t.Fatalf("readyz before ready: %d", code)
	}
	e.srv.SetReady(true)
	if code := e.get(t, "/readyz", nil); code != 200 {
		t.Fatalf("readyz after ready: %d", code)
	}
	var st map[string]any
	e.get(t, "/api/status", &st)
	if st["notes"].(float64) != 4 || st["version"] != "test" {
		t.Fatalf("status: %v", st)
	}
}

func TestTreeAndNote(t *testing.T) {
	e := newEnv(t)
	var tree struct{ Spaces []SpaceTree }
	if code := e.get(t, "/api/tree", &tree); code != 200 {
		t.Fatalf("tree %d", code)
	}
	if len(tree.Spaces) != 3 { // "", home, work
		t.Fatalf("spaces: %+v", tree.Spaces)
	}
	var home *SpaceTree
	for i := range tree.Spaces {
		if tree.Spaces[i].Name == "home" {
			home = &tree.Spaces[i]
		}
	}
	if home == nil || home.Notes != 2 || len(home.Children) != 2 {
		t.Fatalf("home: %+v", home)
	}
	if home.Children[0].Type != "dir" || home.Children[0].Name != "sub" || home.Children[1].Name != "hello.md" {
		t.Fatalf("ordering: %+v", home.Children)
	}
	id := home.Children[1].ID

	var note NoteResponse
	if code := e.get(t, "/api/notes/"+id, &note); code != 200 {
		t.Fatalf("note %d", code)
	}
	if note.Title != "Hello" || note.Base != "home" || !strings.Contains(note.HTML, "<h1") || !strings.HasPrefix(note.Markdown, "---\nid: ") {
		t.Fatalf("note: %+v", note)
	}
	if len(note.Tags) != 1 || note.Tags[0] != "greeting" {
		t.Fatalf("tags: %v", note.Tags)
	}

	var scoped struct{ Spaces []SpaceTree }
	e.get(t, "/api/tree?space=work", &scoped)
	if len(scoped.Spaces) != 1 || scoped.Spaces[0].Children[0].Kind != "html" {
		t.Fatalf("scoped: %+v", scoped.Spaces)
	}
	var htmlNote NoteResponse
	e.get(t, "/api/notes/"+scoped.Spaces[0].Children[0].ID, &htmlNote)
	if htmlNote.HTML != "" || !strings.Contains(htmlNote.Source, "<h1>Dash</h1>") {
		t.Fatalf("html note must ship source, not rendered html: %+v", htmlNote)
	}

	var errResp map[string]string
	if code := e.get(t, "/api/notes/nope", &errResp); code != 400 {
		t.Fatalf("bad id %d", code)
	}
	if code := e.get(t, "/api/notes/01ARZ3NDEKTSV4RRFFQ69G5FAV", &errResp); code != 404 {
		t.Fatalf("missing id %d", code)
	}
	if code := e.get(t, "/api/tree?space=../etc", &errResp); code != 400 {
		t.Fatalf("bad space %d", code)
	}
	if code := e.get(t, "/api/nothing", &errResp); code != 404 {
		t.Fatalf("unknown api %d", code)
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t)
	var res struct {
		Mode string
		Hits []index.SearchHit
	}
	if code := e.get(t, "/api/search?q=unique-phrase", &res); code != 200 {
		t.Fatalf("search %d", code)
	}
	if res.Mode != "fts" || len(res.Hits) != 1 || res.Hits[0].Note.Title != "Second" {
		t.Fatalf("fts: %+v", res)
	}
	e.get(t, "/api/search?q=unique-phrase&space=work", &res)
	if len(res.Hits) != 0 {
		t.Fatalf("space filter: %+v", res)
	}
	var errResp map[string]string
	if code := e.get(t, "/api/search", &errResp); code != 400 {
		t.Fatalf("empty query %d", code)
	}
	if code := e.get(t, "/api/search?q=x&limit=999", &errResp); code != 400 {
		t.Fatalf("bad limit %d", code)
	}

	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("rg not installed; regex passthrough untested")
	}
	var rx struct {
		Mode string
		Hits []struct {
			Path, Text, ID, Title string
			Line                  int
		}
	}
	if code := e.get(t, "/api/search?raw=unique-\\w%2B-xyz", &rx); code != 200 {
		t.Fatalf("regex %d", code)
	}
	if rx.Mode != "regex" || len(rx.Hits) != 1 || rx.Hits[0].Path != "home/sub/second.md" || rx.Hits[0].Title != "Second" || rx.Hits[0].ID == "" {
		t.Fatalf("regex hits: %+v", rx)
	}
	if code := e.get(t, "/api/search?raw=(bad", &errResp); code != 400 {
		t.Fatalf("bad regex %d: %v", code, errResp)
	}
}

func TestFilesAndWeb(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.ts.URL + "/api/files/home/_assets/pic.png")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("asset %d", resp.StatusCode)
	}
	resp.Body.Close()
	for _, bad := range []string{"/api/files/home/hello.md", "/api/files/../etc/passwd", "/api/files/home/_assets/missing.png", "/api/files/.sync/index.db"} {
		resp, _ := http.Get(e.ts.URL + bad)
		if resp.StatusCode == 200 {
			t.Errorf("%s served (%d)", bad, resp.StatusCode)
		}
		resp.Body.Close()
	}
	for _, p := range []string{"/", "/note/01ARZ3NDEKTSV4RRFFQ69G5FAV", "/index.html"} {
		resp, _ := http.Get(e.ts.URL + p)
		if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
			t.Errorf("%s: %d %s", p, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if resp.Header.Get("X-Request-Id") == "" {
			t.Errorf("%s: missing request id", p)
		}
		resp.Body.Close()
	}
	resp, _ = http.Get(e.ts.URL + "/assets/app.js")
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("static asset: %d %s", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	resp.Body.Close()
}

func TestPwaFiles(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.ts.URL + "/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Errorf("sw.js: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("sw.js cache-control: %q", cc)
	}
	resp.Body.Close()

	resp, err = http.Get(e.ts.URL + "/manifest.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "application/manifest+json") {
		t.Errorf("manifest: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("manifest cache-control: %q", cc)
	}
	resp.Body.Close()

	resp, err = http.Get(e.ts.URL + "/icon-192.png")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "image/png") {
		t.Errorf("icon: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp.Body.Close()

	// The share target is a client route; it gets the shell.
	resp, err = http.Get(e.ts.URL + "/share?title=x&url=https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Errorf("/share: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp.Body.Close()
}
