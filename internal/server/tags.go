// Tag endpoints: the list of tags across the spaces the caller can see,
// and every note carrying one tag. Tags are the inline #tags the scanner
// extracts from note bodies (render.Tags); nothing here writes a file.
package server

import (
	"net/http"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/index"
)

// allowedSpaces is the caller's space filter for index queries: nil when
// every space is visible, otherwise the list (possibly empty).
func (s *Server) allowedSpaces(r *http.Request) ([]string, bool) {
	if s.open() {
		return nil, true
	}
	member, isAll, err := s.Auth.MemberSpaces(r.Context(), s.ident(r))
	if err != nil {
		return []string{}, false
	}
	if isAll {
		return nil, true
	}
	if member == nil {
		member = []string{}
	}
	return member, true
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	allowed, ok := s.allowedSpaces(r)
	if !ok {
		writeError(w, http.StatusInternalServerError, "could not resolve the account's spaces")
		return
	}
	tags, err := s.DB.ListTags(r.Context(), allowed)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tags == nil {
		tags = []index.TagCount{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

func (s *Server) handleTagNotes(w http.ResponseWriter, r *http.Request) {
	tag := strings.ToLower(strings.TrimPrefix(r.PathValue("tag"), "#"))
	if tag == "" || len(tag) > 200 {
		writeError(w, http.StatusBadRequest, "tag is required")
		return
	}
	notes, err := s.DB.NotesByTag(r.Context(), tag)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	notes = s.visibleNotes(r, notes)
	if notes == nil {
		notes = []index.Note{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tag": tag, "notes": notes})
}
