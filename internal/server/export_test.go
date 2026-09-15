package server

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/madeofpendletonwool/yana/internal/spaces"
)

// getBytes fetches a path and returns status, content type, and body.
func (e *env) getBytes(t *testing.T, path string) (int, string, []byte) {
	t.Helper()
	resp, err := http.Get(e.ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), body
}

func TestExportNoteHTML(t *testing.T) {
	e := newEnv(t)
	e.srv.SearchJS = []byte("// stub\n")
	n, err := e.db.GetNoteByPath(context.Background(), "home/hello.md")
	if err != nil {
		t.Fatal(err)
	}
	code, ctype, body := e.getBytes(t, "/api/notes/"+n.ID+"/export.html")
	if code != 200 || ctype != "text/html; charset=utf-8" {
		t.Fatalf("export: %d %s", code, ctype)
	}
	page := string(body)
	if !strings.Contains(page, "data:image/png;base64,") {
		t.Error("the note's image is not inlined")
	}
	if !strings.Contains(page, "<style>") {
		t.Error("the stylesheet is not inlined")
	}
}

func TestExportSiteZip(t *testing.T) {
	e := newEnv(t)
	e.srv.SearchJS = []byte("// stub\n")
	code, ctype, body := e.getBytes(t, "/api/spaces/home/export/site.zip")
	if code != 200 || ctype != "application/zip" {
		t.Fatalf("site export: %d %s", code, ctype)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		if strings.HasPrefix(f.Name, "work/") {
			t.Errorf("another space leaked into the site: %s", f.Name)
		}
	}
	for _, want := range []string{"index.html", "site.css", "search.html", "search.js", "search-index.js", "hello.html", "sub/second.html", "_assets/pic.png"} {
		if !names[want] {
			t.Errorf("%s missing from the site", want)
		}
	}
}

func TestExportSiteZipSubtreeParam(t *testing.T) {
	e := newEnv(t)
	e.srv.SearchJS = []byte("// stub\n")
	code, _, body := e.getBytes(t, "/api/spaces/home/export/site.zip?path=sub")
	if code != 200 {
		t.Fatalf("site export: %d", code)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == "hello.html" {
			t.Error("the subtree export carries notes outside the subtree")
		}
	}
	if code, _, _ := e.getBytes(t, "/api/spaces/home/export/site.zip?path=../work"); code != 400 {
		t.Fatalf("escaping path accepted: %d", code)
	}
}

func TestExportTreeZipRoundTripBytes(t *testing.T) {
	e := newEnv(t)
	code, ctype, body := e.getBytes(t, "/api/spaces/home/export/notes.zip")
	if code != 200 || ctype != "application/zip" {
		t.Fatalf("tree export: %d %s", code, ctype)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"home/hello.md", "home/sub/second.md", "home/_assets/pic.png"} {
		if !names[want] {
			t.Errorf("%s missing from the zip", want)
		}
	}
	if names["work/dash.html"] {
		t.Error("another space leaked into the zip")
	}
}

func TestExportAuthzBlocksNonMember(t *testing.T) {
	f := newAuthFixture(t)
	ctx := context.Background()
	if _, _, err := f.as.Setup(ctx, "owner", "owner-password", "t"); err != nil {
		t.Fatal(err)
	}
	owner := f.ownerIdent(t)
	member, err := f.as.CreateUser(ctx, owner, "member", "member-password1")
	if err != nil {
		t.Fatal(err)
	}
	// theirs has no .space.yml: members cannot see it.
	f.writeNote(t, "home/mine.md", "# Mine\n")
	secret := f.writeNote(t, "theirs/secret.md", "# Secret\n")
	f.writeSpaceFile(t, "home", spaces.Spec{Name: "home", Members: []spaces.Member{
		{User: member.ID, Role: spaces.RoleEditor},
	}})
	if _, err := f.sc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	_, authHeader := f.accountLogin(t, "member", "member-password1")
	req := func(path string) int {
		r, err := http.NewRequest("GET", f.ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", authHeader)
		resp, err := f.ts.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := req("/api/spaces/home/export/site.zip"); code != 200 {
		t.Errorf("member space site export: %d", code)
	}
	if code := req("/api/spaces/home/export/notes.zip"); code != 200 {
		t.Errorf("member space tree export: %d", code)
	}
	if code := req("/api/spaces/theirs/export/site.zip"); code != 404 {
		t.Errorf("non-member site export: %d", code)
	}
	if code := req("/api/spaces/theirs/export/notes.zip"); code != 404 {
		t.Errorf("non-member tree export: %d", code)
	}
	if code := req("/api/notes/" + secret + "/export.html"); code != 404 {
		t.Errorf("non-member note export: %d", code)
	}
}
