// Trash endpoints: soft delete, the trash listing, restore, and the two
// permanent destructions (one entry, everything). Space membership rules
// apply as everywhere else: a member sees and recovers trash only in
// their spaces, and emptying reaches only those.
package server

import (
	"errors"
	"net/http"

	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
)

// trashAllow builds the space filter for the caller: nil (everything)
// for the owner or an account-less server, otherwise membership. A
// lookup failure fails closed: nothing is visible.
func (s *Server) trashAllow(r *http.Request) func(space string) bool {
	if s.open() {
		return nil
	}
	id := s.ident(r)
	member, isAll, err := s.Auth.MemberSpaces(r.Context(), id)
	if err != nil {
		return func(string) bool { return false }
	}
	if isAll {
		return nil
	}
	allowed := make(map[string]bool, len(member))
	for _, sp := range member {
		allowed[sp] = true
	}
	return func(space string) bool { return allowed[space] }
}

// handleDeleteNote soft-deletes a note: the file moves under .trash and
// the document is retained for the retention window. The client is
// expected to have shown inbound links first (GET backlinks); the server
// does not block on them.
func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.noteAuthz(w, r, id); !ok {
		return
	}
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the reconciliation loop")
		return
	}
	res, err := s.Sync.Delete(r.Context(), id, s.canWrite(w, r))
	if err != nil {
		switch {
		case errors.Is(err, reconcile.ErrNotFound):
			writeError(w, http.StatusNotFound, "no note with that id")
			return
		}
		var denied *reconcile.SpaceDeniedError
		if errors.As(err, &denied) {
			writeError(w, http.StatusForbidden, denied.Unwrap().Error())
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trash_path": res.TrashPath})
}

// handleTrash lists deleted notes with deletion time and original path.
func (s *Server) handleTrash(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the reconciliation loop")
		return
	}
	entries, err := s.Sync.ListTrash(r.Context(), s.trashAllow(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if entries == nil {
		entries = []reconcile.TrashEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// handleTrashRestore returns one deleted note to its original path.
func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the reconciliation loop")
		return
	}
	res, err := s.Sync.Restore(r.Context(), r.PathValue("id"), s.canWrite(w, r))
	if err != nil {
		switch {
		case errors.Is(err, reconcile.ErrTrashGone), errors.Is(err, reconcile.ErrNotFound):
			writeError(w, http.StatusNotFound, "no deleted note with that id")
			return
		case errors.Is(err, reconcile.ErrNothingToRestore):
			writeError(w, http.StatusConflict, "nothing left to restore: the retention window has passed")
			return
		case errors.Is(err, reconcile.ErrMoveTargetTaken):
			writeError(w, http.StatusConflict, "a file already exists at the restore path")
			return
		}
		if pathsafe.IsRejection(err) {
			writeError(w, http.StatusBadRequest, "that path is not allowed")
			return
		}
		var denied *reconcile.SpaceDeniedError
		if errors.As(err, &denied) {
			writeError(w, http.StatusForbidden, denied.Unwrap().Error())
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": res.Path, "conflict": res.Conflict, "note": res.Note, "deferred": res.Deferred})
}

// handleTrashDestroy permanently destroys one trash entry. This and
// emptying the trash are the only permanent destruction.
func (s *Server) handleTrashDestroy(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the reconciliation loop")
		return
	}
	err := s.Sync.Destroy(r.Context(), r.PathValue("id"), s.canWrite(w, r))
	if err != nil {
		switch {
		case errors.Is(err, reconcile.ErrTrashGone), errors.Is(err, reconcile.ErrNotFound):
			writeError(w, http.StatusNotFound, "no deleted note with that id")
			return
		}
		var denied *reconcile.SpaceDeniedError
		if errors.As(err, &denied) {
			writeError(w, http.StatusForbidden, denied.Unwrap().Error())
			return
		}
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTrashEmpty permanently destroys every trash entry the caller may
// write. The client confirms before calling it.
func (s *Server) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the reconciliation loop")
		return
	}
	allow := s.trashAllow(r)
	if allow != nil {
		id := s.ident(r)
		// Emptying is a write on every space it reaches.
		member, isAll, err := s.Auth.MemberSpaces(r.Context(), id)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if !isAll {
			for _, sp := range member {
				if !s.mayWrite(w, r, sp) {
					return
				}
			}
		}
	}
	n, err := s.Sync.EmptyTrash(r.Context(), allow)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "destroyed": n})
}
