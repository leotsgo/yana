// The first-run choice: once the owner account exists, a server whose
// notes folder holds nothing either starts an empty notebook (the guide)
// or restores the folder from a git backup in the background.
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/leotsgo/yana/internal/git"
	"github.com/leotsgo/yana/internal/index"
)

// RestoreStatus is where the setup restore stands. Phase is connecting,
// fetching, indexing, done or failed; Notes is the index's live note
// count while indexing.
type RestoreStatus struct {
	Phase   string `json:"phase"`
	URL     string `json:"url"`
	HostKey string `json:"host_key"`
	Commits int64  `json:"commits"`
	Notes   int    `json:"notes"`
	Error   string `json:"error"`
}

const (
	phaseIndexing = "indexing"
	phaseDone     = "done"
	phaseFailed   = "failed"
)

const (
	msgRestoreRunning = "A restore is already running."
	msgHoldsNotes     = "The notes folder already holds notes."
)

// restoreJob is the one setup restore this server runs at a time; st is
// nil until one has started.
type restoreJob struct {
	mu sync.Mutex
	st *RestoreStatus
}

// running reports whether a restore is under way. Hold mu.
func (j *restoreJob) running() bool {
	return j.st != nil && j.st.Phase != phaseDone && j.st.Phase != phaseFailed
}

func (j *restoreJob) update(f func(*RestoreStatus)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	f(j.st)
}

// restoreStatus copies the current status with its live fields filled
// in, or nil when no restore ever ran.
func (s *Server) restoreStatus(ctx context.Context) *RestoreStatus {
	s.restore.mu.Lock()
	if s.restore.st == nil {
		s.restore.mu.Unlock()
		return nil
	}
	st := *s.restore.st
	s.restore.mu.Unlock()
	st.HostKey = s.Git.HostKey(st.URL)
	if st.Phase == phaseIndexing || st.Phase == phaseDone {
		st.Notes, _, _ = s.DB.Counts(ctx)
	}
	return &st
}

// handleSetupState (GET /api/setup) tells the setup screen whether there
// is a choice to make and how a restore is going.
func (s *Server) handleSetupState(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	empty, err := s.Git.HoldsNoNotes()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"empty": empty, "restore": s.restoreStatus(r.Context())})
}

// handleSetupEmpty (POST /api/setup/empty) starts an empty notebook: the
// guide, written only into a folder that holds no notes.
func (s *Server) handleSetupEmpty(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	// Under the job lock, so a restore cannot start between the check
	// and the guide landing.
	s.restore.mu.Lock()
	defer s.restore.mu.Unlock()
	if s.restore.running() {
		writeError(w, http.StatusConflict, msgRestoreRunning)
		return
	}
	empty, err := s.Git.HoldsNoNotes()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !empty {
		writeError(w, http.StatusConflict, msgHoldsNotes)
		return
	}
	rel, _, err := s.seedGuide(r.Context(), "")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": rel})
}

// handleSetupRestore (POST /api/setup/restore, {url, sealed_key}) starts
// restoring the empty notes folder from a backup and answers at once;
// the setup screen polls GET /api/setup/restore.
func (s *Server) handleSetupRestore(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	var body struct {
		URL       string `json:"url"`
		SealedKey string `json:"sealed_key"`
	}
	if err := decodeBody(w, r, &body); err != nil {
		return
	}
	clean, err := git.ParseRemoteURL(body.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	remote := index.GitRemote{
		ID: git.NewRemoteID(), URL: clean, Schedule: git.ScheduleNightly, PushHour: 2, Enabled: true,
		CreatedAt: time.Now().UTC(),
	}
	switch {
	case git.IsSSH(clean) && body.SealedKey == "":
		writeError(w, http.StatusBadRequest, "make a key first and add its public half to the repository")
		return
	case !git.IsSSH(clean) && body.SealedKey != "":
		writeError(w, http.StatusBadRequest, "a folder on this machine needs no key")
		return
	case body.SealedKey != "":
		sealed, pub, err := s.Git.OpenKey(body.SealedKey)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		remote.Secret, remote.PublicKey = sealed, pub
	}

	s.restore.mu.Lock()
	if s.restore.running() {
		s.restore.mu.Unlock()
		writeError(w, http.StatusConflict, msgRestoreRunning)
		return
	}
	empty, err := s.Git.HoldsNoNotes()
	if err != nil {
		s.restore.mu.Unlock()
		s.fail(w, r, err)
		return
	}
	if !empty {
		s.restore.mu.Unlock()
		writeError(w, http.StatusConflict, msgHoldsNotes)
		return
	}
	s.restore.st = &RestoreStatus{Phase: string(git.PhaseConnecting), URL: clean}
	s.restore.mu.Unlock()

	// The job outlives the request; the server's base context ends it
	// on shutdown, and an unfinished bootstrap is undone on next start.
	go s.runRestore(s.base(), remote)
	writeJSON(w, http.StatusAccepted, s.restoreStatus(r.Context()))
}

// handleSetupRestoreStatus (GET /api/setup/restore) reports the restore.
func (s *Server) handleSetupRestoreStatus(w http.ResponseWriter, r *http.Request) {
	if !s.gitOwnerOnly(w, r) {
		return
	}
	st := s.restoreStatus(r.Context())
	if st == nil {
		writeError(w, http.StatusNotFound, "no restore has run on this server")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// runRestore is the background job: bootstrap, save the backup as a
// remote, take the restored tree on, index it.
func (s *Server) runRestore(ctx context.Context, remote index.GitRemote) {
	fail := func(err error) {
		msg := git.ExplainGitError(remote.URL, err)
		s.Log.Warn("restore from a backup failed", "url", git.RedactURL(remote.URL), "err", msg)
		s.restore.update(func(st *RestoreStatus) { st.Phase, st.Error = phaseFailed, msg })
	}
	// Between Bootstrap and FinishBootstrap a failure puts the folder
	// back the way it was.
	abort := func(err error) {
		if aerr := s.Git.AbortBootstrap(); aerr != nil {
			s.Log.Error("could not undo a failed restore", "err", aerr)
		}
		fail(err)
	}

	commits, err := s.Git.Bootstrap(ctx, remote, func(p git.BootstrapPhase) {
		s.restore.update(func(st *RestoreStatus) { st.Phase = string(p) })
	})
	if err != nil {
		fail(err)
		return
	}
	s.restore.update(func(st *RestoreStatus) { st.Commits = commits })
	if err := s.createBackupRemote(ctx, &remote); err != nil {
		abort(err)
		return
	}
	if err := s.Git.FinishBootstrap(); err != nil {
		if derr := s.DB.DeleteGitRemote(context.WithoutCancel(ctx), remote.ID); derr != nil {
			s.Log.Error("could not remove the remote of a failed restore", "err", derr)
		}
		abort(err)
		return
	}
	s.Git.Reload(ctx)

	s.restore.update(func(st *RestoreStatus) { st.Phase = phaseIndexing })
	if s.Scanner != nil {
		if _, err := s.Scanner.Scan(ctx); err != nil {
			fail(err)
			return
		}
	}
	s.restore.update(func(st *RestoreStatus) { st.Phase = phaseDone })
}

// createBackupRemote saves the restored-from backup as the remote
// "backup", or "backup 2", "backup 3"… when the name is taken.
func (s *Server) createBackupRemote(ctx context.Context, remote *index.GitRemote) error {
	for i := 1; ; i++ {
		remote.Name = "backup"
		if i > 1 {
			remote.Name = fmt.Sprintf("backup %d", i)
		}
		err := s.DB.CreateGitRemote(ctx, *remote)
		if !errors.Is(err, index.ErrGitRemoteNameTaken) {
			return err
		}
	}
}
