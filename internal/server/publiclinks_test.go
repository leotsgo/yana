package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// publicEnv is the account-less server with its content origin and a
// few notes to share: a markdown note with a picture and links, the
// note one of those links points at, and an HTML note that would run
// scripts if it could.
type publicEnv struct {
	*contentEnv
	recipeID, otherID string
}

func newPublicEnv(t *testing.T) *publicEnv {
	t.Helper()
	ce := newContentEnv(t)
	ce.content.Rich = fstest.MapFS{
		"mermaid.js":          {Data: []byte("/*mermaid*/")},
		"katex.js":            {Data: []byte("/*katex*/")},
		"katex-style.css":     {Data: []byte("@font-face{src:url(./katex-fonts/a.woff2)}")},
		"katex-fonts/a.woff2": {Data: []byte("WOFF")},
	}
	write := func(rel, content string) {
		p := filepath.Join(ce.dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Minute)
		os.Chtimes(p, old, old)
	}
	write("home/recipes/jam.md", "---\nid: 01ARZRECIPE00000000000000A\n---\n# Jam\n\n"+
		"![pot](_assets/pot.png) and ![logo](../_assets/pic.png)\n\n"+
		"See [[other]] and [[hello]] and [the pdf](_assets/how.pdf).\n\n```mermaid\ngraph TD; a-->b\n```\n")
	write("home/recipes/_assets/pot.png", "POT")
	write("home/recipes/_assets/how.pdf", "%PDF")
	write("home/other.md", "---\nid: 01ARZOTHER000000000000000A\n---\n# Other\n\nback to [[jam]]\n")
	sc := scanner.New(ce.srv.Root, ce.db, scanner.Options{SettleTime: time.Millisecond}, nil)
	if _, err := sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &publicEnv{contentEnv: ce, recipeID: "01ARZRECIPE00000000000000A", otherID: "01ARZOTHER000000000000000A"}
}

func (e *publicEnv) call(t *testing.T, method, path string, body string, out any) int {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
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

type linkResp struct {
	Link *struct {
		ID        string     `json:"id"`
		NoteID    string     `json:"note_id"`
		URL       string     `json:"url"`
		ExpiresAt *time.Time `json:"expires_at"`
	} `json:"link"`
	Created bool `json:"created"`
}

// share creates the note's link and returns its path on the content origin.
func (e *publicEnv) share(t *testing.T, id, body string) (linkResp, string) {
	t.Helper()
	var lr linkResp
	code := e.call(t, "POST", "/api/notes/"+id+"/public-link", body, &lr)
	if code != 201 && code != 200 {
		t.Fatalf("share %s: %d", id, code)
	}
	if lr.Link == nil || !strings.HasPrefix(lr.Link.URL, e.cts.URL+"/p/") {
		t.Fatalf("share %s: bad link %+v", id, lr.Link)
	}
	return lr, strings.TrimPrefix(lr.Link.URL, e.cts.URL)
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPublicLinkServesNoteWithoutAuth(t *testing.T) {
	e := newPublicEnv(t)
	lr, p := e.share(t, e.recipeID, "")
	if !lr.Created {
		t.Fatal("first share should create")
	}
	if lr.Link.ExpiresAt != nil {
		t.Fatalf("no expiry asked for, got %v", lr.Link.ExpiresAt)
	}
	// The token is not the id, not the path, and unguessable in length.
	token := strings.TrimPrefix(p, "/p/")
	if len(token) < 40 || strings.Contains(token, e.recipeID) {
		t.Fatalf("token %q", token)
	}

	resp := e.fetch(t, p)
	page := readAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("public page: %d %s", resp.StatusCode, page)
	}
	for _, h := range [][2]string{
		{"Cache-Control", "no-store"},
		{"X-Robots-Tag", "noindex, nofollow, noarchive"},
		{"Referrer-Policy", "no-referrer"},
		{"X-Content-Type-Options", "nosniff"},
	} {
		if got := resp.Header.Get(h[0]); got != h[1] {
			t.Errorf("%s = %q, want %q", h[0], got, h[1])
		}
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Error("a public page must not set cookies")
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "script-src 'unsafe-inline'") {
		t.Errorf("csp %q", csp)
	}
	// The body: rendered, with a robots meta, no path, space or id.
	for _, want := range []string{"<h1", "Jam</h1>", `name="robots"`, "<pre class=\"mermaid\">", `/r/mermaid.js`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, leak := range []string{"home/", "recipes/jam", e.recipeID, "exported"} {
		if strings.Contains(page, leak) {
			t.Errorf("page reveals %q", leak)
		}
	}
	// Pictures go through the same token, relative to the note's directory.
	if !strings.Contains(page, `src="/p/`+token+`/f/pot.png?at=_assets%2Fpot.png"`) {
		t.Errorf("sibling asset not rewritten: %s", page)
	}
	if !strings.Contains(page, `/f/pic.png?at=..%2F_assets%2Fpic.png"`) {
		t.Errorf("parent asset not rewritten: %s", page)
	}
	if !strings.Contains(page, `href="/p/`+token+`/f/how.pdf?at=_assets%2Fhow.pdf"`) {
		t.Errorf("attachment not rewritten: %s", page)
	}
	// Wikilinks are plain text while their targets are private.
	if strings.Contains(page, `<a class="wikilink"`) || !strings.Contains(page, `<span class="wikilink">other</span>`) {
		t.Errorf("private wikilink should be text: %s", page)
	}

	// The pictures themselves.
	for _, a := range []struct{ path, body string }{
		{"/p/" + token + "/f/pot.png?at=_assets%2Fpot.png", "POT"},
		{"/p/" + token + "/f/pic.png?at=..%2F_assets%2Fpic.png", "PNGDATA"},
		{"/p/" + token + "/f/how.pdf?at=_assets%2Fhow.pdf", "%PDF"},
	} {
		resp := e.fetch(t, a.path)
		body := readAll(t, resp)
		if resp.StatusCode != 200 || body != a.body {
			t.Errorf("%s: %d %q", a.path, resp.StatusCode, body)
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: cache-control %q", a.path, resp.Header.Get("Cache-Control"))
		}
	}
	// Not the note file, not another space's assets, nothing outside _assets.
	for _, bad := range []string{
		"/p/" + token + "/f/jam.md?at=jam.md",
		"/p/" + token + "/f/other.md?at=..%2Fother.md",
		"/p/" + token + "/f/chart.png?at=..%2F..%2Fwork%2F_assets%2Fchart.png",
		"/p/" + token + "/f/x?at=..%2F..%2F..%2Fetc%2Fpasswd",
		"/p/" + token + "/f/pot.png",
		"/p/" + token + "/x",
	} {
		resp := e.fetch(t, bad)
		readAll(t, resp)
		if resp.StatusCode != 404 {
			t.Errorf("%s: %d, want 404", bad, resp.StatusCode)
		}
	}
	// The runtimes and the fonts the stylesheet refers to.
	for _, r := range []string{"/r/mermaid.js", "/r/katex.js", "/r/katex.css", "/r/katex-fonts/a.woff2"} {
		resp := e.fetch(t, r)
		readAll(t, resp)
		if resp.StatusCode != 200 {
			t.Errorf("%s: %d", r, resp.StatusCode)
		}
	}
	resp = e.fetch(t, "/r/../index.db")
	readAll(t, resp)
	if resp.StatusCode != 404 {
		t.Errorf("runtime traversal: %d", resp.StatusCode)
	}
}

func TestPublicLinkIsOnePerNoteAndRevokes(t *testing.T) {
	e := newPublicEnv(t)
	first, p := e.share(t, e.recipeID, "")
	second, p2 := e.share(t, e.recipeID, `{"expires":"1d"}`)
	if second.Created || p2 != p || second.Link.ID != first.Link.ID {
		t.Fatalf("second share should return the first link: %+v vs %+v", first.Link, second.Link)
	}
	var got linkResp
	if code := e.call(t, "GET", "/api/notes/"+e.recipeID+"/public-link", "", &got); code != 200 || got.Link == nil || got.Link.URL != first.Link.URL {
		t.Fatalf("get: %d %+v", code, got.Link)
	}
	var note struct {
		Public bool `json:"public"`
	}
	if code := e.get(t, "/api/notes/"+e.recipeID, &note); code != 200 || !note.Public {
		t.Fatalf("note should read public: %d %+v", code, note)
	}
	var tree struct {
		Spaces []struct {
			Children []struct {
				Type     string `json:"type"`
				ID       string `json:"id"`
				Public   bool   `json:"public"`
				Children []struct {
					ID     string `json:"id"`
					Public bool   `json:"public"`
				} `json:"children"`
			} `json:"children"`
		} `json:"spaces"`
	}
	if code := e.get(t, "/api/tree", &tree); code != 200 {
		t.Fatalf("tree %d", code)
	}
	marked := false
	for _, sp := range tree.Spaces {
		for _, c := range sp.Children {
			if c.ID == e.recipeID && c.Public {
				marked = true
			}
			if c.ID == e.otherID && c.Public {
				t.Error("other note should not be marked")
			}
			for _, cc := range c.Children {
				if cc.ID == e.recipeID && cc.Public {
					marked = true
				}
			}
		}
	}
	if !marked {
		t.Error("tree should mark the shared note")
	}

	// A revoked link is a 404 at once, same as one that never existed.
	var rv struct {
		Revoked bool `json:"revoked"`
	}
	if code := e.call(t, "DELETE", "/api/notes/"+e.recipeID+"/public-link", "", &rv); code != 200 || !rv.Revoked {
		t.Fatalf("revoke: %d %+v", code, rv)
	}
	resp := e.fetch(t, p)
	gone := readAll(t, resp)
	resp2 := e.fetch(t, "/p/"+strings.Repeat("x", 43))
	never := readAll(t, resp2)
	if resp.StatusCode != 404 || resp2.StatusCode != 404 || gone != never {
		t.Fatalf("revoked %d %q vs never %d %q", resp.StatusCode, gone, resp2.StatusCode, never)
	}
	if code := e.call(t, "GET", "/api/notes/"+e.recipeID+"/public-link", "", &got); code != 200 || got.Link != nil {
		t.Fatalf("get after revoke: %d %+v", code, got.Link)
	}
	if code := e.call(t, "DELETE", "/api/notes/"+e.recipeID+"/public-link", "", &rv); code != 200 || rv.Revoked {
		t.Fatalf("revoke twice: %d %+v", code, rv)
	}
	// Sharing again makes a new link with a new address.
	third, p3 := e.share(t, e.recipeID, "")
	if !third.Created || p3 == p {
		t.Fatalf("share after revoke: %+v %s", third, p3)
	}
}

func TestPublicLinkExpiry(t *testing.T) {
	e := newPublicEnv(t)
	lr, p := e.share(t, e.recipeID, `{"expires":"1w"}`)
	if lr.Link.ExpiresAt == nil || time.Until(*lr.Link.ExpiresAt) < 6*24*time.Hour {
		t.Fatalf("expiry %v", lr.Link.ExpiresAt)
	}
	var upd linkResp
	if code := e.call(t, "PUT", "/api/notes/"+e.recipeID+"/public-link", `{"expires":"1d"}`, &upd); code != 200 || upd.Link.ExpiresAt == nil || time.Until(*upd.Link.ExpiresAt) > 25*time.Hour {
		t.Fatalf("update: %d %+v", code, upd.Link)
	}
	if code := e.call(t, "PUT", "/api/notes/"+e.recipeID+"/public-link", `{"expires":"never"}`, &upd); code != 200 || upd.Link.ExpiresAt != nil {
		t.Fatalf("update never: %d %+v", code, upd.Link)
	}
	if code := e.call(t, "PUT", "/api/notes/"+e.recipeID+"/public-link", `{"expires":"soon"}`, nil); code != 400 {
		t.Fatalf("bad expiry: %d", code)
	}
	// Move the clock past an expiry: the page is gone, the listing empty.
	e.content.Now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	if code := e.call(t, "PUT", "/api/notes/"+e.recipeID+"/public-link", `{"expires":"1d"}`, &upd); code != 200 {
		t.Fatalf("update 1d: %d", code)
	}
	resp := e.fetch(t, p)
	readAll(t, resp)
	if resp.StatusCode != 404 {
		t.Fatalf("expired link: %d", resp.StatusCode)
	}
}

func TestPublicLinkTargetsLinkWhenPublicToo(t *testing.T) {
	e := newPublicEnv(t)
	_, p := e.share(t, e.recipeID, "")
	_, p2 := e.share(t, e.otherID, "")
	resp := e.fetch(t, p)
	page := readAll(t, resp)
	if !strings.Contains(page, `<a class="wikilink" href="`+p2+`">other</a>`) {
		t.Errorf("public target should link: %s", page)
	}
	if !strings.Contains(page, `<span class="wikilink">hello</span>`) {
		t.Errorf("private target should stay text: %s", page)
	}
	resp = e.fetch(t, p2)
	page = readAll(t, resp)
	if !strings.Contains(page, `<a class="wikilink" href="`+p+`">jam</a>`) {
		t.Errorf("back link should link: %s", page)
	}
}

func TestPublicLinkHTMLNoteIsAlwaysSanitized(t *testing.T) {
	e := newPublicEnv(t)
	// canvas.html is trusted in the app; a link still strips its script.
	_, p := e.share(t, "01ARZCANVAS00000000000000X", "")
	resp := e.fetch(t, p)
	page := readAll(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("html page: %d", resp.StatusCode)
	}
	if strings.Contains(page, "getContext") || strings.Contains(page, "<script>var") {
		t.Errorf("trusted note's script served publicly: %s", page)
	}
	if !strings.Contains(page, "<canvas") {
		t.Errorf("markup lost: %s", page)
	}
	// The hostile note: its script goes, its sibling picture is served.
	_, p = e.share(t, "01ARZHOSTILE0000000000000A", "")
	resp = e.fetch(t, p)
	page = readAll(t, resp)
	token := strings.TrimPrefix(p, "/p/")
	if strings.Contains(page, "fetch(") || strings.Contains(page, "onclick") {
		t.Errorf("hostile markup survived: %s", page)
	}
	if !strings.Contains(page, `src="/p/`+token+`/f/chart.png?at=_assets%2Fchart.png"`) {
		t.Errorf("html note asset not rewritten: %s", page)
	}
}

func TestPublicLinkListAndRevokeAll(t *testing.T) {
	e := newPublicEnv(t)
	e.share(t, e.recipeID, "")
	e.share(t, e.otherID, "")
	var list struct {
		Links []struct {
			NoteID string `json:"note_id"`
			Title  string `json:"title"`
			Path   string `json:"path"`
			Space  string `json:"space"`
			URL    string `json:"url"`
		} `json:"links"`
	}
	if code := e.get(t, "/api/public-links", &list); code != 200 || len(list.Links) != 2 {
		t.Fatalf("list: %d %+v", code, list)
	}
	if list.Links[0].Space != "home" || list.Links[0].Path == "" || list.Links[0].Title == "" {
		t.Errorf("row %+v", list.Links[0])
	}
	var rv struct {
		Revoked int `json:"revoked"`
	}
	if code := e.call(t, "POST", "/api/public-links/revoke-all", "", &rv); code != 200 || rv.Revoked != 2 {
		t.Fatalf("revoke all: %d %+v", code, rv)
	}
	if code := e.get(t, "/api/public-links", &list); code != 200 || len(list.Links) != 0 {
		t.Fatalf("list after: %d %+v", code, list)
	}
}

func TestPublicLinkDiesWithTheNote(t *testing.T) {
	e := newPublicEnv(t)
	_, p := e.share(t, e.otherID, "")
	// Retire the note the way every delete path does; a restore brings
	// the note back with the same id, not the link.
	n, err := e.db.GetNote(context.Background(), e.otherID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.db.Write(context.Background(), func(tx *sql.Tx) error {
		return index.RetireNote(tx, n, "", time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	resp := e.fetch(t, p)
	readAll(t, resp)
	if resp.StatusCode != 404 {
		t.Fatalf("deleted note's link: %d", resp.StatusCode)
	}
	if err := e.db.Write(context.Background(), func(tx *sql.Tx) error {
		return index.UpsertNote(tx, n, "other", "# Other\n", nil)
	}); err != nil {
		t.Fatal(err)
	}
	resp = e.fetch(t, p)
	readAll(t, resp)
	if resp.StatusCode != 404 {
		t.Fatalf("restored note resurrected its link: %d", resp.StatusCode)
	}
	if _, err := e.db.PublicLinkForNote(context.Background(), e.otherID, time.Now()); err == nil {
		t.Fatal("restored note should have no live link")
	}
}

func TestPublicLinkRateLimited(t *testing.T) {
	e := newPublicEnv(t)
	_, p := e.share(t, e.otherID, "")
	var last int
	for i := 0; i < publicLinkRate+5; i++ {
		resp := e.fetch(t, p)
		readAll(t, resp)
		last = resp.StatusCode
	}
	if last != 429 {
		t.Fatalf("after %d reads: %d, want 429", publicLinkRate+5, last)
	}
}

func TestPublicLinkRequiresMembership(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)
	c := NewContent(f.db, f.root, nil, nil)
	cts := httptest.NewServer(c)
	t.Cleanup(cts.Close)
	f.srv.Deps.Content = c
	f.srv.Deps.ContentAddr = strings.TrimPrefix(cts.URL, "http://")

	// eve is a viewer of home: a member, so she may share; work is
	// owner-only and must look missing to her.
	code, out := doPost(t, f.ts, "POST", "/api/notes/"+w.homeID+"/public-link", w.eveHdr, map[string]any{})
	if code != 201 {
		t.Fatalf("viewer share: %d %v", code, out)
	}
	link := out["link"].(map[string]any)
	url := link["url"].(string)
	if code, _ := doPost(t, f.ts, "POST", "/api/notes/"+w.workID+"/public-link", w.eveHdr, map[string]any{}); code != 404 {
		t.Fatalf("non-member share: %d", code)
	}
	if code, _ := doGet(t, f.ts, "/api/notes/"+w.workID+"/public-link", w.samHdr); code != 404 {
		t.Fatalf("non-member get: %d", code)
	}
	if code, _ := doPost(t, f.ts, "DELETE", "/api/notes/"+w.workID+"/public-link", w.samHdr, nil); code != 404 {
		t.Fatalf("non-member revoke: %d", code)
	}
	// No account at all: the page still opens; the API does not.
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	page := readAll(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(page, "raspberry jam") {
		t.Fatalf("anonymous read: %d %s", resp.StatusCode, page)
	}
	if code, _ := doGet(t, f.ts, "/api/notes/"+w.homeID+"/public-link", ""); code != 401 {
		t.Fatalf("anonymous api: %d", code)
	}
	// The owner shares work; sam's listing shows only home's link, and
	// his revoke-all leaves work's alone.
	if code, _ := doPost(t, f.ts, "POST", "/api/notes/"+w.workID+"/public-link", w.ownerHdr, map[string]any{}); code != 201 {
		t.Fatalf("owner share: %d", code)
	}
	code, out = doGet(t, f.ts, "/api/public-links", w.samHdr)
	if code != 200 || len(out["links"].([]any)) != 1 {
		t.Fatalf("sam's list: %d %v", code, out)
	}
	code, out = doGet(t, f.ts, "/api/public-links", w.ownerHdr)
	if code != 200 || len(out["links"].([]any)) != 2 {
		t.Fatalf("owner's list: %d %v", code, out)
	}
	code, out = doPost(t, f.ts, "POST", "/api/public-links/revoke-all", w.samHdr, nil)
	if code != 200 || out["revoked"].(float64) != 1 {
		t.Fatalf("sam's revoke all: %d %v", code, out)
	}
	code, out = doGet(t, f.ts, "/api/public-links", w.ownerHdr)
	if code != 200 || len(out["links"].([]any)) != 1 {
		t.Fatalf("owner's list after: %d %v", code, out)
	}
}
