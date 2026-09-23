// Conflict endpoints: the listing for the Data page, the copies behind
// one note's chip, the diff between a copy and its survivor, and the
// three resolutions. A conflict copy is a real note the whole way
// through — indexed, searchable, editable — so resolving one is the
// same move, edit, and trash the rest of the app already does, and
// each resolution lands as one git commit under the caller's identity.
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
)

// handleConflicts lists every conflict copy in the caller's spaces.
func (s *Server) handleConflicts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.ListConflicts(r.Context(), s.spaceFilter(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": rows})
}

// handleNoteConflicts lists the conflict copies behind one note, for
// the chip on its title bar.
func (s *Server) handleNoteConflicts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.noteAuthz(w, r, id); !ok {
		return
	}
	notes, err := s.DB.ConflictsOf(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": notes})
}

// handleConflictDiff diffs a conflict copy against the note it belongs
// to: the bodies as they sit on disk, frontmatter aside, in the same
// unified shape the history diff shows.
func (s *Server) handleConflictDiff(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.noteAuthz(w, r, id); !ok {
		return
	}
	n, of, ok := s.conflictAndSurvivor(w, r, id)
	if !ok {
		return
	}
	mine, err := readBody(s, of)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	theirs, err := readBody(s, n)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"diff":   unifiedDiff(mine, theirs, of.RelPath, n.RelPath),
		"mine":   map[string]any{"id": of.ID, "path": of.RelPath, "title": of.Title},
		"theirs": map[string]any{"id": n.ID, "path": n.RelPath, "title": n.Title},
	})
}

// readBody returns one note's body from disk, frontmatter aside.
func readBody(s *Server, n index.Note) (string, error) {
	abs, _, err := s.Root.Resolve(n.RelPath)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	return string(frontmatter.Parse(raw).Body), nil
}

// unifiedDiff builds a git-shaped diff of two texts: the ---/+++ heads,
// then every line with a leading blank, minus, or plus.
func unifiedDiff(mine, theirs, minePath, theirsPath string) string {
	if mine == theirs {
		return ""
	}
	var b strings.Builder
	b.WriteString("--- " + minePath + "\n")
	b.WriteString("+++ " + theirsPath + "\n")
	dmp := diffmatchpatch.New()
	ca, cb, lines := dmp.DiffLinesToChars(mine, theirs)
	for _, d := range dmp.DiffCharsToLines(dmp.DiffMain(ca, cb, false), lines) {
		var prefix string
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			prefix = " "
		case diffmatchpatch.DiffDelete:
			prefix = "-"
		case diffmatchpatch.DiffInsert:
			prefix = "+"
		}
		for _, line := range strings.Split(strings.TrimSuffix(d.Text, "\n"), "\n") {
			b.WriteString(prefix)
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// conflictAndSurvivor loads a conflict copy and the note it points at,
// writing the error itself when either half is missing.
func (s *Server) conflictAndSurvivor(w http.ResponseWriter, r *http.Request, id string) (index.Note, index.Note, bool) {
	n, err := s.DB.GetNote(r.Context(), id)
	if errors.Is(err, index.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no note with that id")
		return index.Note{}, index.Note{}, false
	}
	if err != nil {
		s.fail(w, r, err)
		return index.Note{}, index.Note{}, false
	}
	if n.ConflictOf == "" {
		writeError(w, http.StatusConflict, "that note is not a conflict copy with a surviving original")
		return index.Note{}, index.Note{}, false
	}
	of, err := s.DB.GetNote(r.Context(), n.ConflictOf)
	if errors.Is(err, index.ErrNotFound) {
		writeError(w, http.StatusConflict, "the note this copy belongs to is gone; resolve it by hand")
		return index.Note{}, index.Note{}, false
	}
	if err != nil {
		s.fail(w, r, err)
		return index.Note{}, index.Note{}, false
	}
	return n, of, true
}

// handleConflictResolve settles one conflict copy against its survivor.
// Keep mine moves the copy to the trash; keep theirs writes the copy's
// body into the survivor as an edit (open clients converge on it) and
// moves the copy to the trash; keep both renames the copy to an
// ordinary "name (older).ext". Each action is one git commit under the
// caller's identity.
func (s *Server) handleConflictResolve(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the reconciliation loop")
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	switch body.Action {
	case "mine", "theirs", "both":
	default:
		writeError(w, http.StatusBadRequest, "action must be mine, theirs, or both")
		return
	}
	id := r.PathValue("id")
	if _, ok := s.noteAuthz(w, r, id); !ok {
		return
	}
	n, of, ok := s.conflictAndSurvivor(w, r, id)
	if !ok {
		return
	}
	if !s.mayWrite(w, r, n.Space) {
		return
	}
	may := s.canWrite(w, r)

	var (
		resPath string
		err     error
	)
	switch body.Action {
	case "theirs":
		text, rerr := readBody(s, n)
		if rerr != nil {
			s.fail(w, r, rerr)
			return
		}
		author := s.ident(r).Author()
		if author == "user:" {
			author = "user:resolve"
		}
		if err = s.Sync.SetText(r.Context(), of.ID, text, author); err != nil {
			break
		}
		_, err = s.Sync.Delete(r.Context(), n.ID, may)
	case "both":
		resPath, err = s.keepBoth(r.Context(), n, may)
	case "mine":
		_, err = s.Sync.Delete(r.Context(), n.ID, may)
	}
	if err != nil {
		switch {
		case errors.Is(err, reconcile.ErrNotFound):
			writeError(w, http.StatusNotFound, "no note with that id")
		case errors.Is(err, reconcile.ErrMoveTargetTaken), errors.Is(err, reconcile.ErrMoveSamePath):
			writeError(w, http.StatusConflict, err.Error())
		default:
			var denied *reconcile.SpaceDeniedError
			if errors.As(err, &denied) {
				writeError(w, http.StatusForbidden, denied.Unwrap().Error())
				return
			}
			s.fail(w, r, err)
		}
		return
	}

	// The resolution is one commit under the caller: flush the loop's
	// pending writes, then commit now instead of waiting out the quiet
	// window.
	if s.Git != nil {
		if _, err := s.Git.Snapshot(r.Context()); err != nil {
			loggerFrom(r.Context()).Warn("conflict resolution is not committed yet", "err", err)
		}
	}
	out := map[string]any{"ok": true, "action": body.Action}
	if resPath != "" {
		out["path"] = resPath
	}
	writeJSON(w, http.StatusOK, out)
}

// keepBoth renames a conflict copy to an ordinary note:
// "name (older).ext", then "name (older) 2.ext" when that is taken.
func (s *Server) keepBoth(ctx context.Context, n index.Note, may reconcile.CanWrite) (string, error) {
	su := index.ConflictSurvivorPath(n.RelPath)
	if su == "" {
		return "", errors.New("that note is not a conflict copy")
	}
	dir := path.Dir(su)
	if dir == "." {
		dir = ""
	}
	ext := path.Ext(su)
	stem := strings.TrimSuffix(path.Base(su), ext)
	var lastErr error
	for i := 0; i < 50; i++ {
		name := stem + " (older)"
		if i > 0 {
			name += fmt.Sprintf(" %d", i+1)
		}
		target := name + ext
		if dir != "" {
			target = dir + "/" + target
		}
		clean, err := s.Root.Clean(target)
		if err != nil {
			return "", err
		}
		_, err = s.Sync.Move(ctx, n.ID, clean, may)
		if err == nil {
			return clean, nil
		}
		lastErr = err
		if !errors.Is(err, reconcile.ErrMoveTargetTaken) && !errors.Is(err, reconcile.ErrMoveSamePath) {
			return "", err
		}
	}
	return "", lastErr
}

// spaceFilter returns the caller's member spaces for list endpoints,
// nil when the view is unrestricted.
func (s *Server) spaceFilter(r *http.Request) []string {
	if s.open() {
		return nil
	}
	member, isAll, err := s.Auth.MemberSpaces(r.Context(), s.ident(r))
	if err != nil {
		return []string{""} // matches nothing
	}
	if isAll {
		return nil
	}
	return member
}

// conflictsCount reports how many conflict copies the caller has, for
// the home screen. A failure reads as none; the count is a hint, not a
// fact the app depends on.
func (s *Server) conflictsCount(r *http.Request) int {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	rows, err := s.DB.ListConflicts(ctx, s.spaceFilter(r))
	if err != nil {
		return 0
	}
	return len(rows)
}
