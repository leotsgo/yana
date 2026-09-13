// Link- and move-related endpoints: backlinks, the unresolved-link
// report, note creation from an unresolved link, and rename propagation.
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// canWrite adapts the request-scoped permission hook to the reconciler's
// space check. nil (until Phase 4 wires real roles) allows everything.
func (s *Server) canWrite(r *http.Request) reconcile.CanWrite {
	if s.CanWrite == nil {
		return nil
	}
	return func(space string) error { return s.CanWrite(r, space) }
}

func (s *Server) handleBacklinks(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID(id) {
		writeError(w, http.StatusBadRequest, "note id must be a 26-character ULID")
		return
	}
	back, err := s.DB.Backlinks(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if back == nil {
		back = []index.Backlink{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"backlinks": back})
}

func (s *Server) handleUnresolvedLinks(w http.ResponseWriter, r *http.Request) {
	space, ok := s.spaceParam(w, r)
	if !ok {
		return
	}
	un, err := s.DB.UnresolvedLinks(r.Context(), space)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if un == nil {
		un = []index.UnresolvedLink{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"unresolved": un})
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID(id) {
		writeError(w, http.StatusBadRequest, "note id must be a 26-character ULID")
		return
	}
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the reconciliation loop")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a path field")
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	res, err := s.Sync.Move(r.Context(), id, body.Path, s.canWrite(r))
	switch {
	case err == nil:
	case errors.Is(err, reconcile.ErrNotFound):
		writeError(w, http.StatusNotFound, "no note with that id")
		return
	case errors.Is(err, reconcile.ErrMoveSamePath):
		writeError(w, http.StatusBadRequest, "the note is already at that path")
		return
	case errors.Is(err, reconcile.ErrMoveTargetTaken):
		writeError(w, http.StatusConflict, "a file already exists at that path")
		return
	default:
		var denied *reconcile.SpaceDeniedError
		if errors.As(err, &denied) {
			writeError(w, http.StatusForbidden, denied.Unwrap().Error())
			return
		}
		if pathsafe.IsRejection(err) {
			writeError(w, http.StatusBadRequest, "that path is not allowed")
			return
		}
		s.fail(w, r, err)
		return
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"note":      n,
		"rewritten": res.Rewritten,
		"broken":    res.Broken,
	})
}

// handleCreateNote makes a note file. The create affordance on unresolved
// wikilinks uses it; anything else may too.
func (s *Server) handleCreateNote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a path field")
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	clean, err := s.Root.Clean(body.Path)
	if err != nil || scanner.KindOf(clean) == "" || scanner.IsAsset(clean) {
		writeError(w, http.StatusBadRequest, "path must name a markdown or html note inside the tree")
		return
	}
	space := spaceOfPath(clean)
	if s.CanWrite != nil {
		if err := s.CanWrite(r, space); err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
	}
	if int64(len(body.Content)) > s.Root.Limits().MaxNoteSize {
		writeError(w, http.StatusRequestEntityTooLarge, "content is over the note size limit")
		return
	}
	abs, _, err := s.Root.Resolve(clean)
	if err != nil {
		if pathsafe.IsRejection(err) {
			writeError(w, http.StatusBadRequest, "that path is not allowed")
			return
		}
		s.fail(w, r, err)
		return
	}
	if err := s.Root.CheckCollision(clean); err != nil {
		writeError(w, http.StatusBadRequest, "a note with a name equal after case folding exists here")
		return
	}
	id := scanner.NewID(time.Now())
	content, _, _ := frontmatter.EnsureID([]byte(body.Content), id, time.Now())
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		s.fail(w, r, err)
		return
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if os.IsExist(err) {
		writeError(w, http.StatusConflict, "a file already exists at that path")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		s.fail(w, r, err)
		return
	}
	f.Close()
	if s.Scanner != nil {
		if err := s.Scanner.ScanOne(r.Context(), clean); err != nil {
			loggerFrom(r.Context()).Warn("created note is not indexed yet", "path", clean, "err", err)
		}
	}
	n, err := s.DB.GetNoteByPath(r.Context(), clean)
	if err != nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "path": clean})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": n.ID, "path": n.RelPath, "note": n})
}

func spaceOfPath(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}
