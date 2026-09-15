// Package server is the HTTP surface: the JSON API, health endpoints, and
// the embedded web client.
package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/madeofpendletonwool/yana/internal/auth"
	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/render"
	"github.com/madeofpendletonwool/yana/internal/rt"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/search"
	"github.com/madeofpendletonwool/yana/internal/spaces"
)

// Deps are everything the handlers need.
type Deps struct {
	DB      *index.DB
	Root    *pathsafe.Root
	Ripgrep *search.Ripgrep
	Web     fs.FS // embedded client; nil serves a plain message at /
	Log     *slog.Logger
	Version string
	// Sync is the reconciliation loop; nil when the server runs without one.
	Sync *reconcile.Reconciler
	// RT is the realtime relay mounted at GET /ws; nil disables the endpoint.
	RT *rt.Hub
	// Scanner indexes new files (note creation); nil skips the immediate
	// index pass.
	Scanner *scanner.Scanner
	// Git is the history layer; nil (no git binary, git disabled) turns
	// the history endpoints into 501s.
	Git *git.Layer
	// Auth is the accounts service. When set, every /api route except
	// the sign-in endpoints requires a verified identity and answers
	// only within the caller's spaces. nil runs the server without
	// accounts (the pre-Phase-4 state, tests, or a private deployment).
	Auth *auth.Service
	// MCP is the agent tool endpoint (POST /mcp). nil leaves it
	// unmounted; the handler itself decides what it needs.
	MCP http.Handler
	// Content is the handler for the separate origin HTML notes render
	// on. nil disables the view endpoint (501) and the second listener.
	Content *Content
	// ContentOrigin overrides the public base URL of the content origin
	// (set it when a proxy maps a subdomain onto the content listener).
	ContentOrigin string
	// ContentAddr is the content listener's address; its port feeds the
	// derived view URLs when ContentOrigin is empty.
	ContentAddr string
	// SearchJS is the bundled client-side search runtime embedded in
	// static site exports; nil exports sites without a search page.
	SearchJS []byte
	// CanWrite decides whether a request may change a space. nil allows
	// everything; when Auth is set the role check below runs instead.
	CanWrite func(r *http.Request, space string) error
	// Daily is the daily note's path pattern and template; zero values
	// fall back to DefaultDaily.
	Daily DailyConfig
}

// Server holds handler state.
type Server struct {
	Deps
	ready atomic.Bool
	mux   *http.ServeMux
}

// New wires the routes. Call SetReady once the first scan has finished.
func New(d Deps) *Server {
	if d.Log == nil {
		d.Log = slog.Default()
	}
	d.Log = d.Log.With("component", "http")
	s := &Server{Deps: d, mux: http.NewServeMux()}
	s.routes()
	return s
}

// SetReady flips /readyz to 200.
func (s *Server) SetReady(v bool) { s.ready.Store(v) }

// ServeHTTP implements http.Handler with request-id, logging and recovery
// middleware around the mux.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	reqID := r.Header.Get("X-Request-Id")
	if reqID == "" || len(reqID) > 64 {
		reqID = newRequestID()
	}
	w.Header().Set("X-Request-Id", reqID)
	log := s.Log.With("request_id", reqID)
	r = r.WithContext(withLogger(r.Context(), log))
	rw := &statusWriter{ResponseWriter: w, status: 200}
	defer func() {
		if p := recover(); p != nil {
			log.Error("handler panicked", "panic", p, "method", r.Method, "path", r.URL.Path)
			if !rw.wrote {
				writeError(rw, http.StatusInternalServerError, "internal error")
			}
		}
		log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rw.status,
			"bytes", rw.bytes, "duration", time.Since(start))
	}()
	s.mux.ServeHTTP(rw, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	// The sign-in endpoints stand outside the identity gate.
	s.mux.HandleFunc("GET /api/auth/state", s.handleAuthState)
	s.mux.HandleFunc("POST /api/auth/setup", s.handleSetup)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/refresh", s.handleRefresh)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/auth/sessions", s.authed(s.handleSessions))
	s.mux.HandleFunc("DELETE /api/auth/sessions/{id}", s.authed(s.handleSessionRevoke))
	s.mux.HandleFunc("GET /api/users", s.authed(s.handleUsers))
	s.mux.HandleFunc("POST /api/users", s.authed(s.handleUserCreate))
	s.mux.HandleFunc("DELETE /api/users/{id}", s.authed(s.handleUserDelete))
	s.mux.HandleFunc("POST /api/users/{id}/password", s.authed(s.handleUserPassword))
	s.mux.HandleFunc("GET /api/spaces", s.authed(s.handleSpaces))
	s.mux.HandleFunc("POST /api/spaces", s.authed(s.handleSpaceCreate))
	s.mux.HandleFunc("GET /api/spaces/{space}", s.authed(s.handleSpaceGet))
	s.mux.HandleFunc("PATCH /api/spaces/{space}", s.authed(s.handleSpaceUpdate))
	s.mux.HandleFunc("DELETE /api/spaces/{space}", s.authed(s.handleSpaceDelete))
	s.mux.HandleFunc("GET /api/tree", s.authed(s.handleTree))
	s.mux.HandleFunc("GET /api/notes/{id}", s.authed(s.handleNote))
	s.mux.HandleFunc("GET /api/search", s.authed(s.handleSearch))
	s.mux.HandleFunc("GET /api/files/{path...}", s.authed(s.handleFile))
	s.mux.HandleFunc("PUT /api/files/{path...}", s.authed(s.handleFileUpload))
	s.mux.HandleFunc("POST /api/notes/daily", s.authed(s.handleDailyNote))
	s.mux.HandleFunc("POST /api/render", s.authed(s.handleRender))
	s.mux.HandleFunc("GET /api/notes/{id}/backlinks", s.authed(s.handleBacklinks))
	s.mux.HandleFunc("GET /api/links/unresolved", s.authed(s.handleUnresolvedLinks))
	s.mux.HandleFunc("POST /api/notes/{id}/move", s.authed(s.handleMove))
	s.mux.HandleFunc("DELETE /api/notes/{id}", s.authed(s.handleDeleteNote))
	s.mux.HandleFunc("GET /api/trash", s.authed(s.handleTrash))
	s.mux.HandleFunc("POST /api/trash/{id}/restore", s.authed(s.handleTrashRestore))
	s.mux.HandleFunc("DELETE /api/trash/{id}", s.authed(s.handleTrashDestroy))
	s.mux.HandleFunc("POST /api/trash/empty", s.authed(s.handleTrashEmpty))
	s.mux.HandleFunc("POST /api/notes", s.authed(s.handleCreateNote))
	s.mux.HandleFunc("GET /api/notes/{id}/view", s.authed(s.handleNoteView))
	s.mux.HandleFunc("PUT /api/notes/{id}/source", s.authed(s.handleNoteSource))
	s.mux.HandleFunc("POST /api/notes/{id}/trust", s.authed(s.handleNoteTrust))
	s.mux.HandleFunc("POST /api/spaces/{space}/conventions", s.authed(s.handleConventions))
	s.mux.HandleFunc("GET /api/notes/{id}/history", s.authed(s.handleNoteHistory))
	s.mux.HandleFunc("GET /api/notes/{id}/history/diff", s.authed(s.handleNoteHistoryDiff))
	s.mux.HandleFunc("POST /api/notes/{id}/history/restore", s.authed(s.handleNoteHistoryRestore))
	s.mux.HandleFunc("POST /api/git/snapshot", s.authed(s.handleGitSnapshot))
	s.mux.HandleFunc("GET /api/status", s.authed(s.handleStatus))
	s.mux.HandleFunc("GET /api/notes/{id}/export.html", s.authed(s.handleExportNote))
	s.mux.HandleFunc("GET /api/spaces/{space}/export/site.zip", s.authed(s.handleExportSite))
	s.mux.HandleFunc("GET /api/spaces/{space}/export/notes.zip", s.authed(s.handleExportTree))
	if s.Auth != nil {
		// Agent tokens are accounts-adjacent: they exist only when the
		// account world does, and only the owner manages them.
		s.mux.HandleFunc("GET /api/agents", s.authed(s.handleAgents))
		s.mux.HandleFunc("POST /api/agents", s.authed(s.handleAgentCreate))
		s.mux.HandleFunc("DELETE /api/agents/{id}", s.authed(s.handleAgentDelete))
	}
	if s.MCP != nil {
		s.mux.Handle("/mcp", s.MCP)
	}
	if s.RT != nil {
		s.mux.Handle("GET /ws", s.RT)
	}
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	s.mux.HandleFunc("/", s.handleWeb)
}

// --- health ------------------------------------------------------------

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !s.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "reason": "initial scan has not finished"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	notes, assets, err := s.DB.Counts(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	last, _ := s.DB.ScanState(r.Context(), "last_scan")
	daily := s.daily()
	status := map[string]any{
		"version":      s.Version,
		"ready":        s.ready.Load(),
		"notes":        notes,
		"assets":       assets,
		"last_scan":    last,
		"regex_search": s.Ripgrep != nil && s.Ripgrep.Available(),
		"daily":        map[string]string{"pattern": daily.Pattern, "template": daily.Template},
	}
	if s.Sync != nil {
		status["sync"] = s.Sync.Stats()
	}
	if s.RT != nil {
		status["realtime"] = s.RT.Stats()
	}
	if s.Git != nil {
		status["git"] = s.Git.Stats()
	}
	writeJSON(w, http.StatusOK, status)
}

// --- api ---------------------------------------------------------------

// noteAuthz checks the identity may see one note. A note the caller is
// not a member of looks exactly like a missing one; errors are already
// written and reported via ok=false.
func (s *Server) noteAuthz(w http.ResponseWriter, r *http.Request, id string) (space string, ok bool) {
	if !validID(id) {
		writeError(w, http.StatusBadRequest, "note id must be a 26-character ULID")
		return "", false
	}
	if s.open() {
		return "", true
	}
	space, _, err := s.Auth.AuthorizeNote(r.Context(), s.ident(r), id)
	if errors.Is(err, auth.ErrForbidden) || errors.Is(err, index.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no note with that id")
		return "", false
	}
	if err != nil {
		s.fail(w, r, err)
		return "", false
	}
	return space, true
}

// spaceAuthz checks the identity's role in one space requested by
// parameter. An unknown or non-member space reads as missing.
func (s *Server) spaceAuthz(w http.ResponseWriter, r *http.Request, space string) (role string, ok bool) {
	if s.open() {
		return spaces.RoleOwner, true
	}
	role, err := s.Auth.AuthorizeSpace(r.Context(), s.ident(r), space)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such space")
		return "", false
	}
	return role, true
}

// mayWrite reports whether the request may change a space.
func (s *Server) mayWrite(w http.ResponseWriter, r *http.Request, space string) bool {
	if s.Auth != nil {
		if err := s.Auth.CanWriteSpace(r.Context(), s.ident(r), space); err != nil {
			if errors.Is(err, auth.ErrForbidden) {
				writeError(w, http.StatusNotFound, "no such space")
			} else {
				writeError(w, http.StatusForbidden, err.Error())
			}
			return false
		}
		return true
	}
	if s.CanWrite != nil {
		if err := s.CanWrite(r, space); err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return false
		}
	}
	return true
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	space, ok := s.spaceParam(w, r)
	if !ok {
		return
	}
	if space != "" {
		if _, ok := s.spaceAuthz(w, r, space); !ok {
			return
		}
	}
	notes, err := s.DB.ListNotes(r.Context(), space)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	notes = s.visibleNotes(r, notes)
	tree := buildTree(notes)
	if tree == nil {
		tree = []SpaceTree{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"spaces": tree})
}

// visibleNotes drops notes outside the identity's spaces.
func (s *Server) visibleNotes(r *http.Request, notes []index.Note) []index.Note {
	if s.open() {
		return notes
	}
	id := s.ident(r)
	member, isAll, err := s.Auth.MemberSpaces(r.Context(), id)
	if err != nil {
		return nil
	}
	if isAll {
		return notes
	}
	allowed := make(map[string]bool, len(member))
	for _, sp := range member {
		allowed[sp] = true
	}
	out := notes[:0]
	for _, n := range notes {
		if allowed[n.Space] {
			out = append(out, n)
		}
	}
	return out
}

// NoteResponse is the payload for one note.
type NoteResponse struct {
	index.Note
	Tags     []string             `json:"tags"`
	Base     string               `json:"base"` // directory of the note, for relative links
	Links    []index.OutboundLink `json:"links"`
	HTML     string               `json:"html,omitempty"`
	Markdown string               `json:"markdown,omitempty"`
	Source   string               `json:"source,omitempty"` // html notes: raw source
}

func (s *Server) handleNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.noteAuthz(w, r, id); !ok {
		return
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if errors.Is(err, index.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no note with that id")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	abs, _, err := s.Root.Resolve(n.RelPath)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	raw, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "the note's file is gone; the index will catch up on the next scan")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tags, err := s.DB.Tags(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tags == nil {
		tags = []string{}
	}
	links, err := s.DB.OutboundLinks(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if links == nil {
		links = []index.OutboundLink{}
	}
	resp := NoteResponse{Note: n, Tags: tags, Links: links, Base: path.Dir(n.RelPath)}
	if resp.Base == "." {
		resp.Base = ""
	}
	switch n.Kind {
	case "md":
		body := bodyOf(raw)
		html, err := render.Markdown(body)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		resp.HTML = string(html)
		resp.Markdown = string(raw)
	case "html":
		// Rendering happens on the separate content origin (see
		// content.go); the API hands the editor the raw source.
		resp.Source = string(bodyOf(raw))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	space, ok := s.spaceParam(w, r)
	if !ok {
		return
	}
	// Search never crosses a space boundary the caller cannot see: the
	// result set is restricted to the caller's member spaces (or the
	// one requested space, after a membership check).
	var allowed []string
	if s.open() {
		allowed = nil // unrestricted
	} else {
		if space != "" {
			if _, ok := s.spaceAuthz(w, r, space); !ok {
				return
			}
			allowed = []string{space}
		} else {
			member, isAll, err := s.Auth.MemberSpaces(r.Context(), s.ident(r))
			if err != nil {
				s.fail(w, r, err)
				return
			}
			if isAll {
				allowed = nil
			} else {
				allowed = member
				if len(allowed) == 0 {
					allowed = []string{""} // matches nothing
				}
			}
		}
	}
	limit := 50
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "limit must be a number from 1 to 200")
			return
		}
		limit = n
	}
	if raw := q.Get("raw"); raw != "" {
		if len(raw) > 512 {
			writeError(w, http.StatusBadRequest, "regex is longer than 512 characters")
			return
		}
		matches, err := s.Ripgrep.SearchSpaces(r.Context(), raw, space, allowed, limit)
		switch {
		case errors.Is(err, search.ErrUnavailable):
			writeError(w, http.StatusNotImplemented, err.Error())
			return
		case errors.Is(err, search.ErrBadPattern):
			writeError(w, http.StatusBadRequest, err.Error())
			return
		case errors.Is(err, context.DeadlineExceeded):
			writeError(w, http.StatusGatewayTimeout, "regex search took too long and was stopped")
			return
		case err != nil:
			s.fail(w, r, err)
			return
		}
		type regexHit struct {
			search.RegexMatch
			ID    string `json:"id,omitempty"`
			Title string `json:"title,omitempty"`
		}
		hits := make([]regexHit, 0, len(matches))
		for _, m := range matches {
			// The search itself already ran inside the allowed space
			// directories; enrichment only adds ids and titles.
			h := regexHit{RegexMatch: m}
			if n, err := s.DB.GetNoteByPath(r.Context(), m.Path); err == nil {
				h.ID, h.Title = n.ID, n.Title
			}
			hits = append(hits, h)
		}
		writeJSON(w, http.StatusOK, map[string]any{"mode": "regex", "hits": hits})
		return
	}
	query := strings.TrimSpace(q.Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q (full text) or raw (regex) is required")
		return
	}
	if len(query) > 512 {
		writeError(w, http.StatusBadRequest, "query is longer than 512 characters")
		return
	}
	hits, err := s.DB.Search(r.Context(), query, space, allowed, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if hits == nil {
		hits = []index.SearchHit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"mode": "fts", "hits": hits})
}

// handleFile serves files from _assets directories so rendered notes can
// show their images. Notes themselves are not served raw here; the API
// returns them with their metadata.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	abs, rel, err := s.Root.Resolve(r.PathValue("path"))
	if err != nil {
		if pathsafe.IsRejection(err) {
			writeError(w, http.StatusBadRequest, "that path is not allowed")
			return
		}
		s.fail(w, r, err)
		return
	}
	if !isAssetPath(rel) {
		writeError(w, http.StatusNotFound, "only files under an _assets directory are served")
		return
	}
	if _, ok := s.spaceAuthz(w, r, spaceOfPath(rel)); !ok {
		return
	}
	f, err := os.Open(abs)
	if errors.Is(err, fs.ErrNotExist) {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

func isAssetPath(rel string) bool {
	for _, seg := range strings.Split(path.Dir(rel), "/") {
		if seg == "_assets" {
			return true
		}
	}
	return false
}

// --- web ---------------------------------------------------------------

func (s *Server) handleWeb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.Web == nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("YANA/ is running, but this build has no web client. The API is under /api/.\n"))
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	if f, err := s.Web.Open(p); err == nil {
		if fi, err := f.Stat(); err == nil && !fi.IsDir() {
			f.Close()
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.Web, p)
			return
		}
		f.Close()
	}
	// Client-side routes fall back to the app shell.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, s.Web, "index.html")
}

// --- helpers -----------------------------------------------------------

func (s *Server) spaceParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	space := r.URL.Query().Get("space")
	if space == "" {
		return "", true
	}
	clean, err := s.Root.Clean(space)
	if err != nil || strings.Contains(clean, "/") || clean == "" {
		writeError(w, http.StatusBadRequest, "space must be a single directory name")
		return "", false
	}
	return clean, true
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	loggerFrom(r.Context()).Error("request failed", "err", err, "path", r.URL.Path)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func validID(id string) bool {
	if len(id) != 26 {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}
	return true
}

// bodyOf strips a frontmatter block, if present.
func bodyOf(raw []byte) []byte {
	return frontmatter.Parse(raw).Body
}

func newRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.wrote = true
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Hijack forwards to the wrapped writer so WebSocket upgrades pass through
// the logging middleware.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return h.Hijack()
}

type ctxKey struct{}

func withLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func loggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
