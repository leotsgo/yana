// Space endpoints. A space is a top-level directory whose .space.yml
// carries its display name and member list; these handlers create,
// read, update, and remove that file (and the directory), and the
// watcher reloads the cache from whatever they (or anyone's editor)
// write.
package server

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/spaces"
)

// handleSpaces lists the spaces the identity can see.
func (s *Server) handleSpaces(w http.ResponseWriter, r *http.Request) {
	all, err := s.DB.ListSpaces(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.open() {
		if all == nil {
			all = []index.SpaceRow{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"spaces": all})
		return
	}
	id := s.ident(r)
	member, isAll, err := s.Auth.MemberSpaces(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	allowed := map[string]bool{}
	for _, sp := range member {
		allowed[sp] = true
	}
	// The root itself is a space only the owner sees (notes loose in
	// the tree root).
	if isAll {
		allowed[""] = true
	}
	out := make([]index.SpaceRow, 0, len(all))
	for _, sp := range all {
		if allowed[sp.Space] {
			out = append(out, sp)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"spaces": out})
}

// handleSpaceCreate makes a new space directory with .space.yml naming
// the creator as its owner.
func (s *Server) handleSpaceCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	clean, err := s.Root.Clean(body.Name)
	if err != nil || !spaces.ValidName(clean) {
		writeError(w, http.StatusBadRequest, "name must be a single directory segment (letters, digits, dash, underscore)")
		return
	}
	if err := s.Root.CheckCollision(clean); err != nil {
		writeError(w, http.StatusBadRequest, "a space with a name equal after case folding exists")
		return
	}
	spec := spaces.Spec{Name: clean}
	if s.Auth != nil {
		id := s.ident(r)
		spec.Members = []spaces.Member{{User: id.UserID, Role: spaces.RoleOwner}}
	}
	abs := filepath.Join(s.Root.Dir(), clean)
	if err := os.Mkdir(abs, 0o755); err != nil {
		if os.IsExist(err) {
			writeError(w, http.StatusConflict, "a directory with that name exists")
			return
		}
		s.fail(w, r, err)
		return
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(abs, spaces.FileName), spaces.Render(spec), 0o644); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"name": clean})
}

// handleSpaceGet shows one space: its label, the identity's role, and
// (for space owners) the member list.
func (s *Server) handleSpaceGet(w http.ResponseWriter, r *http.Request) {
	space, ok := s.spacePath(w, r)
	if !ok {
		return
	}
	if s.Auth != nil {
		id := s.ident(r)
		role, err := s.Auth.AuthorizeSpace(r.Context(), id, space)
		if err != nil {
			writeError(w, http.StatusNotFound, "no such space")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": space, "role": role})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": space, "role": spaces.RoleOwner})
}

// handleSpaceUpdate rewrites .space.yml (label and members) for users
// who own the space. The file lands through the same atomic write as
// every other tree change, and the watcher reloads the cache.
func (s *Server) handleSpaceUpdate(w http.ResponseWriter, r *http.Request) {
	space, ok := s.spacePath(w, r)
	if !ok {
		return
	}
	if s.Auth != nil {
		id := s.ident(r)
		role, err := s.Auth.AuthorizeSpace(r.Context(), id, space)
		if err != nil {
			writeError(w, http.StatusNotFound, "no such space")
			return
		}
		if role != spaces.RoleOwner {
			writeError(w, http.StatusForbidden, "only a space owner may change its membership")
			return
		}
	}
	var body struct {
		Name    string          `json:"name"`
		Members []spaces.Member `json:"members"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	spec := spaces.Spec{Name: body.Name, Members: body.Members}
	if _, err := spaces.Parse(spaces.Render(spec)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.writeSpaceFile(space, spec); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleSpaceDelete removes an empty space directory. A space with
// notes or assets in it must be emptied first; that is the honest way
// to delete content.
func (s *Server) handleSpaceDelete(w http.ResponseWriter, r *http.Request) {
	space, ok := s.spacePath(w, r)
	if !ok {
		return
	}
	if s.Auth != nil {
		id := s.ident(r)
		role, err := s.Auth.AuthorizeSpace(r.Context(), id, space)
		if err != nil {
			writeError(w, http.StatusNotFound, "no such space")
			return
		}
		if role != spaces.RoleOwner {
			writeError(w, http.StatusForbidden, "only a space owner may remove the space")
			return
		}
	}
	abs, _, err := s.Root.Resolve(space)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such space")
		return
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such space")
		return
	}
	for _, e := range entries {
		if e.Name() != spaces.FileName {
			writeError(w, http.StatusConflict, "the space still holds notes or assets; empty it first")
			return
		}
	}
	if err := os.Remove(filepath.Join(abs, spaces.FileName)); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := os.Remove(abs); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// spacePath validates the {space} path parameter.
func (s *Server) spacePath(w http.ResponseWriter, r *http.Request) (string, bool) {
	space := r.PathValue("space")
	clean, err := s.Root.Clean(space)
	if err != nil || !spaces.ValidName(clean) || clean != space {
		writeError(w, http.StatusBadRequest, "space must be a single directory name")
		return "", false
	}
	return clean, true
}

// writeSpaceFile writes .space.yml atomically through the path-safety
// root.
func (s *Server) writeSpaceFile(space string, spec spaces.Spec) error {
	abs, _, err := s.Root.Resolve(spaces.FileRel(space))
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(abs, spaces.Render(spec), 0o644)
}
