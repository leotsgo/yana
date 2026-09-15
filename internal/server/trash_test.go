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

type trashEnv struct {
	env
	rec *reconcile.Reconciler
}

func newTrashEnv(t *testing.T, canWrite func(r *http.Request, space string) error) *trashEnv {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
		old := time.Now().Add(-time.Minute)
		os.Chtimes(p, old, old)
	}
	write("main/keep.md", "# Keep\n")
	write("main/doom.md", "# Doomed\n\nlast words\n")

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
		Version: "test", Scanner: sc, Sync: rec, CanWrite: canWrite,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &trashEnv{env: env{dir: dir, srv: srv, ts: ts, db: db}, rec: rec}
}

func (e *trashEnv) req(t *testing.T, method, path string, out any) int {
	t.Helper()
	req, err := http.NewRequest(method, e.ts.URL+path, nil)
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
			t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

func (e *trashEnv) post(t *testing.T, path string, out any) int {
	t.Helper()
	resp, err := http.Post(e.ts.URL+path, "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && resp.StatusCode < 500 {
			t.Fatalf("POST %s: decode: %v", path, err)
		}
	}
	return resp.StatusCode
}

func TestTrashEndpointsEndToEnd(t *testing.T) {
	e := newTrashEnv(t, nil)
	doom, err := e.db.GetNoteByPath(context.Background(), "main/doom.md")
	if err != nil {
		t.Fatal(err)
	}

	var delRes struct {
		OK        bool   `json:"ok"`
		TrashPath string `json:"trash_path"`
	}
	if code := e.req(t, http.MethodDelete, "/api/notes/"+doom.ID, &delRes); code != 200 {
		t.Fatalf("delete %d", code)
	}
	if !delRes.OK || delRes.TrashPath != ".trash/main/doom.md" {
		t.Fatalf("delete result: %+v", delRes)
	}
	if b, err := os.ReadFile(filepath.Join(e.dir, ".trash", "main", "doom.md")); err != nil || !strings.Contains(string(b), "last words") {
		t.Fatalf("trash copy: %v", err)
	}

	var list struct {
		Entries []struct {
			ID      string `json:"id"`
			Path    string `json:"path"`
			HasFile bool   `json:"has_file"`
		} `json:"entries"`
	}
	if code := e.req(t, http.MethodGet, "/api/trash", &list); code != 200 || len(list.Entries) != 1 {
		t.Fatalf("list %d %+v", code, list)
	}
	if list.Entries[0].ID != doom.ID || list.Entries[0].Path != "main/doom.md" || !list.Entries[0].HasFile {
		t.Fatalf("entry: %+v", list.Entries[0])
	}

	var restore struct {
		OK   bool   `json:"ok"`
		Path string `json:"path"`
	}
	if code := e.post(t, "/api/trash/"+doom.ID+"/restore", &restore); code != 200 || restore.Path != "main/doom.md" {
		t.Fatalf("restore %d %+v", code, restore)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "main", "doom.md")); err != nil {
		t.Fatalf("restored file: %v", err)
	}
	if code := e.req(t, http.MethodGet, "/api/trash", &list); code != 200 || len(list.Entries) != 0 {
		t.Fatalf("list after restore %d %+v", code, list)
	}

	// Destroy and empty on a fresh deletion.
	if code := e.req(t, http.MethodDelete, "/api/notes/"+doom.ID, nil); code != 200 {
		t.Fatalf("re-delete %d", code)
	}
	var emptied struct {
		Destroyed int `json:"destroyed"`
	}
	if code := e.post(t, "/api/trash/empty", &emptied); code != 200 || emptied.Destroyed != 1 {
		t.Fatalf("empty %d %+v", code, emptied)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".trash", "main", "doom.md")); !os.IsNotExist(err) {
		t.Fatal("empty left the trash copy")
	}
}

func TestTrashDestroyOne(t *testing.T) {
	e := newTrashEnv(t, nil)
	n, err := e.db.GetNoteByPath(context.Background(), "main/doom.md")
	if err != nil {
		t.Fatal(err)
	}
	if code := e.req(t, http.MethodDelete, "/api/notes/"+n.ID, nil); code != 200 {
		t.Fatalf("delete %d", code)
	}
	if code := e.req(t, http.MethodDelete, "/api/trash/"+n.ID, nil); code != 200 {
		t.Fatalf("destroy %d", code)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".trash", "main", "doom.md")); !os.IsNotExist(err) {
		t.Fatal("destroy left the file")
	}
	if code := e.req(t, http.MethodDelete, "/api/trash/"+n.ID, nil); code != 404 {
		t.Fatalf("destroy again %d", code)
	}
}

func TestTrashRespectsWritePermission(t *testing.T) {
	e := newTrashEnv(t, func(r *http.Request, space string) error {
		if r.Method == http.MethodGet {
			return nil
		}
		return errNoWrite
	})
	n, err := e.db.GetNoteByPath(context.Background(), "main/doom.md")
	if err != nil {
		t.Fatal(err)
	}
	if code := e.req(t, http.MethodDelete, "/api/notes/"+n.ID, nil); code != 403 {
		t.Fatalf("delete without write: %d", code)
	}
	if code := e.post(t, "/api/trash/"+n.ID+"/restore", nil); code != 404 {
		t.Fatalf("restore of nothing: %d", code)
	}
}

func TestTrashWithoutSyncIsNotImplemented(t *testing.T) {
	e := newEnv(t) // no Sync in Deps
	n, err := e.db.GetNoteByPath(context.Background(), "home/hello.md")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodDelete, e.ts.URL+"/api/notes/"+n.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("delete without sync: %d", resp.StatusCode)
	}
	if code := e.get(t, "/api/trash", nil); code != http.StatusNotImplemented {
		t.Fatalf("list without sync: %d", code)
	}
}

var errNoWrite = staticNoWrite("this space is read-only")

type staticNoWrite string

func (e staticNoWrite) Error() string { return string(e) }

// With accounts on: the trash never crosses a space boundary the caller
// cannot write, and emptying reaches only the caller's spaces.
func TestTrashIsSpaceScoped(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)

	// The owner deletes one note in each space.
	for _, id := range []string{w.homeID, w.workID} {
		if code, _ := doPost(t, f.ts, http.MethodDelete, "/api/notes/"+id, w.ownerHdr, nil); code != 200 {
			t.Fatalf("owner delete %s: %d", id, code)
		}
	}

	// Sam (editor of home only) sees home's entry, not work's.
	code, body := doGet(t, f.ts, "/api/trash", w.samHdr)
	if code != 200 {
		t.Fatalf("sam list: %d", code)
	}
	entries := body["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("sam sees %d entries, want 1: %v", len(entries), entries)
	}
	entry := entries[0].(map[string]any)
	if entry["id"] != w.homeID || entry["path"] != "home/home-secret.md" {
		t.Fatalf("entry: %v", entry)
	}

	// Sam can restore home's note; the work entry is invisible and its
	// restore reads as missing.
	if code, _ := doPost(t, f.ts, http.MethodPost, "/api/trash/"+w.homeID+"/restore", w.samHdr, nil); code != 200 {
		t.Fatalf("sam restore: %d", code)
	}
	if code, _ := doPost(t, f.ts, http.MethodPost, "/api/trash/"+w.workID+"/restore", w.samHdr, nil); code != 404 {
		t.Fatalf("sam restoring another space: %d", code)
	}

	// Eve is a viewer in home: she sees the (now empty) trash but cannot
	// delete or empty into it.
	if code, _ := doPost(t, f.ts, http.MethodDelete, "/api/notes/"+w.homeID, w.eveHdr, nil); code != 403 && code != 404 {
		t.Fatalf("viewer delete: %d", code)
	}

	// The owner empties everything.
	code, body = doPost(t, f.ts, http.MethodPost, "/api/trash/empty", w.ownerHdr, nil)
	if code != 200 || body["destroyed"] != float64(1) {
		t.Fatalf("owner empty: %d %v", code, body)
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".trash", "work", "work-secret.md")); !os.IsNotExist(err) {
		t.Fatal("empty left work's trash copy")
	}
}
