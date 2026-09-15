// The export endpoints: the escape hatch that makes invariant #6 real.
// Each is a download — one note as a self-contained HTML file, a space
// or subtree as a static site, and the markdown and assets as a
// byte-identical zip. Exports are reads: any member of the space can
// take one, and nothing outside the caller's spaces is ever written into
// one.
package server

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/export"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
)

// exporter builds the export dependencies for one request.
func (s *Server) exporter() *export.Deps {
	return &export.Deps{DB: s.DB, Root: s.Root, SearchJS: s.SearchJS}
}

// handleExportNote serves one note as a single self-contained HTML file.
func (s *Server) handleExportNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.noteAuthz(w, r, id); !ok {
		return
	}
	page, err := s.exporter().SingleNote(r.Context(), id)
	switch {
	case errors.Is(err, index.ErrNotFound):
		writeError(w, http.StatusNotFound, "no note with that id")
		return
	case err != nil:
		s.fail(w, r, err)
		return
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+slug(n.Title)+".html\"")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page)
}

// handleExportSite serves a space or subtree as a static site zip.
// ?path= scopes the site to a subtree of the space.
func (s *Server) handleExportSite(w http.ResponseWriter, r *http.Request) {
	space, subtree, ok := s.exportScope(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+slug(space)+"-site.zip\"")
	w.WriteHeader(http.StatusOK)
	stats, err := s.exporter().SiteZip(r.Context(), space, subtree, w)
	if err != nil {
		loggerFrom(r.Context()).Error("site export failed", "err", err, "space", space, "path", subtree, "pages", stats.Pages)
	}
}

// handleExportTree serves a space or subtree as a byte-identical zip of
// the markdown and assets. ?path= scopes it to a subtree.
func (s *Server) handleExportTree(w http.ResponseWriter, r *http.Request) {
	space, subtree, ok := s.exportScope(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+slug(space)+".zip\"")
	w.WriteHeader(http.StatusOK)
	if _, err := s.exporter().TreeZip(r.Context(), space, subtree, w); err != nil {
		loggerFrom(r.Context()).Error("tree export failed", "err", err, "space", space, "path", subtree)
	}
}

// exportScope validates the space parameter and the optional ?path=
// subtree, applying the space membership check.
func (s *Server) exportScope(w http.ResponseWriter, r *http.Request) (space, subtree string, ok bool) {
	space, ok = s.spacePath(w, r)
	if !ok {
		return "", "", false
	}
	if _, ok := s.spaceAuthz(w, r, space); !ok {
		return "", "", false
	}
	subtree = r.URL.Query().Get("path")
	if subtree != "" {
		clean, err := s.Root.Clean(subtree)
		if err != nil || strings.HasPrefix(clean, "..") {
			if pathsafe.IsRejection(err) {
				writeError(w, http.StatusBadRequest, "that path is not allowed")
				return "", "", false
			}
			writeError(w, http.StatusBadRequest, "path must be a directory inside the space")
			return "", "", false
		}
		subtree = clean
	}
	return space, subtree, true
}

var slugRe = regexp.MustCompile(`[^a-z0-9._-]+`)

// slug flattens a title or space name into a safe download filename.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		return "export"
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}
