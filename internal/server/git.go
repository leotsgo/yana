// Git history endpoints: per-note revision log, diff between revisions,
// restore, and the explicit snapshot.
package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/git"
	"github.com/madeofpendletonwool/yana/internal/index"
)

// restoreAuthor tags the CRDT edit a restore makes. It maps to the human
// git identity like every user:* author.
const restoreAuthor = "user:restore"

func (s *Server) gitUnavailable(w http.ResponseWriter) bool {
	if s.Git == nil {
		writeError(w, http.StatusNotImplemented, "git history is unavailable in this build")
		return true
	}
	return false
}

// handleNoteHistory lists a note's revisions, following renames.
func (s *Server) handleNoteHistory(w http.ResponseWriter, r *http.Request) {
	if s.gitUnavailable(w) {
		return
	}
	id := r.PathValue("id")
	if _, ok := s.noteAuthz(w, r, id); !ok {
		return
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if errors.Is(err, index.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no note with that id")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	entries, err := s.Git.Log(r.Context(), n.RelPath, 200)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if entries == nil {
		entries = []git.LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// handleNoteHistoryDiff diffs a note's path between two revisions.
func (s *Server) handleNoteHistoryDiff(w http.ResponseWriter, r *http.Request) {
	if s.gitUnavailable(w) {
		return
	}
	id := r.PathValue("id")
	if _, ok := s.noteAuthz(w, r, id); !ok {
		return
	}
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	if !git.ValidRevision(from) || !git.ValidRevision(to) {
		writeError(w, http.StatusBadRequest, "from and to must be commit hashes from the note's history")
		return
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if errors.Is(err, index.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no note with that id")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	diff, err := s.Git.Diff(r.Context(), n.RelPath, from, to)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diff": diff})
}

// handleNoteHistoryRestore writes a revision's old content back into the
// note as an edit: into the CRDT document, through the reconciliation
// loop, never a stomp over the file, so open clients converge to the
// restored text and the restore itself is a revertible edit.
func (s *Server) handleNoteHistoryRestore(w http.ResponseWriter, r *http.Request) {
	if s.gitUnavailable(w) {
		return
	}
	if s.Sync == nil {
		writeError(w, http.StatusNotImplemented, "the reconciliation loop is unavailable in this build")
		return
	}
	id := r.PathValue("id")
	if _, ok := s.noteAuthz(w, r, id); !ok {
		return
	}
	var body struct {
		Revision string `json:"revision"`
		Path     string `json:"path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil || body.Revision == "" {
		writeError(w, http.StatusBadRequest, "body must be JSON with a revision field")
		return
	}
	if !git.ValidRevision(body.Revision) {
		writeError(w, http.StatusBadRequest, "revision must be a commit hash from the note's history")
		return
	}
	n, err := s.DB.GetNote(r.Context(), id)
	if errors.Is(err, index.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no note with that id")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The restore path is the one the note held at that revision (its
	// current path when it has not moved). It must stay inside the tree;
	// the frontmatter id check below refuses anything that is not this
	// note anyway.
	rel := n.RelPath
	if body.Path != "" {
		clean, err := s.Root.Clean(body.Path)
		if err != nil {
			writeError(w, http.StatusBadRequest, "path must be the note's path at that revision, as reported by the history")
			return
		}
		rel = clean
	}
	content, err := s.Git.Show(r.Context(), body.Revision, rel)
	if err != nil {
		writeError(w, http.StatusNotFound, "that revision does not hold this note")
		return
	}
	fm := frontmatter.Parse(content)
	if fm.Meta.ID != id {
		writeError(w, http.StatusConflict, "that revision holds a different note at that path")
		return
	}
	if !s.mayWrite(w, r, n.Space) {
		return
	}
	if err := s.Sync.SetText(r.Context(), id, string(fm.Body), restoreAuthor); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGitSnapshot commits now instead of waiting for the quiet window.
// It touches every space's history, so it needs the global owner.
func (s *Server) handleGitSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.gitUnavailable(w) {
		return
	}
	if s.Auth != nil && !s.ident(r).Owner {
		writeError(w, http.StatusForbidden, "snapshots are run by the owner account")
		return
	}
	commits, err := s.Git.Snapshot(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "commits": commits})
}
