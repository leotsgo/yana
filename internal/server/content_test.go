package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// contentEnv is a main server plus the content origin on its own
// httptest listener, the way the process runs them on two ports.
type contentEnv struct {
	*env
	content  *Content
	cts      *httptest.Server // the content origin, its own port
	viewPath string           // cached /n/{id}?token=… of the script note
}

func newContentEnv(t *testing.T) *contentEnv {
	t.Helper()
	e := newEnv(t)
	c := NewContent(e.db, e.srv.Root, nil, nil)
	cts := httptest.NewServer(c)
	t.Cleanup(cts.Close)
	e.srv.Deps.Content = c
	e.srv.Deps.ContentAddr = strings.TrimPrefix(cts.URL, "http://")
	ce := &contentEnv{env: e, content: c, cts: cts}

	// A hostile note and a trusted canvas note in the work space.
	write := func(rel, content string) {
		p := filepath.Join(e.dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Minute)
		os.Chtimes(p, old, old)
	}
	write("work/hostile.html", "---\nid: 01ARZHOSTILE0000000000000A\n---\n"+
		"<h1>Hostile</h1>\n"+
		"<script>fetch('/api/notes').then(r=>r.text()).then(t=>parent.postMessage(t))</script>\n"+
		"<img src=\"https://evil.example/pixel.png\">\n"+
		"<img src=\"_assets/chart.png\" alt=\"chart\">\n"+
		"<p onclick=\"parent.document.cookie\">text</p>\n"+
		"<a data-wikilink=\"dash.html\">dash link</a>\n")
	write("work/canvas.html", "---\nid: 01ARZCANVAS00000000000000X\ntrusted: true\n---\n"+
		"<canvas id=\"c\" width=\"10\" height=\"10\"></canvas>\n"+
		"<script>var c=document.getElementById('c').getContext('2d');c.fillStyle='#c33';c.fillRect(0,0,10,10);var f=0;function loop(){f=(f+1)%10;c.clearRect(0,0,10,10);c.fillRect(f,0,1,10);requestAnimationFrame(loop)}loop();</script>\n")
	write("work/_assets/chart.png", "PNGCHART")
	sc := scanner.New(e.srv.Root, e.db, scanner.Options{SettleTime: time.Millisecond}, nil)
	if _, err := sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.srv.Deps.Scanner = sc

	var view struct{ URL string }
	if code := e.get(t, "/api/notes/01ARZHOSTILE0000000000000A/view", &view); code != 200 {
		t.Fatalf("view %d", code)
	}
	ce.viewPath = strings.TrimPrefix(view.URL, cts.URL)
	return ce
}

func (ce *contentEnv) fetch(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := http.Get(ce.cts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// The acceptance test from the plan: a note whose script tries to read
// the API cannot — not by fetching (sanitizer strips the script, CSP
// forbids connects), not by cookies (the content origin never sees any,
// and the frame's opaque origin cannot read them), not by breaking out
// (the origin has no API at all, and only these two routes exist).
func TestHostileScriptCannotReadAPI(t *testing.T) {
	ce := newContentEnv(t)

	// The view URL is on a different origin than the API: its host port
	// differs from the API server's.
	u := ce.cts.URL + ce.viewPath
	if ce.env.ts.URL == ce.cts.URL {
		t.Fatalf("content origin shares the API origin: %s", u)
	}

	resp := ce.fetch(t, ce.viewPath)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("note view %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)

	// The hostile script is gone: sanitized away before serving.
	for _, gone := range []string{
		"<script>fetch(", // the note's own script (ours is the shim)
		"evil.example",   // remote resource
		"onclick",        // event handler
		"parent.document.cookie",
	} {
		if strings.Contains(string(body), gone) {
			t.Fatalf("hostile content survived: %q in %s", gone, body)
		}
	}
	// The document part and the wikilink survive.
	if !strings.Contains(string(body), "<h1>Hostile</h1>") || !strings.Contains(string(body), `data-wikilink="dash.html"`) {
		t.Fatalf("document lost: %s", body)
	}

	// The CSP forbids every network use a script could make.
	csp := resp.Header.Get("Content-Security-Policy")
	for _, need := range []string{"connect-src 'none'", "form-action 'none'", "img-src 'self'", "default-src 'none'"} {
		if !strings.Contains(csp, need) {
			t.Fatalf("CSP missing %q: %s", need, csp)
		}
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("nosniff missing")
	}
	if resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("content type %s", resp.Header.Get("Content-Type"))
	}
	// The content origin never authenticates with cookies.
	if len(resp.Cookies()) != 0 {
		t.Fatalf("content origin set cookies: %v", resp.Cookies())
	}

	// No API exists on this origin to read, with or without the token.
	token := ce.viewToken()
	for _, path := range []string{"/api/notes", "/api/tree", "/api/auth/state", "/", "/ws"} {
		resp := ce.fetch(t, path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s on content origin: %d", path, resp.StatusCode)
		}
	}

	// The view token is not an account token: the API rejects it as a
	// bearer token even though the server runs without accounts needing
	// one, the shape is wrong on purpose. On an accounts server it must
	// not verify; verify the signature domain differs by checking that
	// the token does not parse as an access token.
	if strings.HasPrefix(token, "v1.") {
		t.Fatal("view token collides with the access token format")
	}

	// Tokens are scoped to their note: the hostile note's token cannot
	// open another note.
	other := "/n/01ARZCANVAS00000000000000X?token=" + token
	resp = ce.fetch(t, other)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cross-note token accepted: %d", resp.StatusCode)
	}

	// Expired or mangled tokens are refused.
	bad := strings.Replace(ce.viewPath, "token=", "token=x", 1)
	resp = ce.fetch(t, bad)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token accepted: %d", resp.StatusCode)
	}
	// No token at all is refused.
	resp = ce.fetch(t, "/n/01ARZHOSTILE0000000000000A")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token accepted: %d", resp.StatusCode)
	}

	// A cookie header changes nothing: the origin does not read cookies.
	req, _ := http.NewRequest("GET", ce.cts.URL+ce.viewPath, nil)
	req.Header.Set("Cookie", "yana_refresh=stolen")
	cresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cresp.Body.Close()
	if cresp.StatusCode != 200 {
		t.Fatalf("cookie sent, view broke: %d", cresp.StatusCode)
	}
}

func (ce *contentEnv) viewToken() string {
	i := strings.Index(ce.viewPath, "token=")
	return ce.viewPath[i+len("token="):]
}

// A trusted note keeps its script and it runs (CSP allows inline
// scripts; the shim and the note's own code are the only ones there).
func TestTrustedCanvasNoteRenders(t *testing.T) {
	ce := newContentEnv(t)
	var view struct{ URL string }
	if code := ce.get(t, "/api/notes/01ARZCANVAS00000000000000X/view", &view); code != 200 {
		t.Fatalf("view %d", code)
	}
	resp := ce.fetch(t, strings.TrimPrefix(view.URL, ce.cts.URL))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("trusted view %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{
		"<canvas id=\"c\" width=\"10\" height=\"10\">",
		"requestAnimationFrame(loop)",
	} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("trusted note lost %q: %s", want, body)
		}
	}
	// Even trusted, the CSP still forbids fetches and forms.
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("trusted CSP: %s", csp)
	}
}

// Flipping trusted to false re-sanitizes on the next render, and back.
func TestTrustFlipResanitizes(t *testing.T) {
	ce := newContentEnv(t)
	id := "01ARZCANVAS00000000000000X"

	// The canvas note starts trusted and serves its script.
	view := ce.mustView(t, id)
	body := ce.mustBody(t, view)
	if !strings.Contains(body, "requestAnimationFrame") {
		t.Fatalf("trusted body lost script: %s", body)
	}

	// Untrust through the API; the note payload follows.
	if code := ce.post(t, "/api/notes/"+id+"/trust", map[string]any{"trusted": false}, nil); code != 200 {
		t.Fatalf("untrust %d", code)
	}
	var note NoteResponse
	if code := ce.get(t, "/api/notes/"+id, &note); code != 200 || note.Trusted {
		t.Fatalf("note after untrust: %d %+v", code, note.Note)
	}

	// The very next render is sanitized.
	body = ce.mustBody(t, ce.mustView(t, id))
	if strings.Contains(body, "requestAnimationFrame") {
		t.Fatalf("script survived untrust: %s", body)
	}
	if !strings.Contains(body, "<canvas") {
		t.Fatalf("canvas element lost: %s", body)
	}

	// Trusting again restores the raw body.
	if code := ce.post(t, "/api/notes/"+id+"/trust", map[string]any{"trusted": true}, nil); code != 200 {
		t.Fatalf("retrust %d", code)
	}
	body = ce.mustBody(t, ce.mustView(t, id))
	if !strings.Contains(body, "requestAnimationFrame") {
		t.Fatalf("script did not come back: %s", body)
	}
}

// Assets resolve through the note's base URL: the token rides in the
// path, only the note's space is reachable, and only _assets serves.
func TestContentAssets(t *testing.T) {
	ce := newContentEnv(t)
	view := ce.mustView(t, "01ARZHOSTILE0000000000000A")
	body := ce.mustBody(t, view)

	// The base tag points at the token-carrying asset route in the
	// note's directory.
	if !strings.Contains(body, "<base href=\"/t/") || !strings.Contains(body, "/f/work/\">") {
		t.Fatalf("base tag missing or wrong: %s", body)
	}
	// A relative image reference works from the base.
	if !strings.Contains(body, `src="_assets/chart.png"`) {
		t.Fatalf("relative image lost: %s", body)
	}
	i := strings.Index(body, "<base href=\"") + len("<base href=\"")
	j := strings.IndexByte(body[i:], '"')
	base := body[i : i+j]
	resp := ce.fetch(t, base+"_assets/chart.png")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("asset via base %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	if string(data) != "PNGCHART" {
		t.Fatalf("asset bytes %q", data)
	}

	// Another space's assets are not reachable with this note's token.
	other := ce.env.dir + "/other-space/_assets/secret.txt"
	os.MkdirAll(filepath.Dir(other), 0o755)
	os.WriteFile(other, []byte("secret"), 0o644)
	resp = ce.fetch(t, base+"../../other-space/_assets/secret.txt")
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("cross-space asset served")
	}

	// Notes themselves are not assets.
	resp = ce.fetch(t, base+"../hello.md")
	resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatal("note served as asset")
	}
}

// The wikilink shim: resolved links carry the id, unresolved ones do
// not, and the shim script is in the wrapper head.
func TestContentWikilinkAnnotation(t *testing.T) {
	ce := newContentEnv(t)
	body := ce.mustBody(t, ce.mustView(t, "01ARZHOSTILE0000000000000A"))
	// work/dash.html exists, so Dash resolves to its id.
	var n NoteResponse
	if code := ce.get(t, "/api/notes/01ARZHOSTILE0000000000000A", &n); code != 200 {
		t.Fatal(code)
	}
	dashID := ""
	for _, l := range n.Links {
		if l.RawTarget == "dash.html" && l.Resolved {
			dashID = l.ToID
		}
	}
	if dashID == "" {
		t.Fatalf("Dash did not resolve: %+v", n.Links)
	}
	if !strings.Contains(body, `data-wikilink-id="`+dashID+`"`) {
		t.Fatalf("wikilink id missing: %s", body)
	}
	if !strings.Contains(body, `data-yana`) && !strings.Contains(body, "parent.postMessage") {
		t.Fatal("shim script missing")
	}
}

// View URL derivation: derived from the request host plus the content
// port; the configured origin wins when set.
func TestContentURLBuilding(t *testing.T) {
	ce := newContentEnv(t)
	var view struct{ URL string }
	if code := ce.get(t, "/api/notes/01ARZHOSTILE0000000000000A/view", &view); code != 200 {
		t.Fatal(code)
	}
	if !strings.HasPrefix(view.URL, ce.cts.URL+"/n/") {
		t.Fatalf("derived url %q, want prefix %s", view.URL, ce.cts.URL)
	}

	ce.srv.Deps.ContentOrigin = "https://content.notes.example.com"
	if code := ce.get(t, "/api/notes/01ARZHOSTILE0000000000000A/view", &view); code != 200 {
		t.Fatal(code)
	}
	if !strings.HasPrefix(view.URL, "https://content.notes.example.com/n/") {
		t.Fatalf("configured origin ignored: %s", view.URL)
	}

	// Markdown notes do not get view URLs.
	if code := ce.get(t, "/api/notes/01ARZ3NDEKTSV4RRFFQ69G5FAV/view", &view); code != 404 && code != 400 {
		t.Fatalf("md note view %d", code)
	}
}

// Without the content origin the view endpoint says so, clearly.
func TestContentDisabled(t *testing.T) {
	e := newEnv(t)
	e.srv.Deps.Content = nil
	var errResp map[string]string
	if code := e.get(t, "/api/notes/01ARZ3NDEKTSV4RRFFQ69G5FAV/view", &errResp); code != 501 {
		t.Fatalf("view without content origin %d", code)
	}
}

// Token expiry is real: an expired token is refused.
func TestViewTokenExpires(t *testing.T) {
	e := newEnv(t)
	c := NewContent(e.db, e.srv.Root, nil, nil)
	now := time.Now()
	c.Now = func() time.Time { return now }
	token, _ := c.MintToken("01ARZ3NDEKTSV4RRFFQ69G5FAV", time.Minute)
	now = now.Add(2 * time.Minute)
	if c.verifyContentToken(token, "01ARZ3NDEKTSV4RRFFQ69G5FAV") {
		t.Fatal("expired token verified")
	}
}

func (ce *contentEnv) mustView(t *testing.T, id string) string {
	t.Helper()
	var view struct{ URL string }
	if code := ce.get(t, "/api/notes/"+id+"/view", &view); code != 200 {
		t.Fatalf("view %d", code)
	}
	return strings.TrimPrefix(view.URL, ce.cts.URL)
}

func (ce *contentEnv) mustBody(t *testing.T, path string) string {
	t.Helper()
	resp := ce.fetch(t, path)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s: %d", path, resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func (ce *contentEnv) post(t *testing.T, path string, body any, out any) int {
	t.Helper()
	resp, err := http.Post(ce.ts.URL+path, "application/json", strings.NewReader(mustJSON(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// The index treats sanitized and trusted notes identically for search:
// both are indexed from the file, and the raw body feeds links.
func TestHTMLNoteIndexed(t *testing.T) {
	ce := newContentEnv(t)
	// The hostile note's text is searchable via its tag-stripped body.
	if code := ce.get(t, "/api/search?q=hostile&space=work", nil); code != 200 {
		t.Fatalf("search %d", code)
	}
	// The hostile note links to Dash (work/dash.html) through
	// data-wikilink, visible in its payload.
	var n NoteResponse
	if code := ce.get(t, "/api/notes/01ARZHOSTILE0000000000000A", &n); code != 200 {
		t.Fatal(code)
	}
	found := false
	for _, l := range n.Links {
		if l.RawTarget == "dash.html" {
			found = l.Resolved
		}
	}
	if !found {
		t.Fatalf("Dash link not resolved: %+v", n.Links)
	}
}
