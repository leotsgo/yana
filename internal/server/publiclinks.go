// Public links: a per-note switch that serves one note, read-only, at an
// unguessable URL on the content origin with no account. The static site
// export covers "publish the whole thing"; this covers "send my mother
// the recipe". The app origin holds the management routes (create,
// read, change the expiry, revoke, list, revoke all), every one behind
// membership of the note's space; the content origin serves the pages
// and the pictures beside them.
//
// The token is not stored. It is derived from the link's row id with
// the content secret (HMAC-SHA256, 32 bytes, as unguessable as the
// secret), and only its SHA-256 is kept in the row — so a copy of the
// index alone opens nothing, while the app can show the same URL again
// whenever a member asks. Revoked, expired, and never-existed links all
// answer 404 with the same body.
package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/export"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// publicLinkRate is the requests one link may make per minute, page and
// pictures together: enough for a household reading a note with many
// images, not enough to make a link a public file server.
const publicLinkRate = 300

var publicLinkPurpose = []byte("yana-public-link\x00")

// PublicToken derives the token of a link from its row id.
func (c *Content) PublicToken(linkID string) string {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write(publicLinkPurpose)
	mac.Write([]byte(linkID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// publicTokenHash is the stored form of a link's token, for a lookup.
func (c *Content) publicTokenHash(linkID string) string {
	return index.HashPublicToken(c.PublicToken(linkID))
}

// publicPageCSP is the policy on a public page: the runtimes and the
// note's pictures from this origin, pictures the note hotlinks over
// https (as an export would show them), nothing else.
const publicPageCSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https:; font-src 'self'; media-src 'self' https:; connect-src 'none'; " +
	"form-action 'none'; base-uri 'none'; frame-ancestors 'none'"

// publicHeaders are on every answer under /p/: not indexed, not cached
// (a revoked link is gone at once), no referrer, no cookies ever.
func publicHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
}

// publicGone is the one answer for a link that does not open: missing,
// revoked, expired, or a note that is gone.
func publicGone(w http.ResponseWriter) {
	plainError(w, http.StatusNotFound, "there is no note at this address")
}

// servePublic answers /p/{token} and /p/{token}/f/{name}?at=….
func (c *Content) servePublic(w http.ResponseWriter, r *http.Request) {
	publicHeaders(w)
	rest := strings.TrimPrefix(r.URL.Path, "/p/")
	token, tail, _ := strings.Cut(rest, "/")
	if token == "" || len(token) > 64 {
		publicGone(w)
		return
	}
	hash := index.HashPublicToken(token)
	link, n, err := c.DB.PublicLinkByHash(r.Context(), hash, c.Now())
	if errors.Is(err, index.ErrPublicLinkNotFound) {
		publicGone(w)
		return
	}
	if err != nil {
		plainError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !c.linkRate.Allow("link:" + hash) {
		w.Header().Set("Retry-After", "60")
		plainError(w, http.StatusTooManyRequests, "this link is being read too often; try again in a minute")
		return
	}
	switch {
	case tail == "":
		c.servePublicPage(w, r, link, n, token)
	case strings.HasPrefix(tail, "f/"):
		c.servePublicAsset(w, r, n, tail)
	default:
		publicGone(w)
	}
}

// servePublicPage renders the note for a link. Every reference beside
// the note goes through the same token; a wikilink links only when its
// target has a live link of its own.
func (c *Content) servePublicPage(w http.ResponseWriter, r *http.Request, link index.PublicLink, n index.Note, token string) {
	deps := &export.Deps{DB: c.DB, Root: c.Root, Rich: c.Rich}
	refs := export.PublicRefs{
		Asset: func(rel string) string {
			clean, ok := c.publicAssetPath(n, rel)
			if !ok {
				return ""
			}
			return "/p/" + token + "/f/" + url.PathEscape(path.Base(clean)) + "?at=" + url.QueryEscape(rel)
		},
		Note: func(id string) string {
			other, err := c.DB.PublicLinkForNote(r.Context(), id, c.Now())
			if err != nil {
				return ""
			}
			return "/p/" + c.PublicToken(other.ID)
		},
		Rich: func(name string) string {
			if c.Rich == nil {
				return ""
			}
			return "/r/" + name
		},
	}
	page, err := deps.PublicPage(r.Context(), n, refs)
	if errors.Is(err, index.ErrNotFound) {
		publicGone(w)
		return
	}
	if err != nil {
		c.Log.Error("public page failed", "err", err, "link", link.ID)
		plainError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Security-Policy", publicPageCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
	c.Log.Info("public page served", "link", link.ID, "id", n.ID)
}

// publicAssetPath resolves a reference relative to the note's directory
// to a clean tree path, accepting only a file under an _assets directory
// of the note's own space.
func (c *Content) publicAssetPath(n index.Note, rel string) (string, bool) {
	rel = strings.TrimPrefix(rel, "./")
	if i := strings.IndexAny(rel, "?#"); i >= 0 {
		rel = rel[:i]
	}
	if rel == "" {
		return "", false
	}
	// A reference may climb ("../_assets/logo.svg"); resolve it against
	// the note's directory before the tree's own checks, which take no
	// ".." at all. Anything that climbs out of the tree fails here.
	joined := path.Clean(path.Join(path.Dir(n.RelPath), rel))
	if joined == ".." || strings.HasPrefix(joined, "../") || strings.HasPrefix(rel, "/") {
		return "", false
	}
	clean, err := c.Root.Clean(joined)
	if err != nil || !isAssetPath(clean) || spaceOfPath(clean) != n.Space {
		return "", false
	}
	return clean, true
}

// servePublicAsset answers /p/{token}/f/{name}?at={rel}: the file the
// note refers to as rel, from its own directory. The name in the path
// is for the browser; at is what is served.
func (c *Content) servePublicAsset(w http.ResponseWriter, r *http.Request, n index.Note, tail string) {
	clean, ok := c.publicAssetPath(n, r.URL.Query().Get("at"))
	if !ok || strings.Contains(strings.TrimPrefix(tail, "f/"), "/") {
		publicGone(w)
		return
	}
	abs, _, err := c.Root.Resolve(clean)
	if err != nil {
		publicGone(w)
		return
	}
	f, err := os.Open(abs)
	if errors.Is(err, fs.ErrNotExist) {
		publicGone(w)
		return
	}
	if err != nil {
		plainError(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		publicGone(w)
		return
	}
	if fi.Size() > c.Root.Limits().MaxAssetSize {
		plainError(w, http.StatusRequestEntityTooLarge, "file is over the asset size limit")
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": fi.Name()}))
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// richFiles maps the runtime routes to the files the web build lays out.
var richFiles = map[string]string{
	"mermaid.js": "mermaid.js",
	"katex.js":   "katex.js",
	"katex.css":  "katex-style.css",
}

// serveRich answers /r/{name}: the diagram and math runtimes a public
// page links, and the fonts the KaTeX stylesheet refers to beside it.
// They are the same bytes for everyone, so they cache.
func (c *Content) serveRich(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/r/")
	file, ok := richFiles[name]
	if !ok && strings.HasPrefix(name, "katex-fonts/") && !strings.Contains(name[len("katex-fonts/"):], "/") {
		file, ok = name, true
	}
	if !ok || c.Rich == nil {
		plainError(w, http.StatusNotFound, "no such file")
		return
	}
	data, err := fs.ReadFile(c.Rich, file)
	if err != nil {
		plainError(w, http.StatusNotFound, "no such file")
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

// --- the app origin: managing links ---------------------------------------

// publicLinkOut is a link as the API shows it to a member.
type publicLinkOut struct {
	ID        string     `json:"id"`
	NoteID    string     `json:"note_id"`
	URL       string     `json:"url"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
}

func (s *Server) publicLinkOut(r *http.Request, l index.PublicLink) publicLinkOut {
	return publicLinkOut{
		ID: l.ID, NoteID: l.NoteID,
		URL:       s.contentBase(r) + "/p/" + s.Content.PublicToken(l.ID),
		CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt,
	}
}

// publicExpiry turns the API's expiry choice into a time: "1d", "1w",
// or "never" (and "", which is never).
func publicExpiry(choice string, now time.Time) (*time.Time, bool) {
	switch choice {
	case "", "never":
		return nil, true
	case "1d":
		t := now.Add(24 * time.Hour).UTC()
		return &t, true
	case "1w":
		t := now.Add(7 * 24 * time.Hour).UTC()
		return &t, true
	}
	return nil, false
}

// publicLinkNote is the membership gate every link route shares: the
// note must exist and the caller must belong to its space.
func (s *Server) publicLinkNote(w http.ResponseWriter, r *http.Request) (index.Note, bool) {
	id := r.PathValue("id")
	if _, ok := s.noteAuthz(w, r, id); !ok {
		return index.Note{}, false
	}
	if s.Content == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the content origin; set YANA_CONTENT_LISTEN")
		return index.Note{}, false
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if errors.Is(err, index.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no note with that id")
		return index.Note{}, false
	}
	if err != nil {
		s.fail(w, r, err)
		return index.Note{}, false
	}
	return n, true
}

// handlePublicLinkGet answers with the note's live link, or null.
func (s *Server) handlePublicLinkGet(w http.ResponseWriter, r *http.Request) {
	n, ok := s.publicLinkNote(w, r)
	if !ok {
		return
	}
	l, err := s.DB.PublicLinkForNote(r.Context(), n.ID, time.Now())
	if errors.Is(err, index.ErrPublicLinkNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"link": nil})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"link": s.publicLinkOut(r, l)})
}

// handlePublicLinkCreate makes the note's link, or returns the one it
// has: {expires: "1d" | "1w" | "never"} applies to a new link only.
func (s *Server) handlePublicLinkCreate(w http.ResponseWriter, r *http.Request) {
	n, ok := s.publicLinkNote(w, r)
	if !ok {
		return
	}
	var in struct {
		Expires string `json:"expires"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeError(w, http.StatusBadRequest, "body must be JSON")
			return
		}
	}
	now := time.Now().UTC()
	exp, ok := publicExpiry(in.Expires, now)
	if !ok {
		writeError(w, http.StatusBadRequest, "expires must be 1d, 1w, or never")
		return
	}
	l := index.PublicLink{ID: scanner.NewID(now), NoteID: n.ID, CreatedBy: s.ident(r).UserID, CreatedAt: now, ExpiresAt: exp}
	out, created, err := s.DB.CreatePublicLink(r.Context(), l, s.Content.publicTokenHash, now)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		loggerFrom(r.Context()).Info("public link created", "id", n.ID, "link", out.ID, "expires", exp)
	}
	writeJSON(w, status, map[string]any{"link": s.publicLinkOut(r, out), "created": created})
}

// handlePublicLinkUpdate changes when the note's live link expires.
func (s *Server) handlePublicLinkUpdate(w http.ResponseWriter, r *http.Request) {
	n, ok := s.publicLinkNote(w, r)
	if !ok {
		return
	}
	var in struct {
		Expires string `json:"expires"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	now := time.Now().UTC()
	exp, ok := publicExpiry(in.Expires, now)
	if !ok {
		writeError(w, http.StatusBadRequest, "expires must be 1d, 1w, or never")
		return
	}
	l, err := s.DB.SetPublicLinkExpiry(r.Context(), n.ID, exp, now)
	if errors.Is(err, index.ErrPublicLinkNotFound) {
		writeError(w, http.StatusNotFound, "this note has no live link")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"link": s.publicLinkOut(r, l)})
}

// handlePublicLinkRevoke retires the note's link. Revoking a note with
// no link succeeds: there is no link either way.
func (s *Server) handlePublicLinkRevoke(w http.ResponseWriter, r *http.Request) {
	n, ok := s.publicLinkNote(w, r)
	if !ok {
		return
	}
	revoked, err := s.DB.RevokePublicLink(r.Context(), n.ID, time.Now().UTC())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if revoked {
		loggerFrom(r.Context()).Info("public link revoked", "id", n.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revoked": revoked})
}

// publicLinkSpaces is the space filter for listings: nil for everything
// (the owner, or a server without accounts), else the caller's spaces.
func (s *Server) publicLinkSpaces(r *http.Request) ([]string, error) {
	if s.open() {
		return nil, nil
	}
	member, all, err := s.Auth.MemberSpaces(r.Context(), s.ident(r))
	if err != nil {
		return nil, err
	}
	if all {
		return nil, nil
	}
	if member == nil {
		member = []string{}
	}
	return member, nil
}

// publicLinkRow is one row of the Data page's list.
type publicLinkRow struct {
	publicLinkOut
	Title string `json:"title"`
	Path  string `json:"path"`
	Space string `json:"space"`
}

// handlePublicLinks lists every live link in the caller's spaces.
func (s *Server) handlePublicLinks(w http.ResponseWriter, r *http.Request) {
	if s.Content == nil {
		writeJSON(w, http.StatusOK, map[string]any{"links": []publicLinkRow{}})
		return
	}
	spaces, err := s.publicLinkSpaces(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.DB.ListPublicLinks(r.Context(), spaces, time.Now())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]publicLinkRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, publicLinkRow{
			publicLinkOut: s.publicLinkOut(r, row.PublicLink),
			Title:         row.Note.Title, Path: row.Note.RelPath, Space: row.Note.Space,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": out})
}

// handlePublicLinksRevokeAll retires every live link in the caller's
// spaces.
func (s *Server) handlePublicLinksRevokeAll(w http.ResponseWriter, r *http.Request) {
	spaces, err := s.publicLinkSpaces(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	n, err := s.DB.RevokePublicLinksIn(r.Context(), spaces, time.Now().UTC())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	loggerFrom(r.Context()).Info("public links revoked", "count", n)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "revoked": n})
}

// isPublic reports whether a note has a live link, for the note page's
// globe; a lookup failure reads as no link.
func (s *Server) isPublic(r *http.Request, id string) bool {
	if s.Content == nil {
		return false
	}
	_, err := s.DB.PublicLinkForNote(r.Context(), id, time.Now())
	return err == nil
}
