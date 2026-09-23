// Point-in-time restore endpoints: the preview that says exactly what
// restoring to a commit would do, the restore itself, and the
// deleted-notes list that brings one note back without reading a commit
// log. A tree restore is owner only; a space restore needs write access
// on that space; agent tokens never reach these routes — they have no
// place on /api at all.
package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
)

// pitRestoreBody is the request shape for both the preview and the
// restore. Space "" is the whole tree; otherwise it is one space's
// directory, and nothing outside it moves.
type pitRestoreBody struct {
	Commit string `json:"commit"`
	Space  string `json:"space"`
}

// pitRestoreAuthz checks the body's scope and the caller's right to
// restore it, returning the cleaned space name ("" is the whole tree).
// Errors are already written; ok=false means stop.
func (s *Server) pitRestoreAuthz(w http.ResponseWriter, r *http.Request, body pitRestoreBody) (string, bool) {
	if !git.ValidRevision(body.Commit) {
		writeError(w, http.StatusBadRequest, "commit must be a commit hash from the history")
		return "", false
	}
	if body.Space == "" {
		// The tree restore reaches every space at once.
		if s.Auth != nil && !s.ident(r).Owner {
			writeError(w, http.StatusForbidden, "a restore of the whole tree is run by the owner account")
			return "", false
		}
		return "", true
	}
	clean, err := s.Root.Clean(body.Space)
	if err != nil || clean == "" || strings.Contains(clean, "/") {
		writeError(w, http.StatusBadRequest, "space must be a single directory name")
		return "", false
	}
	if _, ok := s.spaceAuthz(w, r, clean); !ok {
		return "", false
	}
	if !s.mayWrite(w, r, clean) {
		return "", false
	}
	return clean, true
}

// pitAuthor is the git identity the restore commits under: the person
// who ran it. An account-less server has no person to name, and the
// layer falls back to the configured human identity.
func (s *Server) pitAuthor(r *http.Request) git.PITAuthor {
	if s.Auth == nil {
		return git.PITAuthor{}
	}
	id := s.ident(r)
	if id.Username == "" || strings.ContainsAny(id.Username, "<>\n\r") {
		return git.PITAuthor{}
	}
	email := id.Username + "@yana.local"
	if git.AuthorKind(id.Username, email) != "person" {
		return git.PITAuthor{}
	}
	return git.PITAuthor{Name: id.Username, Email: email}
}

// restoreChangeJSON is one path the restore would touch, as the preview
// lists it. ID and title are present when the note exists now, so a
// preview row can open the note it is about to change.
type restoreChangeJSON struct {
	Action string `json:"action"` // added, changed, deleted, moved
	Path   string `json:"path"`
	From   string `json:"from,omitempty"` // the path the note holds now; moves only
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
}

// pitChangesJSON maps a plan onto the preview's rows, resolving note
// ids and titles from the index in one read.
func (s *Server) pitChangesJSON(r *http.Request, changes []git.PITChange) []restoreChangeJSON {
	byPath := map[string]noteRef{}
	if notes, err := s.DB.ListNotes(r.Context(), ""); err == nil {
		for _, n := range notes {
			byPath[n.RelPath] = noteRef{n.ID, n.Title}
		}
	}
	out := make([]restoreChangeJSON, 0, len(changes))
	for _, c := range changes {
		row := restoreChangeJSON{Path: c.Path}
		lookup := c.Path
		switch c.Status {
		case 'A':
			row.Action = "added" // nothing lives here now; the note returns with the restore
		case 'D':
			row.Action = "deleted"
		case 'R':
			row.Action = "moved"
			row.From = c.Orig
			lookup = c.Orig // the note exists now, at the path it would leave
		default:
			row.Action = "changed"
		}
		if n, ok := byPath[lookup]; ok {
			row.ID, row.Title = n.id, n.title
		}
		out = append(out, row)
	}
	return out
}

// handlePITRestorePreview describes, exactly and without touching
// anything, what restoring to a commit would do.
func (s *Server) handlePITRestorePreview(w http.ResponseWriter, r *http.Request) {
	if s.gitUnavailable(w) {
		return
	}
	var body pitRestoreBody
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	space, ok := s.pitRestoreAuthz(w, r, body)
	if !ok {
		return
	}
	p, err := s.Git.PreviewPIT(r.Context(), body.Commit, space)
	if err != nil {
		s.writePITError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"preview": map[string]any{
			"commit": p.Commit, "subject": p.Subject, "author": p.Author, "date": p.Date,
			"space": p.Space, "added": p.Added, "changed": p.Changed, "deleted": p.Deleted, "moved": p.Moved,
			"changes": s.pitChangesJSON(r, p.Changes),
		},
	})
}

// handlePITRestore moves the tree, or one space, back to a commit: the
// pre-restore state is committed and tagged, the plan is applied, the
// whole thing lands as one commit authored by whoever ran it, and open
// editors converge through the reconciliation loop. What the restore
// removes goes to the trash, so it comes back the same way.
func (s *Server) handlePITRestore(w http.ResponseWriter, r *http.Request) {
	if s.gitUnavailable(w) {
		return
	}
	var body pitRestoreBody
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	space, ok := s.pitRestoreAuthz(w, r, body)
	if !ok {
		return
	}
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "the reconciliation loop is unavailable in this build")
		return
	}
	// The restore runs to completion even if the caller walks away
	// halfway through; the tree must not stop between two states.
	ctx := context.WithoutCancel(r.Context())
	who := s.pitAuthor(r)
	res, err := s.Git.RestoreTo(ctx, body.Commit, space, who, git.PITApply{
		Trash: func(ctx context.Context, rel string) error {
			clean, err := s.Root.Clean(rel)
			if err != nil {
				return nil // the scan applies the same path rules
			}
			if s.DB == nil {
				return s.Sync.TrashFile(ctx, clean)
			}
			if row, err := s.DB.GetNoteByPath(ctx, clean); err == nil {
				// A note goes out the same door a delete uses, so its
				// trash record and retained document come with it.
				_, err := s.Sync.Delete(ctx, row.ID, nil)
				return err
			}
			return s.Sync.TrashFile(ctx, clean)
		},
		Sync: func(ctx context.Context, rel string) {
			if clean, err := s.Root.Clean(rel); err == nil {
				s.Sync.Sync(ctx, clean)
			}
		},
	})
	if err != nil {
		s.writePITError(w, err)
		return
	}
	if s.Scanner != nil {
		if _, err := s.Scanner.Scan(ctx); err != nil {
			s.Log.Error("rescan after a restore failed", "err", err)
		}
	}
	if s.Sync != nil {
		s.Sync.SweepOrphans(ctx)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "commit": res.Commit, "tag": res.Tag,
		"added": res.Added, "changed": res.Changed, "deleted": res.Deleted, "moved": res.Moved,
	})
}

// writePITError maps the git layer's answers onto statuses.
func (s *Server) writePITError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, git.ErrNoSuchCommit):
		writeError(w, http.StatusBadRequest, "that commit is not in this history")
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// --- deleted notes ------------------------------------------------------------
//
// The Data page's list: every deleted note with something to bring it
// back — the trash copy, the retained document, or the git history.

// deletedNoteJSON is one row of the deleted-notes list.
type deletedNoteJSON struct {
	index.DeletedNote
	HasFile    bool `json:"has_file"`
	HasSidecar bool `json:"has_sidecar"`
	// InHistory says the git history holds the note's content: the
	// restore source when neither the trash copy nor the retained
	// document survives.
	InHistory bool `json:"in_history"`
	Untracked bool `json:"untracked,omitempty"`
}

// handleDeletedNotes lists deleted notes the caller may write, with
// what a restore would recover them from.
func (s *Server) handleDeletedNotes(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "the reconciliation loop is unavailable in this build")
		return
	}
	entries, err := s.Sync.ListDeletedEvery(r.Context(), s.trashAllow(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]deletedNoteJSON, 0, len(entries))
	for _, e := range entries {
		row := deletedNoteJSON{DeletedNote: e.DeletedNote, HasFile: e.HasFile, HasSidecar: e.HasSidecar, Untracked: e.Untracked}
		if !e.HasFile && !e.HasSidecar {
			// Neither local copy survives; the note is only in the
			// history, if it is anywhere at all.
			if s.Git == nil {
				continue
			}
			if _, _, err := s.Git.FindNote(r.Context(), e.RelPath, e.ID); err != nil {
				continue
			}
			row.InHistory = true
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

// handleDeletedNoteRestore brings one deleted note back: from the trash
// copy or the retained document when either survives, otherwise from
// the git history — the last committed revision of the note, at its
// original path or a free name beside whoever took the path.
func (s *Server) handleDeletedNoteRestore(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "the reconciliation loop is unavailable in this build")
		return
	}
	id := r.PathValue("id")
	res, err := s.Sync.Restore(r.Context(), id, s.canWrite(w, r))
	if err == nil {
		s.deletedRestored(w, res.Path, res.Conflict, res.Note, res.Deferred, "trash")
		return
	}
	switch {
	case errors.Is(err, reconcile.ErrNothingToRestore):
		// The local copies are gone; the history is the way back.
	case errors.Is(err, reconcile.ErrTrashGone), errors.Is(err, reconcile.ErrNotFound):
		writeError(w, http.StatusNotFound, "no deleted note with that id")
		return
	case errors.Is(err, reconcile.ErrMoveTargetTaken):
		writeError(w, http.StatusConflict, "a file already exists at the restore path")
		return
	case pathsafe.IsRejection(err):
		writeError(w, http.StatusBadRequest, "that path is not allowed")
		return
	default:
		var denied *reconcile.SpaceDeniedError
		if errors.As(err, &denied) {
			writeError(w, http.StatusForbidden, denied.Unwrap().Error())
			return
		}
		s.fail(w, r, err)
		return
	}
	s.restoreFromHistory(w, r, id)
}

// restoreFromHistory is the fallback: the note's last committed
// revision, written back where it lived.
func (s *Server) restoreFromHistory(w http.ResponseWriter, r *http.Request, id string) {
	if s.gitUnavailable(w) {
		writeError(w, http.StatusConflict, "nothing left to restore: the note's trash copy is gone and the history is unavailable")
		return
	}
	row, _, err := s.Sync.DeletedRow(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "no deleted note with that id")
		return
	}
	if !s.mayWrite(w, r, row.Space) {
		return
	}
	content, rev, err := s.Git.FindNote(r.Context(), row.RelPath, row.ID)
	if err != nil {
		writeError(w, http.StatusConflict, "nothing left to restore: the history does not hold that note")
		return
	}
	target := row.RelPath
	conflict := false
	if abs, _, err := s.Root.Resolve(target); err == nil {
		if _, statErr := os.Lstat(abs); statErr == nil {
			conflict = true
			ext := path.Ext(target)
			target = strings.TrimSuffix(target, ext) + ".conflict-" + time.Now().UTC().Format("20060102T150405") + ext
		}
	}
	abs, _, err := s.Root.Resolve(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, "that path is not allowed")
		return
	}
	if err := fsutil.MkdirInherit(filepath.Dir(abs)); err != nil {
		s.fail(w, r, err)
		return
	}
	if err := fsutil.WriteFileAtomic(abs, content, 0o644); err != nil {
		s.fail(w, r, err)
		return
	}
	// The returning file commits under the person who brought it back.
	if s.Auth != nil {
		if a := s.ident(r).Author(); a != "" {
			s.Git.Author(target, a)
		}
	}
	s.Sync.Sync(r.Context(), target)
	note, err := s.DB.GetNote(r.Context(), row.ID)
	if err != nil && !errors.Is(err, index.ErrNotFound) {
		s.fail(w, r, err)
		return
	}
	deferred := errors.Is(err, index.ErrNotFound)
	s.Log.Info("note restored from the history", "id", row.ID, "path", target, "revision", rev, "conflict", conflict)
	s.deletedRestored(w, target, conflict, note, deferred, "history")
}

func (s *Server) deletedRestored(w http.ResponseWriter, target string, conflict bool, note index.Note, deferred bool, from string) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "path": target, "conflict": conflict, "note": note, "deferred": deferred, "from": from,
	})
}
