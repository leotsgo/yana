// Package server is the HTTP surface: the JSON API, health endpoints, and
// the embedded web client.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/render"
	"github.com/madeofpendletonwool/yana/internal/search"
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
	s.mux.HandleFunc("GET /api/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/spaces", s.handleSpaces)
	s.mux.HandleFunc("GET /api/tree", s.handleTree)
	s.mux.HandleFunc("GET /api/notes/{id}", s.handleNote)
	s.mux.HandleFunc("GET /api/search", s.handleSearch)
	s.mux.HandleFunc("GET /api/files/{path...}", s.handleFile)
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
	status := map[string]any{
		"version":      s.Version,
		"ready":        s.ready.Load(),
		"notes":        notes,
		"assets":       assets,
		"last_scan":    last,
		"regex_search": s.Ripgrep != nil && s.Ripgrep.Available(),
	}
	if s.Sync != nil {
		status["sync"] = s.Sync.Stats()
	}
	writeJSON(w, http.StatusOK, status)
}

// --- api ---------------------------------------------------------------

func (s *Server) handleSpaces(w http.ResponseWriter, r *http.Request) {
	spaces, err := s.DB.Spaces(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if spaces == nil {
		spaces = []index.SpaceInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"spaces": spaces})
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	space, ok := s.spaceParam(w, r)
	if !ok {
		return
	}
	notes, err := s.DB.ListNotes(r.Context(), space)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tree := buildTree(notes)
	if tree == nil {
		tree = []SpaceTree{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"spaces": tree})
}

// NoteResponse is the payload for one note.
type NoteResponse struct {
	index.Note
	Tags     []string `json:"tags"`
	Base     string   `json:"base"` // directory of the note, for relative links
	HTML     string   `json:"html,omitempty"`
	Markdown string   `json:"markdown,omitempty"`
	Source   string   `json:"source,omitempty"` // html notes: raw source
}

func (s *Server) handleNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID(id) {
		writeError(w, http.StatusBadRequest, "note id must be a 26-character ULID")
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
	resp := NoteResponse{Note: n, Tags: tags, Base: path.Dir(n.RelPath)}
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
		// Rendering HTML notes needs the separate content origin from
		// Phase 9. Until then the client shows the source.
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
		matches, err := s.Ripgrep.Search(r.Context(), raw, space, limit)
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
	hits, err := s.DB.Search(r.Context(), query, space, limit)
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
