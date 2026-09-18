// The content origin: the second listener HTML notes render from. It is
// a different origin (its own port, or a subdomain behind a proxy) so the
// sandboxed frame that draws a note cannot reach the app's API, its
// cookies, or its DOM even if the note's markup turns out to do more than
// the sanitizer expected. The routes here, and nothing else:
//
//	GET /n/{id}?token=…            the rendered note
//	GET /t/{token}/f/{path…}       an asset, resolved against the note
//	GET /p/{token}                 a public link's note, read-only
//	GET /p/{token}/f/{name}?at=…   an asset beside that note
//	GET /r/{name}                  the diagram and math runtimes
//
// The view token is minted by the main API (GET /api/notes/{id}/view),
// signed with the content secret, scoped to one note, and short-lived.
// It is not an account token: it opens exactly this note and the assets
// beside it, nothing else. Accounts are not consulted here at all, so
// the frame's opaque origin — which can neither read cookies nor set
// headers — loses nothing it needs. A public link's token is the same
// idea held open: derived from the link's row with the same secret,
// good until the link is revoked or expires (see publiclinks.go).
package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/render"
)

// ViewTTL is how long a minted note-view token opens its note. A frame
// that is already loaded keeps rendering; only loading a new one needs a
// fresh token.
const ViewTTL = 5 * time.Minute

// Content is the handler mounted on the content origin's listener.
type Content struct {
	DB     *index.DB
	Root   *pathsafe.Root
	Log    Logger
	secret []byte
	Now    func() time.Time
	// Rich holds the diagram and math runtimes public pages link (see
	// export.Deps.Rich); nil serves a diagram or an equation as text.
	Rich fs.FS
	// linkRate bounds requests per public link, page and assets alike.
	linkRate *pathsafe.RateLimiter
}

// NewContent builds the content-origin handler. The secret signs view
// tokens and derives public-link tokens; a nil secret gets fresh random
// bytes, which means neither survives a restart (the process loads a
// persisted one so public links do).
func NewContent(db *index.DB, root *pathsafe.Root, secret []byte, log Logger) *Content {
	if secret == nil {
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
	}
	if log == nil {
		log = noopLogger{}
	}
	rate := pathsafe.Rate{N: publicLinkRate, Window: time.Minute}
	return &Content{DB: db, Root: root, Log: log, secret: secret, Now: time.Now,
		linkRate: pathsafe.NewRateLimiter(rate, rate)}
}

// Logger is what Content needs from a logger.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}

var contentTokenPurpose = []byte("yana-content-view\x00")

// MintToken signs a token that opens one note until exp.
func (c *Content) MintToken(noteID string, ttl time.Duration) (token string, exp time.Time) {
	exp = c.Now().Add(ttl).UTC()
	return signContentToken(c.secret, noteID, exp), exp
}

func signContentToken(secret []byte, noteID string, exp time.Time) string {
	payload := noteID + "." + strconv.FormatInt(exp.UnixNano(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write(contentTokenPurpose)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifyContentToken checks the signature, expiry, and that the token is
// for this note.
func (c *Content) verifyContentToken(token, noteID string) bool {
	i := strings.LastIndexByte(token, '.') // payload is id.exp; sig is last
	if i < 0 {
		return false
	}
	payload, sig := token[:i], token[i+1:]
	id, expStr, ok := strings.Cut(payload, ".")
	if !ok || id != noteID {
		return false
	}
	expNano, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || !c.Now().UTC().Before(time.Unix(0, expNano)) {
		return false
	}
	want, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, c.secret)
	mac.Write(contentTokenPurpose)
	mac.Write([]byte(payload))
	return hmac.Equal(mac.Sum(nil), want)
}

// contentCSP is the content security policy on every note page: scripts
// and styles inline (the note is the whole document), images, fonts, and
// media only from this origin, no fetches, no forms, no framing others.
const contentCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
	"img-src 'self'; font-src 'self'; media-src 'self'; connect-src 'none'; " +
	"form-action 'none'; base-uri 'self'"

func (c *Content) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		plainError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/n/"):
		c.serveNote(w, r)
	case strings.HasPrefix(r.URL.Path, "/t/"):
		c.serveAsset(w, r)
	case strings.HasPrefix(r.URL.Path, "/p/"):
		c.servePublic(w, r)
	case strings.HasPrefix(r.URL.Path, "/r/"):
		c.serveRich(w, r)
	default:
		plainError(w, http.StatusNotFound, "no such page")
	}
}

// serveNote renders one note: sanitized unless its frontmatter says
// trusted, wrapped in a document that resolves relative references to the
// note's own directory on this origin and carries the wikilink shim.
func (c *Content) serveNote(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/n/")
	w.Header().Set("Content-Security-Policy", contentCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	if !validID(id) || !c.verifyContentToken(r.URL.Query().Get("token"), id) {
		plainError(w, http.StatusUnauthorized, "this link has expired; reload the note")
		return
	}
	n, err := c.DB.GetNote(r.Context(), id)
	if errors.Is(err, index.ErrNotFound) {
		plainError(w, http.StatusNotFound, "no note with that id")
		return
	}
	if err != nil {
		plainError(w, http.StatusInternalServerError, "internal error")
		return
	}
	abs, rel, err := c.Root.Resolve(n.RelPath)
	if err != nil {
		plainError(w, http.StatusInternalServerError, "internal error")
		return
	}
	raw, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) {
		plainError(w, http.StatusNotFound, "the note's file is gone")
		return
	}
	if err != nil {
		plainError(w, http.StatusInternalServerError, "internal error")
		return
	}
	fm := frontmatter.Parse(raw)
	body := fm.Body

	links, err := c.DB.OutboundLinks(r.Context(), id)
	if err != nil {
		c.Log.Error("content origin: links unavailable", "err", err, "id", id)
		links = nil
	}
	body = annotateWikilinks(body, links)

	if !fm.Meta.Trusted {
		body = render.SanitizeHTML(body)
	}

	token := r.URL.Query().Get("token")
	base := "/t/" + token + "/f/"
	if dir := path.Dir(rel); dir != "." && dir != "/" {
		base += dir + "/"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(wrapNote(body, base))
	c.Log.Info("content served", "id", id, "path", rel, "trusted", fm.Meta.Trusted)
}

var (
	wikilinkIDRe    = regexp.MustCompile(`(?is)\s+data-wikilink-id="[^"]*"`)
	wikilinkAttrRe  = regexp.MustCompile(`(?is)data-wikilink="([^"]*)"`)
	wikilinkIDValid = regexp.MustCompile(`^[0-9A-Za-z]{26}$`)
)

// annotateWikilinks adds the server's resolution to every wikilink: the
// index's to_id for the target lands in a data-wikilink-id attribute the
// frame shim hands to the parent. It runs before sanitization, on the raw
// body, so attribute escaping is bluemonday's job afterwards.
func annotateWikilinks(body []byte, links []index.OutboundLink) []byte {
	if len(links) == 0 {
		return wikilinkIDRe.ReplaceAll(body, nil)
	}
	resolved := make(map[string]string, len(links))
	for _, l := range links {
		if l.Resolved && wikilinkIDValid.MatchString(l.ToID) {
			resolved[l.RawTarget] = l.ToID
		}
	}
	out := wikilinkIDRe.ReplaceAll(body, nil)
	s := string(out)
	for _, m := range wikilinkAttrRe.FindAllSubmatch(out, -1) {
		escaped := string(m[1])
		id, ok := resolved[unescapeAttr(escaped)]
		if !ok {
			continue
		}
		s = strings.ReplaceAll(s, `data-wikilink="`+escaped+`"`,
			`data-wikilink="`+escaped+`" data-wikilink-id="`+id+`"`)
	}
	return []byte(s)
}

func unescapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&#34;", `"`)
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&amp;", "&")
	return s
}

// wrapNote places the body in the wrapper document every note renders in.
// The base tag is what makes relative image and font references resolve
// against the note's own directory on this origin — HTML attributes and
// CSS url() alike — with the view token carried in the path so <img> tags
// (which cannot set headers) still authenticate. The shim script forwards
// wikilink clicks to the parent app; it is ours, not the note's.
func wrapNote(body []byte, base string) []byte {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<base href=\"")
	b.WriteString(base)
	b.WriteString("\">\n<script>\n")
	b.WriteString(wikilinkShim)
	b.WriteString("\n</script>\n</head>\n<body>\n")
	b.Write(body)
	b.WriteString("\n</body>\n</html>\n")
	return []byte(b.String())
}

const wikilinkShim = `(function(){
"use strict";
document.addEventListener("click", function(ev){
  var t = ev.target;
  while (t && t.nodeType === 1 && !t.hasAttribute("data-wikilink")) t = t.parentNode;
  if (!t || t.nodeType !== 1) return;
  ev.preventDefault();
  parent.postMessage({yana: "wikilink", target: t.getAttribute("data-wikilink") || "", id: t.getAttribute("data-wikilink-id") || null}, "*");
}, false);
})();`

// serveAsset answers /t/{token}/f/{path…}: the view token's note decides
// which space's assets are reachable.
func (c *Content) serveAsset(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/t/")
	token, assetPath, ok := strings.Cut(rest, "/f/")
	if !ok {
		plainError(w, http.StatusNotFound, "no such file")
		return
	}
	noteID, _, ok := strings.Cut(token, ".")
	if !ok || !validID(noteID) || !c.verifyContentToken(token, noteID) {
		plainError(w, http.StatusUnauthorized, "this link has expired; reload the note")
		return
	}
	n, err := c.DB.GetNote(r.Context(), noteID)
	if errors.Is(err, index.ErrNotFound) {
		plainError(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		plainError(w, http.StatusInternalServerError, "internal error")
		return
	}
	clean, err := c.Root.Clean(assetPath)
	if err != nil || !isAssetPath(clean) {
		plainError(w, http.StatusNotFound, "no such file")
		return
	}
	if spaceOfPath(clean) != n.Space {
		plainError(w, http.StatusForbidden, "this file is not in the note's space")
		return
	}
	abs, _, err := c.Root.Resolve(clean)
	if err != nil {
		plainError(w, http.StatusNotFound, "no such file")
		return
	}
	f, err := os.Open(abs)
	if errors.Is(err, fs.ErrNotExist) {
		plainError(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		plainError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		plainError(w, http.StatusNotFound, "no such file")
		return
	}
	if fi.Size() > c.Root.Limits().MaxAssetSize {
		plainError(w, http.StatusRequestEntityTooLarge, "file is over the asset size limit")
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "private, max-age=60")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

func plainError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintln(w, msg)
}

// hashContent is the whole-file sha256 clients send back as their base
// for last-write-wins saves.
func hashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
