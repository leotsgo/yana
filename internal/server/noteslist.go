// The flat note list: one row per note the caller can see, with the
// fields the offline replica needs (created, updated, tags) that the
// nested tree response does not carry. The Android client caches this
// in Room and rebuilds search and the tree from it when offline.
package server

import (
	"net/http"

	"github.com/madeofpendletonwool/yana/internal/index"
)

// NoteListEntry is one note in the list, its tags riding along.
type NoteListEntry struct {
	index.Note
	Tags []string `json:"tags"`
}

// handleNotesList lists every visible note, optionally restricted to a
// space. The set is the caller's own: a note from a space they do not
// belong to never reaches the response.
func (s *Server) handleNotesList(w http.ResponseWriter, r *http.Request) {
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
	tags, err := s.DB.AllTags(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]NoteListEntry, len(notes))
	for i, n := range notes {
		t := tags[n.ID]
		if t == nil {
			t = []string{}
		}
		out[i] = NoteListEntry{Note: n, Tags: t}
	}
	writeJSON(w, http.StatusOK, map[string]any{"notes": out})
}
