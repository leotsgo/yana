// Git history endpoints: per-note revision log, diff between revisions,
// restore, the explicit snapshot, and the backup remotes.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

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

// --- backup remotes ------------------------------------------------------------
//
// Remotes are owner-only like snapshots: a push carries every space.
// Responses never include the credential, only whether one is stored.

func (s *Server) gitOwnerOnly(w http.ResponseWriter, r *http.Request) bool {
	if s.gitUnavailable(w) {
		return false
	}
	if s.Auth != nil && !s.ident(r).Owner {
		writeError(w, http.StatusForbidden, "backup remotes are managed by the owner account")
		return false
	}
	return true
}

var remoteNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,47}$`)

// remoteBody is the request shape for creating and editing a remote.
type remoteBody struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Schedule string `json:"schedule"`
	PushHour *int   `json:"push_hour"`
	// Username left out of an edit keeps the stored one; "" clears it.
	Username *string `json:"username"`
	// Token is the credential. On an edit an empty token keeps the stored
	// one unless ClearToken is set.
	Token      string `json:"token"`
	ClearToken bool   `json:"clear_token"`
	Enabled    *bool  `json:"enabled"`
}

// applyRemoteBody validates body onto r. It reports whether the stored
// credential should be kept as is.
func (s *Server) applyRemoteBody(w http.ResponseWriter, body remoteBody, r *index.GitRemote, create bool) (keepSecret bool, ok bool) {
	name := strings.TrimSpace(body.Name)
	if !remoteNamePattern.MatchString(name) {
		writeError(w, http.StatusBadRequest, "name must be 1 to 48 letters, digits, spaces, dots, underscores or dashes")
		return false, false
	}
	clean, urlUser, urlPass, err := git.ParseRemoteURL(body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false, false
	}
	schedule := body.Schedule
	if schedule == "" {
		schedule = git.ScheduleNightly
	}
	if !git.ValidSchedule(schedule) {
		writeError(w, http.StatusBadRequest, "schedule must be commit, hourly, or nightly")
		return false, false
	}
	if body.PushHour != nil {
		if *body.PushHour < 0 || *body.PushHour > 23 {
			writeError(w, http.StatusBadRequest, "push_hour must be 0 to 23")
			return false, false
		}
		r.PushHour = *body.PushHour
	} else if create {
		r.PushHour = 2
	}
	r.Name, r.URL, r.Schedule = name, clean, schedule
	if body.Username != nil {
		r.Username = strings.TrimSpace(*body.Username)
	}
	if urlUser != "" {
		r.Username = urlUser
	}
	if body.Enabled != nil {
		r.Enabled = *body.Enabled
	} else if create {
		r.Enabled = true
	}
	token := body.Token
	if token == "" {
		token = urlPass
	}
	switch {
	case token != "":
		if strings.ContainsAny(token, "\r\n\x00") {
			writeError(w, http.StatusBadRequest, "token must be a single line")
			return false, false
		}
		sealed, err := s.Git.Seal(token)
		if err != nil {
			writeError(w, http.StatusNotImplemented, err.Error())
			return false, false
		}
		r.Secret = sealed
		r.HasSecret = true
		return false, true
	case body.ClearToken || create:
		r.Secret = nil
		r.HasSecret = false
		return false, true
	default:
		return true, true
	}
}

func (s *Server) handleGitRemotes(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	remotes, err := s.DB.ListGitRemotes(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if remotes == nil {
		remotes = []index.GitRemote{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"remotes": remotes})
}

func (s *Server) handleGitRemoteCreate(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	var body remoteBody
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	remote := index.GitRemote{ID: git.NewRemoteID(), CreatedAt: time.Now().UTC()}
	if _, ok := s.applyRemoteBody(w, body, &remote, true); !ok {
		return
	}
	if err := s.DB.CreateGitRemote(r.Context(), remote); err != nil {
		writeRemoteError(w, r, s, err)
		return
	}
	s.Git.Reload(r.Context())
	remote.Secret = nil
	writeJSON(w, http.StatusCreated, remote)
}

func (s *Server) handleGitRemoteUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	remote, err := s.DB.GetGitRemote(r.Context(), r.PathValue("id"))
	if err != nil {
		writeRemoteError(w, r, s, err)
		return
	}
	var body remoteBody
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	// Fields the body leaves out keep their values.
	if body.Name == "" {
		body.Name = remote.Name
	}
	if body.URL == "" {
		body.URL = remote.URL
	}
	if body.Schedule == "" {
		body.Schedule = remote.Schedule
	}
	keep, ok := s.applyRemoteBody(w, body, &remote, false)
	if !ok {
		return
	}
	if err := s.DB.UpdateGitRemote(r.Context(), remote, keep); err != nil {
		writeRemoteError(w, r, s, err)
		return
	}
	s.Git.Reload(r.Context())
	remote, err = s.DB.GetGitRemote(r.Context(), remote.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	remote.Secret = nil
	writeJSON(w, http.StatusOK, remote)
}

func (s *Server) handleGitRemoteDelete(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	if err := s.DB.DeleteGitRemote(r.Context(), r.PathValue("id")); err != nil {
		writeRemoteError(w, r, s, err)
		return
	}
	s.Git.Reload(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleGitRemotePush commits what is pending, then pushes to one remote
// now. The push error, when there is one, is the response: it is the
// thing the owner needs to read.
func (s *Server) handleGitRemotePush(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	remote, err := s.DB.GetGitRemote(r.Context(), r.PathValue("id"))
	if err != nil {
		writeRemoteError(w, r, s, err)
		return
	}
	if _, err := s.Git.Snapshot(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, "commit before push failed: "+err.Error())
		return
	}
	if err := s.Git.Push(r.Context(), remote); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	remote, err = s.DB.GetGitRemote(r.Context(), remote.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	remote.Secret = nil
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "remote": remote})
}

// handleGitRemoteTest checks the remote is reachable with its credentials.
func (s *Server) handleGitRemoteTest(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	remote, err := s.DB.GetGitRemote(r.Context(), r.PathValue("id"))
	if err != nil {
		writeRemoteError(w, r, s, err)
		return
	}
	refs, err := s.Git.Test(r.Context(), remote)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "branches": refs})
}

func writeRemoteError(w http.ResponseWriter, r *http.Request, s *Server, err error) {
	switch {
	case errors.Is(err, index.ErrGitRemoteNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, index.ErrGitRemoteNameTaken):
		writeError(w, http.StatusConflict, err.Error())
	default:
		s.fail(w, r, err)
	}
}

// --- restore from a backup ---------------------------------------------------

// restoreRemote loads the remote a restore was asked for, refusing the
// ones a restore cannot run against.
func (s *Server) restoreRemote(w http.ResponseWriter, r *http.Request) (index.GitRemote, bool) {
	remote, err := s.DB.GetGitRemote(r.Context(), r.PathValue("id"))
	if err != nil {
		writeRemoteError(w, r, s, err)
		return index.GitRemote{}, false
	}
	if !remote.Enabled {
		writeError(w, http.StatusBadRequest, "that remote is disabled; enable it to restore from it")
		return index.GitRemote{}, false
	}
	return remote, true
}

// handleGitRestorePreview fetches a backup into the hidden ref and
// describes it: how much it holds, and how it stands against the local
// history. Nothing in the working tree is touched, so an unreachable
// remote fails harmlessly here.
func (s *Server) handleGitRestorePreview(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	remote, ok := s.restoreRemote(w, r)
	if !ok {
		return
	}
	commit, err := s.Git.FetchRestore(r.Context(), remote)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	preview, err := s.Git.PreviewRestore(r.Context(), commit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preview": preview})
}

// handleGitRestore moves the tree to a backup: fetch and reset, never a
// merge. The owner confirms by typing the remote's name; the pre-restore
// state is committed and tagged, restored files reach open editors as
// edits through the reconciliation loop, and a full scan rebuilds the
// index, search, tasks and links. .sync/ and .trash/ are gitignored and
// stay put — accounts, sessions and public links belong to this server
// and survive the restore.
func (s *Server) handleGitRestore(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON with a confirm field")
		return
	}
	remote, ok := s.restoreRemote(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(body.Confirm) != remote.Name {
		writeError(w, http.StatusBadRequest, "type the remote's name ("+remote.Name+") to confirm the restore")
		return
	}
	// The restore runs to completion even if the caller walks away
	// halfway through; a reset abandoned mid-flight is a half-restored
	// tree.
	ctx := context.WithoutCancel(r.Context())
	commit, err := s.Git.FetchRestore(ctx, remote)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	res, err := s.Git.Restore(ctx, commit, func(ctx context.Context, paths []git.RestorePath) {
		if s.Sync == nil {
			return
		}
		for _, p := range paths {
			if _, err := s.Root.Clean(p.Path); err != nil {
				continue // the scan applies the same path rules
			}
			s.Sync.Sync(ctx, p.Path)
		}
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
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
		"added": res.Added, "changed": res.Changed, "deleted": res.Deleted,
	})
}
