// Folder endpoints. A folder is a real directory (invariant 3), so making
// one is a mkdir, and renaming or moving one is a move of every note
// under it through the reconciler — the same path a single note takes,
// so every inbound wikilink is rewritten — followed by whatever else the
// directory held (assets, files the scanner ignores) and the empty shell.
// Deleting one trashes each note the same way a single delete does, then
// removes the directories left empty.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/reconcile"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// dirParam cleans a folder path from a request. A folder sits inside a
// space (a bare space name is not a folder) and never under _assets.
func (s *Server) dirParam(w http.ResponseWriter, raw string) (string, bool) {
	clean, err := s.Root.Clean(raw)
	if err != nil || clean == "" {
		writeError(w, http.StatusBadRequest, "that path is not allowed")
		return "", false
	}
	if !strings.Contains(clean, "/") {
		writeError(w, http.StatusBadRequest, "a folder lives inside a space; spaces are managed in settings")
		return "", false
	}
	if scanner.IsAsset(clean) || path.Base(clean) == "_assets" {
		writeError(w, http.StatusBadRequest, "_assets directories hold images, not notes")
		return "", false
	}
	if strings.HasPrefix(path.Base(clean), ".") {
		writeError(w, http.StatusBadRequest, "that path is not allowed")
		return "", false
	}
	return clean, true
}

// handleDirCreate makes an empty folder. It shows in the tree at once
// (the tree endpoint walks for empty directories) and holds nothing
// until a note lands in it.
func (s *Server) handleDirCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a path field")
		return
	}
	clean, ok := s.dirParam(w, body.Path)
	if !ok {
		return
	}
	if !s.mayWrite(w, r, spaceOfPath(clean)) {
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
	if info, err := os.Lstat(abs); err == nil {
		if info.IsDir() {
			writeError(w, http.StatusConflict, "a folder already exists at that path")
		} else {
			writeError(w, http.StatusConflict, "a file already exists at that path")
		}
		return
	}
	if err := s.Root.CheckCollision(clean); err != nil {
		writeError(w, http.StatusBadRequest, "a name equal after case folding exists here")
		return
	}
	if err := fsutil.MkdirInherit(abs); err != nil {
		s.fail(w, r, err)
		return
	}
	loggerFrom(r.Context()).Info("folder created", "path", clean)
	writeJSON(w, http.StatusCreated, map[string]any{"path": clean})
}

// notesUnder lists the indexed notes inside a folder, shallowest first
// so a move recreates the structure top-down.
func (s *Server) notesUnder(r *http.Request, dir string) ([]index.Note, error) {
	all, err := s.DB.ListNotes(r.Context(), spaceOfPath(dir))
	if err != nil {
		return nil, err
	}
	var out []index.Note
	for _, n := range all {
		if strings.HasPrefix(n.RelPath, dir+"/") {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := strings.Count(out[i].RelPath, "/"), strings.Count(out[j].RelPath, "/")
		if di != dj {
			return di < dj
		}
		return out[i].RelPath < out[j].RelPath
	})
	return out, nil
}

// handleDirMove renames or moves a folder: every note under it moves
// through the reconciler (rewriting inbound links), then the rest of the
// directory's contents follow and the empty shell is removed. A failure
// part-way stops there and reports how far it got; the notes already
// moved stay moved, each one a complete, link-safe move of its own.
func (s *Server) handleDirMove(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the reconciliation loop")
		return
	}
	var body struct {
		Path string `json:"path"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with path and to fields")
		return
	}
	from, ok := s.dirParam(w, body.Path)
	if !ok {
		return
	}
	to, ok := s.dirParam(w, body.To)
	if !ok {
		return
	}
	if from == to {
		writeError(w, http.StatusBadRequest, "the folder is already at that path")
		return
	}
	if strings.HasPrefix(to, from+"/") {
		writeError(w, http.StatusBadRequest, "a folder cannot move inside itself")
		return
	}
	if !s.mayWrite(w, r, spaceOfPath(from)) || !s.mayWrite(w, r, spaceOfPath(to)) {
		return
	}
	absFrom, _, err := s.Root.Resolve(from)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if info, err := os.Lstat(absFrom); err != nil || !info.IsDir() {
		writeError(w, http.StatusNotFound, "no folder at that path")
		return
	}
	absTo, _, err := s.Root.Resolve(to)
	if err != nil {
		if pathsafe.IsRejection(err) {
			writeError(w, http.StatusBadRequest, "that path is not allowed")
			return
		}
		s.fail(w, r, err)
		return
	}
	if info, err := os.Lstat(absTo); err == nil && !info.IsDir() {
		writeError(w, http.StatusConflict, "a file already exists at that path")
		return
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := s.Root.CheckCollision(to); err != nil {
			writeError(w, http.StatusBadRequest, "a name equal after case folding exists here")
			return
		}
	}
	notes, err := s.notesUnder(r, from)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	moved, rewritten, broken := 0, 0, 0
	for _, n := range notes {
		target := to + strings.TrimPrefix(n.RelPath, from)
		res, err := s.Sync.Move(r.Context(), n.ID, target, s.canWrite(w, r))
		if err != nil {
			var denied *reconcile.SpaceDeniedError
			msg := "could not move " + n.RelPath
			code := http.StatusInternalServerError
			switch {
			case errors.As(err, &denied):
				// mayWrite already answered.
				return
			case errors.Is(err, reconcile.ErrMoveTargetTaken):
				msg, code = "a file already exists at "+target, http.StatusConflict
			case pathsafe.IsRejection(err):
				msg, code = "that path is not allowed", http.StatusBadRequest
			default:
				loggerFrom(r.Context()).Error("folder move stopped", "from", from, "to", to, "note", n.RelPath, "err", err)
			}
			writeJSON(w, code, map[string]any{
				"error": msg + " (" + itoa(moved) + " of " + itoa(len(notes)) + " notes moved)",
				"moved": moved, "total": len(notes), "rewritten": rewritten, "broken": broken,
			})
			return
		}
		moved++
		rewritten += res.Rewritten
		broken += res.Broken
	}
	// Whatever the notes left behind — assets, files the scanner does
	// not index, empty subfolders — goes the same way, then the shell.
	if err := moveRest(absFrom, absTo); err != nil {
		loggerFrom(r.Context()).Warn("folder moved but some contents stayed", "from", from, "to", to, "err", err)
	}
	loggerFrom(r.Context()).Info("folder moved", "from", from, "to", to, "notes", moved, "rewrites", rewritten, "broken", broken)
	writeJSON(w, http.StatusOK, map[string]any{
		"path": to, "moved": moved, "total": len(notes), "rewritten": rewritten, "broken": broken,
	})
}

// moveRest moves every entry still under from into to and removes from.
// Directories that exist on both sides are merged; a file that exists on
// both sides is left where it was.
func moveRest(from, to string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	var firstErr error
	for _, e := range entries {
		src := filepath.Join(from, e.Name())
		dst := filepath.Join(to, e.Name())
		if e.IsDir() {
			if err := fsutil.MkdirInherit(dst); err != nil {
				firstErr = errors.Join(firstErr, err)
				continue
			}
			if err := moveRest(src, dst); err != nil {
				firstErr = errors.Join(firstErr, err)
			}
			continue
		}
		if _, err := os.Lstat(dst); err == nil {
			firstErr = errors.Join(firstErr, errors.New(dst+" already exists"))
			continue
		}
		if err := fsutil.MkdirInherit(to); err != nil {
			firstErr = errors.Join(firstErr, err)
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			firstErr = errors.Join(firstErr, err)
		}
	}
	if firstErr == nil {
		if err := os.Remove(from); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return firstErr
}

// handleDirDelete trashes every note under a folder and removes the
// directories that are empty afterwards. Assets stay where they are —
// the notes that link them may come back from the trash — so a folder
// holding some keeps its shell.
func (s *Server) handleDirDelete(w http.ResponseWriter, r *http.Request) {
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "this server runs without the reconciliation loop")
		return
	}
	dir, ok := s.dirParam(w, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	if !s.mayWrite(w, r, spaceOfPath(dir)) {
		return
	}
	abs, _, err := s.Root.Resolve(dir)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if info, err := os.Lstat(abs); err != nil || !info.IsDir() {
		writeError(w, http.StatusNotFound, "no folder at that path")
		return
	}
	notes, err := s.notesUnder(r, dir)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	deleted := 0
	for _, n := range notes {
		if _, err := s.Sync.Delete(r.Context(), n.ID, s.canWrite(w, r)); err != nil {
			var denied *reconcile.SpaceDeniedError
			if errors.As(err, &denied) {
				return
			}
			loggerFrom(r.Context()).Error("folder delete stopped", "dir", dir, "note", n.RelPath, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error":   "could not delete " + n.RelPath + " (" + itoa(deleted) + " of " + itoa(len(notes)) + " notes deleted)",
				"deleted": deleted, "total": len(notes),
			})
			return
		}
		deleted++
	}
	removed := removeEmptyDirs(abs)
	loggerFrom(r.Context()).Info("folder deleted", "path", dir, "notes", deleted, "removed", removed)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": deleted, "removed": removed})
}

// removeEmptyDirs removes dir if nothing but empty directories is left
// under it, bottom-up, and reports whether dir itself went.
func removeEmptyDirs(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	empty := true
	for _, e := range entries {
		if e.IsDir() && removeEmptyDirs(filepath.Join(dir, e.Name())) {
			continue
		}
		empty = false
	}
	if !empty {
		return false
	}
	return os.Remove(dir) == nil
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
