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

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// linksEnv is an env whose tree carries wikilinks and, optionally, a live
// reconciliation loop for move tests.
type linksEnv struct {
	*env
	rec *reconcile.Reconciler
	sc  *scanner.Scanner
}

func newLinksEnv(t *testing.T, withSync bool, canWrite func(r *http.Request, space string) error) *linksEnv {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
		old := time.Now().Add(-time.Minute)
		os.Chtimes(p, old, old)
	}
	write("main/hello.md", "# Hello\n\nSee [[second]] and [[docs/guide|the guide]].\nAn open one: [[missing]].\n")
	write("main/second.md", "# Second\n\nBack to [[hello]].\n")
	write("main/docs/guide.md", "# Guide\n\nAlso [[hello.md|the hello]].\n")
	write("other/note.md", "# Other\n")

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
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>YANA/</title>")}}
	deps := Deps{DB: db, Root: root, Web: web, Version: "test", Scanner: sc, CanWrite: canWrite}
	var rec *reconcile.Reconciler
	if withSync {
		rec = reconcile.New(root, db, sc, reconcile.Options{
			IdleTime: 60 * time.Millisecond, Debounce: 20 * time.Millisecond,
			SettleTime: 150 * time.Millisecond, UnloadAfter: -1,
		}, nil)
		if err := rec.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rec.Close() })
		deps.Sync = rec
	}
	srv := New(deps)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &linksEnv{env: &env{dir: dir, srv: srv, ts: ts, db: db}, rec: rec, sc: sc}
}

func (e *linksEnv) post(t *testing.T, path string, body any, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		enc := json.NewEncoder(&buf)
		if err := enc.Encode(body); err != nil {
			t.Fatal(err)
		}
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

func TestNotePayloadCarriesLinks(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	second, err := e.db.GetNoteByPath(context.Background(), "main/second.md")
	if err != nil {
		t.Fatal(err)
	}
	var note NoteResponse
	if code := e.get(t, "/api/notes/"+second.ID, &note); code != 200 {
		t.Fatalf("note %d", code)
	}
	byRaw := map[string]index.OutboundLink{}
	for _, l := range note.Links {
		byRaw[l.RawTarget] = l
	}
	if l := byRaw["hello"]; !l.Resolved || l.ToID == "" {
		t.Fatalf("hello link = %+v", l)
	}
	if len(note.Links) != 1 {
		t.Fatalf("links = %+v", note.Links)
	}
}

func TestBacklinksEndpoint(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	hello, err := e.db.GetNoteByPath(context.Background(), "main/hello.md")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Backlinks []index.Backlink `json:"backlinks"`
	}
	if code := e.get(t, "/api/notes/"+hello.ID+"/backlinks", &res); code != 200 {
		t.Fatalf("backlinks %d", code)
	}
	if len(res.Backlinks) != 2 {
		t.Fatalf("backlinks = %+v", res.Backlinks)
	}
	for _, b := range res.Backlinks {
		if !strings.Contains(b.Context, "[[hello") {
			t.Fatalf("context = %q", b.Context)
		}
	}
	if code := e.get(t, "/api/notes/tooshort/backlinks", nil); code != 400 {
		t.Fatalf("bad id = %d", code)
	}
}

func TestUnresolvedEndpoint(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	var all struct {
		Unresolved []index.UnresolvedLink `json:"unresolved"`
	}
	if code := e.get(t, "/api/links/unresolved", &all); code != 200 {
		t.Fatalf("unresolved %d", code)
	}
	if len(all.Unresolved) != 1 || all.Unresolved[0].RawTarget != "missing" {
		t.Fatalf("unresolved = %+v", all.Unresolved)
	}
	var one struct {
		Unresolved []index.UnresolvedLink `json:"unresolved"`
	}
	if code := e.get(t, "/api/links/unresolved?space=other", &one); code != 200 || len(one.Unresolved) != 0 {
		t.Fatalf("other space = %d %+v", code, one.Unresolved)
	}
	if code := e.get(t, "/api/links/unresolved?space=main/sub", nil); code != 400 {
		t.Fatalf("bad space = %d", code)
	}
}

func TestCreateNote(t *testing.T) {
	e := newLinksEnv(t, false, nil)
	var res struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if code := e.post(t, "/api/notes", map[string]string{"path": "main/missing.md"}, &res); code != 201 {
		t.Fatalf("create = %d", code)
	}
	raw, err := os.ReadFile(filepath.Join(e.dir, "main", "missing.md"))
	if err != nil || !strings.Contains(string(raw), "id: "+res.ID) {
		t.Fatalf("created file = %q, %v", raw, err)
	}
	var unresolved struct {
		Unresolved []index.UnresolvedLink `json:"unresolved"`
	}
	e.get(t, "/api/links/unresolved?space=main", &unresolved)
	if len(unresolved.Unresolved) != 0 {
		t.Fatalf("creating the note should resolve [[missing]]: %+v", unresolved.Unresolved)
	}

	if code := e.post(t, "/api/notes", map[string]string{"path": "main/missing.md"}, nil); code != 409 {
		t.Fatalf("conflict = %d", code)
	}
	if code := e.post(t, "/api/notes", map[string]string{"path": "../escape.md"}, nil); code != 400 {
		t.Fatalf("escape = %d", code)
	}
	if code := e.post(t, "/api/notes", map[string]string{"path": "main/pic.png"}, nil); code != 400 {
		t.Fatalf("non-note = %d", code)
	}
}

func TestCreateNoteDenied(t *testing.T) {
	e := newLinksEnv(t, false, func(r *http.Request, space string) error {
		if space == "main" {
			return context.Canceled
		}
		return nil
	})
	if code := e.post(t, "/api/notes", map[string]string{"path": "main/x.md"}, nil); code != 403 {
		t.Fatalf("denied create = %d", code)
	}
	if code := e.post(t, "/api/notes", map[string]string{"path": "other/y.md"}, nil); code != 201 {
		t.Fatalf("allowed create = %d", code)
	}
}

func TestMoveEndpoint(t *testing.T) {
	e := newLinksEnv(t, true, nil)
	hello, err := e.db.GetNoteByPath(context.Background(), "main/hello.md")
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Note      index.Note `json:"note"`
		Rewritten int        `json:"rewritten"`
	}
	if code := e.post(t, "/api/notes/"+hello.ID+"/move", map[string]string{"path": "main/renamed/hello.md"}, &res); code != 200 {
		t.Fatalf("move = %d", code)
	}
	if res.Rewritten != 2 || res.Note.RelPath != "main/renamed/hello.md" {
		t.Fatalf("move result = %+v", res)
	}
	// The inbound link files are rewritten through write-back.
	deadline := time.Now().Add(5 * time.Second)
	ok := false
	for time.Now().Before(deadline) {
		second := fileBodyOrNull(t, e.dir, "main/second.md")
		guide := fileBodyOrNull(t, e.dir, "main/docs/guide.md")
		if strings.Contains(second, "[[renamed/hello") && strings.Contains(guide, "[[renamed/hello") {
			ok = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("inbound links not rewritten: %q %q",
			fileBodyOrNull(t, e.dir, "main/second.md"), fileBodyOrNull(t, e.dir, "main/docs/guide.md"))
	}
}

func fileBodyOrNull(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	// Return the body after the frontmatter block.
	if i := bytes.Index(b, []byte("\n---\n")); i >= 0 && bytes.HasPrefix(b, []byte("---")) {
		return string(b[i+len("\n---\n"):])
	}
	return string(b)
}

func TestMoveEndpointErrors(t *testing.T) {
	e := newLinksEnv(t, true, nil)
	hello, err := e.db.GetNoteByPath(context.Background(), "main/hello.md")
	if err != nil {
		t.Fatal(err)
	}
	if code := e.post(t, "/api/notes/"+hello.ID+"/move", map[string]string{"path": "main/hello.md"}, nil); code != 400 {
		t.Fatalf("same path = %d", code)
	}
	if code := e.post(t, "/api/notes/"+hello.ID+"/move", map[string]string{"path": "main/second.md"}, nil); code != 409 {
		t.Fatalf("taken = %d", code)
	}
	if code := e.post(t, "/api/notes/"+hello.ID+"/move", map[string]string{"path": "../../etc/x.md"}, nil); code != 400 {
		t.Fatalf("escape = %d", code)
	}
	if code := e.post(t, "/api/notes/"+hello.ID+"/move", nil, nil); code != 400 {
		t.Fatalf("empty body = %d", code)
	}
}

func TestMoveEndpointDenied(t *testing.T) {
	e := newLinksEnv(t, true, func(r *http.Request, space string) error {
		if space == "main" {
			return context.Canceled
		}
		return nil
	})
	hello, err := e.db.GetNoteByPath(context.Background(), "main/hello.md")
	if err != nil {
		t.Fatal(err)
	}
	if code := e.post(t, "/api/notes/"+hello.ID+"/move", map[string]string{"path": "main/x.md"}, nil); code != 403 {
		t.Fatalf("denied move = %d", code)
	}
	// Nothing applied.
	if _, err := os.Stat(filepath.Join(e.dir, "main", "hello.md")); err != nil {
		t.Fatalf("hello moved despite denial: %v", err)
	}
}
