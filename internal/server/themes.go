// Colour themes added by the owner, kept with the notes in
// .yana/themes.json so they travel with the vault and its git history.
// The client validates every theme before a colour reaches the page; the
// server only keeps the file a JSON list of objects.
package server

import (
	"net/http"
)

// themesRel is the themes file, relative to the notes root.
const themesRel = ".yana/themes.json"

const themesMax = 100

func (s *Server) handleThemes(w http.ResponseWriter, r *http.Request) {
	var list []map[string]any
	if _, err := s.readYana(r, themesRel, &list); err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleThemesPut(w http.ResponseWriter, r *http.Request) {
	if s.Auth != nil && !s.ident(r).Owner {
		writeError(w, http.StatusForbidden, "themes are added by the owner account")
		return
	}
	var list []map[string]any
	if err := decodeBody(w, r, &list); err != nil {
		return
	}
	if len(list) > themesMax {
		writeError(w, http.StatusBadRequest, "too many themes")
		return
	}
	if list == nil {
		list = []map[string]any{}
	}
	if err := s.writeYana(themesRel, list); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}
