// Where new notes and the daily note go, chosen by the owner in Settings
// and kept with the notes in .yana/defaults.json, so every device and
// account follows them. The client resolves what the values point at (a
// folder not there yet, a space that went away); the server only checks
// they are well-formed.
package server

import (
	"net/http"
	"slices"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/spaces"
)

// defaultsRel is the defaults file, relative to the notes root.
const defaultsRel = ".yana/defaults.json"

// Defaults is .yana/defaults.json. Spaces are names; folders are full
// paths with their space first. Empty means not chosen.
type Defaults struct {
	Space       string `json:"space"`
	Folder      string `json:"folder"`
	DailySpace  string `json:"daily_space"`
	DailyFolder string `json:"daily_folder"`
}

// handleDefaults answers with the file, or null when there is none yet
// (so the owner's browser can bring over what it chose before).
func (s *Server) handleDefaults(w http.ResponseWriter, r *http.Request) {
	var d Defaults
	found, err := s.readYana(r, defaultsRel, &d)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	allowed, ok := s.readableSpaces(w, r)
	if !ok {
		return
	}
	// A space the caller cannot read is not even named to them.
	if allowed != nil {
		readable := func(space string) bool { return space != "" && slices.Contains(allowed, space) }
		if !readable(d.Space) {
			d.Space = ""
		}
		if !readable(strings.SplitN(d.Folder, "/", 2)[0]) {
			d.Folder = ""
		}
		if !readable(d.DailySpace) {
			d.DailySpace = ""
		}
		if !readable(strings.SplitN(d.DailyFolder, "/", 2)[0]) {
			d.DailyFolder = ""
		}
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDefaultsPut(w http.ResponseWriter, r *http.Request) {
	if s.Auth != nil && !s.ident(r).Owner {
		writeError(w, http.StatusForbidden, "the defaults are set by the owner account")
		return
	}
	var d Defaults
	if err := decodeBody(w, r, &d); err != nil {
		return
	}
	for _, sp := range []string{d.Space, d.DailySpace} {
		if sp != "" && !spaces.ValidName(sp) {
			writeError(w, http.StatusBadRequest, "a default space must be a space name")
			return
		}
	}
	for _, f := range []*string{&d.Folder, &d.DailyFolder} {
		if *f == "" {
			continue
		}
		clean, err := s.Root.Clean(strings.Trim(*f, "/"))
		if err != nil || !spaces.ValidName(strings.Split(clean, "/")[0]) {
			writeError(w, http.StatusBadRequest, "a default folder must be a path inside a space")
			return
		}
		*f = clean
	}
	if err := s.writeYana(defaultsRel, d); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}
