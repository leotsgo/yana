// Bringing a backup back: clone on first run, and restore from a
// configured remote. The inverse of the nightly push.
//
// A clone happens inside Ensure, before the first scan: a root that has
// never held notes (a fresh volume) and YANA_GIT_REMOTE set is filled
// from that remote, through the same inline credential helper a push
// uses, so a token never reaches a URL, a command line or a log line.
//
// A restore is owner-driven from settings. The remote's HEAD is fetched
// into a hidden ref (nothing in the working tree moves), described — how
// much it holds, where it stands against the local history — and only
// then, once the owner has typed the remote's name, applied: the
// pre-restore state is committed and tagged, and the branch and the
// working tree are reset to the fetched commit. Always fetch and reset,
// never a merge or a pull: a root that was re-initialised after the
// backup has no common ancestor with it.
//
// .sync/ and .trash/ are gitignored, and a reset never touches ignored
// files: accounts, sessions, the auth and content secrets, the sealed
// remote credentials and the trash belong to this server, not to the
// backup.
package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

const (
	// RestoreRef is the hidden ref a backup is fetched into. It is not a
	// branch, so nothing lists it, builds on it, or pushes it.
	RestoreRef = "refs/yana/restore"
	// emptyTree is git's well-known empty tree, the diff base when the
	// repository has no commits yet.
	emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
	// restoreTimeout bounds one restore fetch.
	restoreTimeout = 10 * time.Minute
)

// --- clone on first run ------------------------------------------------------

// cloneIfEmpty brings YANA_GIT_REMOTE down into a root that has never held
// notes: nothing but .sync/, .trash/ and dotfiles, the state of a fresh
// volume. The clone is init + fetch + reset rather than git clone, so
// those pre-existing directories do not block it the way they block git
// clone. It reports whether the root now holds the backup's tree. A root
// that already holds notes is never cloned over; a clone that fails
// leaves the root as it was and reports the error, so the server starts
// the way it does without a git binary and the next start retries.
func (l *Layer) cloneIfEmpty(ctx context.Context) (bool, error) {
	if l.opts.Remote == "" {
		return false, nil
	}
	empty, err := l.rootEmptyOfNotes()
	if err != nil {
		l.log.Warn("could not read the notes root; running git init", "err", err)
		return false, nil
	}
	if !empty {
		l.log.Info("the notes root already holds notes; initialising a repository instead of cloning YANA_GIT_REMOTE")
		return false, nil
	}
	clean, username, password, err := ParseRemoteURL(l.opts.Remote)
	if err != nil {
		l.log.Warn("YANA_GIT_REMOTE is not a usable remote; running git init", "err", err)
		return false, nil
	}
	config, env, err := l.credentialEnv(index.GitRemote{ID: "env", Name: "remote", URL: clean, Enabled: true})
	if err != nil {
		return false, l.cloneFailed(err)
	}
	if password != "" {
		hc, he := helperEnv(username, password)
		config, env = append(config, hc...), append(env, he...)
	}
	pctx, pcancel := context.WithTimeout(ctx, time.Minute)
	defer pcancel()
	// An empty backup is the fresh-install case, not a failure: the
	// server keeps its own history and the pushes fill the remote.
	heads, err := l.gitEnv(pctx, config, env, "ls-remote", "--heads", "--", clean)
	if err != nil {
		return false, l.cloneFailed(fmt.Errorf("reach %s: %w", RedactURL(clean), err))
	}
	if strings.TrimSpace(heads) == "" {
		l.log.Info("YANA_GIT_REMOTE is reachable and empty; initialising a repository instead of cloning", "url", RedactURL(clean))
		return false, nil
	}
	if _, err := l.git(pctx, "-c", "init.defaultBranch=main", "init"); err != nil {
		return false, l.cloneFailed(fmt.Errorf("git init: %w", err))
	}
	fctx, cancel := context.WithTimeout(ctx, restoreTimeout)
	defer cancel()
	if _, err := l.gitEnv(fctx, config, env, "fetch", "--no-tags", "--", clean, "+HEAD:"+RestoreRef); err != nil {
		return false, l.cloneFailed(fmt.Errorf("clone from %s: %w", RedactURL(clean), err))
	}
	if _, err := l.git(fctx, "reset", "--hard", RestoreRef); err != nil {
		return false, l.cloneFailed(fmt.Errorf("check out the backup: %w", err))
	}
	l.log.Info("cloned YANA_GIT_REMOTE into the notes root", "url", RedactURL(clean))
	return true, nil
}

// cloneFailed undoes a partial clone — the root goes back to holding
// nothing, so the next start tries again — and wraps the error for
// Ensure to report: an unreachable backup must not make the app
// unbootable, but history is unavailable for this run.
func (l *Layer) cloneFailed(err error) error {
	l.log.Error("could not clone YANA_GIT_REMOTE; starting without git history and retrying on the next start", "err", err)
	if rmErr := os.RemoveAll(filepath.Join(l.root, ".git")); rmErr != nil {
		l.log.Warn("could not remove the partial clone", "err", rmErr)
	}
	return err
}

// rootEmptyOfNotes reports whether the root holds nothing but .sync/,
// .trash/ and dotfiles.
func (l *Layer) rootEmptyOfNotes() (bool, error) {
	entries, err := os.ReadDir(l.root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return false, nil
		}
	}
	return true, nil
}

// --- restore -----------------------------------------------------------------

// FetchRestore fetches the remote's HEAD into the hidden ref and returns
// the commit it landed on. The working tree is not touched; an
// unreachable remote fails here, before anything is at stake.
func (l *Layer) FetchRestore(ctx context.Context, r index.GitRemote) (string, error) {
	if !l.available {
		return "", errors.New("git history is unavailable")
	}
	config, env, err := l.credentialEnv(r)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, restoreTimeout)
	defer cancel()
	if _, err := l.gitEnv(ctx, config, env, "fetch", "--no-tags", "--", r.URL, "+HEAD:"+RestoreRef); err != nil {
		return "", fmt.Errorf("fetch %s: %w", r.Name, err)
	}
	out, err := l.git(ctx, "rev-parse", "--verify", RestoreRef)
	if err != nil {
		return "", errors.New("the backup holds no history")
	}
	return strings.TrimSpace(out), nil
}

// RestorePreview describes a backup before anything is touched.
type RestorePreview struct {
	// Commit is the backup's newest commit, where a restore lands.
	Commit string `json:"commit"`
	// Commits is how many commits the backup's history holds.
	Commits int64 `json:"commits"`
	// Notes is how many note files its tree holds.
	Notes int `json:"notes"`
	// Relation says how the backup stands against the local history:
	// identical, ahead, behind, or diverged.
	Relation string `json:"relation"`
	// Newest is the backup's newest commit.
	Newest LogEntry `json:"newest"`
}

// PreviewRestore describes what restoring to ref would do. Nothing is
// touched.
func (l *Layer) PreviewRestore(ctx context.Context, ref string) (RestorePreview, error) {
	if !l.available {
		return RestorePreview{}, errors.New("git history is unavailable")
	}
	if !ValidRevision(ref) {
		return RestorePreview{}, errors.New("restore point must be a commit hash")
	}
	p := RestorePreview{Commit: ref}
	if out, err := l.git(ctx, "rev-list", "--count", ref); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64); err == nil {
			p.Commits = n
		}
	}
	if out, err := l.git(ctx, "log", "-1", "--format=%H%x1f%an%x1f%ae%x1f%aI%x1f%s", ref); err == nil {
		if parts := strings.SplitN(strings.TrimSpace(out), "\x1f", 5); len(parts) == 5 {
			p.Newest = LogEntry{Hash: parts[0], Name: parts[1], Email: parts[2], Date: parts[3], Subject: parts[4], Kind: AuthorKind(parts[1], parts[2])}
		}
	}
	if out, err := l.git(ctx, "ls-tree", "-r", "--name-only", "-z", ref); err == nil {
		for _, f := range strings.Split(out, "\x00") {
			if scanner.KindOf(f) != "" {
				p.Notes++
			}
		}
	}
	head, err := l.git(ctx, "rev-parse", "--verify", "HEAD")
	switch {
	case err != nil:
		p.Relation = "ahead" // no local history; the backup holds all of it
	case strings.TrimSpace(head) == ref:
		p.Relation = "identical"
	case l.isAncestor(ctx, ref, strings.TrimSpace(head)):
		p.Relation = "behind"
	case l.isAncestor(ctx, strings.TrimSpace(head), ref):
		p.Relation = "ahead"
	default:
		p.Relation = "diverged"
	}
	return p, nil
}

// isAncestor reports whether rev is an ancestor of descendant; histories
// that share no ancestor answer false.
func (l *Layer) isAncestor(ctx context.Context, rev, descendant string) bool {
	_, err := l.git(ctx, "merge-base", "--is-ancestor", rev, descendant)
	return err == nil
}

// RestorePath is one path a restore changed: added (A), changed (M, or a
// rename R with Orig holding the previous path), or deleted (D).
type RestorePath struct {
	Status byte
	Path   string
	Orig   string
}

// RestoreResult reports what a restore did.
type RestoreResult struct {
	// Commit is HEAD after the restore: the backup's newest commit.
	Commit string `json:"commit"`
	// Tag names the pre-restore state; empty when there was no local
	// history to tag.
	Tag     string `json:"tag"`
	Added   int    `json:"added"`
	Changed int    `json:"changed"`
	Deleted int    `json:"deleted"`
}

// Restore moves the working tree to ref. Pending edits are flushed and
// committed first and the pre-restore state is tagged
// pre-restore/<timestamp>, so it is findable without the reflog; then the
// branch and the working tree are reset to ref — never merged. apply,
// when set, runs after the tree has moved, still serialised against the
// commit loop, and receives every path the restore changed: the caller
// drives the reconciliation loop over them so open documents converge
// instead of fighting the write-back.
func (l *Layer) Restore(ctx context.Context, ref string, apply func(ctx context.Context, paths []RestorePath)) (RestoreResult, error) {
	if !l.available {
		return RestoreResult{}, errors.New("git history is unavailable")
	}
	if !ValidRevision(ref) {
		return RestoreResult{}, errors.New("restore point must be a commit hash")
	}
	// Snapshot what is here now so the pre-restore state is a commit;
	// commitPending takes the committing lock itself.
	if _, err := l.commitPending(ctx, "snapshot"); err != nil {
		return RestoreResult{}, err
	}
	// The move itself runs alone: no window commit, no push, interleaved
	// with a half-restored tree.
	l.committing.Lock()
	defer l.committing.Unlock()
	var res RestoreResult
	base := emptyTree
	if out, err := l.git(ctx, "rev-parse", "--verify", "HEAD"); err == nil {
		base = strings.TrimSpace(out)
		res.Tag = "pre-restore/" + l.opts.Now().Format("20060102-150405")
		if _, err := l.git(ctx, "tag", res.Tag); err != nil {
			l.log.Warn("could not tag the pre-restore state", "err", err)
			res.Tag = ""
		}
	}
	if _, err := l.git(ctx, "reset", "--hard", ref); err != nil {
		return res, err
	}
	// The ignore file is what keeps .sync/ and .trash/ out of the
	// history; re-assert it whatever the backup's tree says, so no later
	// window can commit them.
	if err := l.ensureIgnore(); err != nil {
		l.log.Warn("could not re-write .gitignore after the restore", "err", err)
	}
	var paths []RestorePath
	if out, err := l.git(ctx, "diff", "--name-status", "-z", "-M", base, ref); err == nil {
		fields := strings.Split(out, "\x00")
		for i := 0; i < len(fields); i++ {
			st := fields[i]
			if st == "" {
				continue
			}
			p := RestorePath{Status: st[0]}
			switch p.Status {
			case 'R', 'C':
				// A rename or copy lists the original path before the
				// new one, each its own NUL-separated field.
				if i+2 < len(fields) {
					p.Orig, p.Path = fields[i+1], fields[i+2]
					i += 2
				}
			default:
				if i+1 < len(fields) {
					p.Path = fields[i+1]
					i++
				}
			}
			if p.Path == "" {
				continue
			}
			switch p.Status {
			case 'A':
				res.Added++
			case 'D':
				res.Deleted++
			case 'M', 'R', 'C':
				res.Changed++
			}
			paths = append(paths, p)
		}
	}
	if out, err := l.git(ctx, "rev-parse", "--verify", "HEAD"); err == nil {
		res.Commit = strings.TrimSpace(out)
	}
	// The counters describe the repository, and a restore is work the
	// other remotes have not seen.
	now := l.opts.Now()
	l.mu.Lock()
	l.commits = l.commitCount(ctx)
	l.lastCommit = now
	l.lastMade = now
	l.mu.Unlock()
	l.saveWindowState(now)
	if apply != nil {
		apply(ctx, paths)
	}
	l.log.Info("restored from a backup", "commit", res.Commit, "tag", res.Tag, "added", res.Added, "changed", res.Changed, "deleted", res.Deleted)
	return res, nil
}
