// Colour themes added by the owner, kept with the notes in
// .yana/themes.json so they travel with the vault and its git history.
// The dotted folder keeps the file out of the tree and the index. The
// client validates every theme before a colour reaches the page; the
// server only keeps the file a JSON list of objects.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/madeofpendletonwool/yana/internal/fsutil"
)

// themesRel is the themes file, relative to the notes root.
const themesRel = ".yana/themes.json"

const themesMax = 100

func (s *Server) handleThemes(w http.ResponseWriter, r *http.Request) {
	raw, err := os.ReadFile(filepath.Join(s.Root.Dir(), themesRel))
	if errors.Is(err, fs.ErrNotExist) {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		// A hand edit gone wrong shows no themes rather than breaking settings.
		loggerFrom(r.Context()).Warn("themes file unreadable", "path", themesRel, "err", err)
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
	out, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	abs := filepath.Join(s.Root.Dir(), themesRel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := fsutil.WriteFileAtomic(abs, append(out, '\n'), 0o644); err != nil {
		s.fail(w, r, err)
		return
	}
	if s.Git != nil {
		s.Git.Notify(themesRel)
	}
	writeJSON(w, http.StatusOK, list)
}
