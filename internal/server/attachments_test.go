package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/testpdf"
)

// attachEnv is a main server with the reconciliation loop and the content
// origin, which the attachment endpoints need.
type attachEnv struct {
	*env
	rec     *reconcile.Reconciler
	content *Content
	cts     *httptest.Server
}

func newAttachEnv(t *testing.T) *attachEnv {
	t.Helper()
	e := newEnv(t)
	sc := scanner.New(e.srv.Root, e.db, scanner.Options{SettleTime: time.Millisecond}, nil)
	e.srv.Deps.Scanner = sc
	rec := reconcile.New(e.srv.Root, e.db, sc, reconcile.Options{
		IdleTime: 60 * time.Millisecond, Debounce: 20 * time.Millisecond,
		SettleTime: 150 * time.Millisecond, UnloadAfter: -1,
	}, nil)
	if err := rec.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rec.Close() })
	c := NewContent(e.db, e.srv.Root, nil, nil)
	cts := httptest.NewServer(c)
	t.Cleanup(cts.Close)
	e.srv.Deps.Sync = rec
	e.srv.Deps.Content = c
	e.srv.Deps.ContentAddr = strings.TrimPrefix(cts.URL, "http://")
	return &attachEnv{env: e, rec: rec, content: c, cts: cts}
}

func (e *attachEnv) put(t *testing.T, path string, body []byte, out any) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, e.ts.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/pdf")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && resp.StatusCode < 500 {
			t.Fatalf("PUT %s: decode: %v", path, err)
		}
	}
	return resp.StatusCode
}

func TestUploadPDFIndexesTextAndPages(t *testing.T) {
	e := newAttachEnv(t)
	body := testpdf.Build("Kettle manual page one", "Descale quarterly with vinegar")
	var res struct {
		Path  string `json:"path"`
		Name  string `json:"name"`
		Size  int    `json:"size"`
		Pages *int   `json:"pages"`
		URL   string `json:"url"`
	}
	if code := e.put(t, "/api/files/home/_assets/kettle.pdf", body, &res); code != 201 {
		t.Fatalf("upload %d", code)
	}
	if res.Path != "home/_assets/kettle.pdf" || res.Name != "kettle.pdf" {
		t.Fatalf("upload result: %+v", res)
	}
	if res.Pages == nil || *res.Pages != 2 {
		t.Fatalf("pages in upload response: %+v", res)
	}
	if res.Size != len(body) {
		t.Fatalf("size %d, want %d", res.Size, len(body))
	}

	// Metadata endpoint: size from disk, pages from the index.
	var meta struct {
		Path  string `json:"path"`
		Name  string `json:"name"`
		Size  int64  `json:"size"`
		Pages *int   `json:"pages"`
		Kind  string `json:"kind"`
	}
	if code := e.get(t, "/api/attachments/home/_assets/kettle.pdf", &meta); code != 200 {
		t.Fatalf("attachment meta %d", code)
	}
	if meta.Kind != "pdf" || meta.Pages == nil || *meta.Pages != 2 || meta.Size != int64(len(body)) {
		t.Fatalf("meta: %+v", meta)
	}

	// Search finds a phrase from inside the PDF and the notes that use it.
	var search struct {
		Mode string `json:"mode"`
		Hits []struct {
			Note struct {
				ID string `json:"id"`
			} `json:"note"`
		} `json:"hits"`
		Attachments []struct {
			Path    string `json:"path"`
			Name    string `json:"name"`
			Snippet string `json:"snippet"`
			Refs    []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"refs"`
		} `json:"attachments"`
	}
	if code := e.get(t, "/api/search?q="+urlQueryEscape("Descale quarterly"), &search); code != 200 {
		t.Fatalf("search %d", code)
	}
	if len(search.Attachments) != 1 || search.Attachments[0].Name != "kettle.pdf" {
		t.Fatalf("attachment hits: %+v", search.Attachments)
	}
	if !strings.Contains(search.Attachments[0].Snippet, "Descale") {
		t.Fatalf("snippet: %+v", search.Attachments[0])
	}

	// A note that references the file is linked from the hit.
	e.writeOld(t, "home/kettle.md", "# Kettle\n\nSee [kettle.pdf](_assets/kettle.pdf).\n")
	if _, err := e.srv.Deps.Scanner.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if code := e.get(t, "/api/search?q=kettle.pdf", &search); code != 200 {
		t.Fatalf("search %d", code)
	}
	found := false
	for _, a := range search.Attachments {
		for _, ref := range a.Refs {
			if ref.Title == "Kettle" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no reference to kettle.pdf in hits: %+v", search.Attachments)
	}
}

func TestAssetContentTypes(t *testing.T) {
	e := newAttachEnv(t)
	e.writeOld(t, "home/_assets/manual.pdf", string(testpdf.Build("page one")))
	e.writeOld(t, "home/_assets/notes.txt", "plain text")
	e.writeOld(t, "home/_assets/report.docx", "PK\x03\x04 docx bytes")
	e.writeOld(t, "home/_assets/mystery.zzz", "whatever")
	if _, err := e.srv.Deps.Scanner.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	check := func(path, wantType, wantDisposition string) {
		t.Helper()
		resp, err := http.Get(e.ts.URL + "/api/files/" + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != wantType {
			t.Fatalf("%s: content type %q, want %q", path, got, wantType)
		}
		if got := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(got, wantDisposition) {
			t.Fatalf("%s: disposition %q, want %q…", path, got, wantDisposition)
		}
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s: nosniff missing", path)
		}
		if csp := resp.Header.Get("Content-Security-Policy"); csp != "default-src 'none'; sandbox" {
			t.Fatalf("%s: csp %q", path, csp)
		}
	}
	check("home/_assets/manual.pdf", "application/pdf", "inline")
	check("home/_assets/notes.txt", "text/plain; charset=utf-8", "inline")
	check("home/_assets/report.docx", "application/octet-stream", "attachment")
	check("home/_assets/mystery.zzz", "application/octet-stream", "attachment")
	check("home/_assets/pic.png", "image/png", "inline")
}

func TestAssetViewURLOnContentOrigin(t *testing.T) {
	e := newAttachEnv(t)
	e.writeOld(t, "home/_assets/manual.pdf", string(testpdf.Build("page one")))
	e.writeOld(t, "home/manuals.md", "# Manuals\n\n[manual.pdf](_assets/manual.pdf)\n")
	if _, err := e.srv.Deps.Scanner.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	note, err := e.db.GetNoteByPath(context.Background(), "home/manuals.md")
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		URL string `json:"url"`
	}
	path := fmt.Sprintf("/api/notes/%s/asset-view?asset=home/_assets/manual.pdf", note.ID)
	if code := e.get(t, path, &view); code != 200 {
		t.Fatalf("asset view %d", code)
	}
	if !strings.HasPrefix(view.URL, e.cts.URL+"/t/") || !strings.Contains(view.URL, "/f/home/_assets/manual.pdf") {
		t.Fatalf("asset view url: %q", view.URL)
	}
	// The minted URL serves the PDF on the content origin, sandboxed.
	resp, err := http.Get(view.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/pdf" {
		t.Fatalf("content origin serve: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if csp := resp.Header.Get("Content-Security-Policy"); csp != "default-src 'none'; sandbox" {
		t.Fatalf("content origin csp: %q", csp)
	}
	// An asset outside the note's space is refused.
	if code := e.get(t, "/api/notes/"+note.ID+"/asset-view?asset=work/_assets/other.pdf", nil); code != 403 {
		t.Fatalf("cross-space asset view: %d", code)
	}
}

func TestOrphanAssetsAndTrash(t *testing.T) {
	e := newAttachEnv(t)
	e.writeOld(t, "home/_assets/used.png", "PNG")
	e.writeOld(t, "home/_assets/lonely.pdf", string(testpdf.Build("nobody links me")))
	e.writeOld(t, "home/note.md", "# Note\n\n![used](_assets/used.png)\n")
	if _, err := e.srv.Deps.Scanner.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	var orphans struct {
		Assets []index.Asset `json:"assets"`
	}
	if code := e.get(t, "/api/assets/orphans", &orphans); code != 200 {
		t.Fatalf("orphans %d", code)
	}
	if len(orphans.Assets) != 1 || orphans.Assets[0].RelPath != "home/_assets/lonely.pdf" {
		t.Fatalf("orphans: %+v", orphans.Assets)
	}
	// Trashing moves the file to .trash and never deletes it.
	req, err := http.NewRequest(http.MethodDelete, e.ts.URL+"/api/files/home/_assets/lonely.pdf", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("trash asset: %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".trash", "home", "_assets", "lonely.pdf")); err != nil {
		t.Fatalf("trash copy missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "home", "_assets", "lonely.pdf")); !os.IsNotExist(err) {
		t.Fatal("original still there")
	}
	if code := e.get(t, "/api/assets/orphans", &orphans); code != 200 || len(orphans.Assets) != 0 {
		t.Fatalf("orphans after trash: %d %+v", code, orphans.Assets)
	}
	// Only asset paths may be trashed this way.
	req, _ = http.NewRequest(http.MethodDelete, e.ts.URL+"/api/files/home/note.md", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("note delete through files endpoint: %d", resp.StatusCode)
	}
}

// writeOld writes a file with an mtime in the past, like the env helpers.
func (e *attachEnv) writeOld(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(e.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	_ = os.Chtimes(p, old, old)
}

func urlQueryEscape(s string) string {
	r := strings.NewReplacer(" ", "%20", "?", "%3F", "&", "%26")
	return r.Replace(s)
}
