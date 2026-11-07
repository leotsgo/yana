// Bringing a backup back: bootstrap an empty root, and restore from a
// configured remote. The inverse of the nightly push.
//
// A bootstrap is owner-driven from the setup screen of a fresh server: a
// root that holds no notes is filled from the remote the owner pasted,
// through the same per-remote key a push uses, so a key never reaches a
// URL, a command line or a log line. A marker under .sync makes it
// crash-safe: a bootstrap the server never finished is undone on start.
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
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leotsgo/yana/internal/fsutil"
	"github.com/leotsgo/yana/internal/index"
	"github.com/leotsgo/yana/internal/scanner"
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

// --- bootstrap ---------------------------------------------------------------

// BootstrapPhase names the step a bootstrap is on, for the setup screen.
type BootstrapPhase string

const (
	// PhaseConnecting is the reachability check (ls-remote).
	PhaseConnecting BootstrapPhase = "connecting"
	// PhaseFetching is the download of the backup's history.
	PhaseFetching BootstrapPhase = "fetching"
)

var (
	// ErrNotEmpty refuses a bootstrap over a root that holds notes.
	ErrNotEmpty = errors.New("the notes folder already holds notes")
	// ErrEmptyBackup is a reachable remote with no branch to restore.
	ErrEmptyBackup = errors.New("That repository is empty; there is nothing to restore.")
)

// markerPath is the bootstrap's crash marker: present from just before the
// working tree moves until the server has taken the backup on
// (FinishBootstrap). It holds the HEAD hash from before the bootstrap, or
// nothing when the branch was unborn.
func (l *Layer) markerPath() string {
	return filepath.Join(l.root, ".sync", "restore-pending")
}

// HoldsNoNotes reports whether the root holds no note and no asset, at any
// depth outside dot-directories. Junk a fresh volume carries (lost+found,
// .sync, a .gitignore) does not count.
func (l *Layer) HoldsNoNotes() (bool, error) {
	errFound := errors.New("found")
	err := filepath.WalkDir(l.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory (lost+found on a volume mounted for
			// another user) holds nothing this server could serve.
			if p != l.root && errors.Is(err, fs.ErrPermission) {
				return fs.SkipDir
			}
			return err
		}
		if p == l.root {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(l.root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if scanner.KindOf(rel) != "" || scanner.IsAsset(rel) {
			return errFound
		}
		return nil
	})
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, errFound):
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		return true, nil
	}
	return false, err
}

// Bootstrap fills a root that holds no notes from r: the remote's HEAD is
// fetched into the hidden ref, then the branch, the index and the working
// tree move to it. Only paths in the backup's tree are written — nothing
// else in the root is touched, tracked or not — so a rollback can undo
// exactly what the bootstrap brought.
//
// It is crash-safe: the marker is written before the tree moves and
// stays until FinishBootstrap, so a bootstrap the server never finished
// taking on is undone on the next start (recoverBootstrap) and the setup
// screen comes back over an empty root. Any failure after the marker
// rolls back at once. It runs under the committing lock, so no window
// commits a half-written tree. It returns the backup's commit count.
func (l *Layer) Bootstrap(ctx context.Context, r index.GitRemote, phase func(BootstrapPhase)) (commits int64, err error) {
	if !l.available {
		return 0, errors.New("git history is unavailable")
	}
	if phase == nil {
		phase = func(BootstrapPhase) {}
	}
	l.committing.Lock()
	defer l.committing.Unlock()
	empty, err := l.HoldsNoNotes()
	if err != nil {
		return 0, err
	}
	if !empty {
		return 0, ErrNotEmpty
	}
	config, env, cleanup, err := l.remoteEnv(r)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	phase(PhaseConnecting)
	pctx, pcancel := context.WithTimeout(ctx, time.Minute)
	defer pcancel()
	heads, err := l.gitEnv(pctx, config, env, "ls-remote", "--heads", "--", r.URL)
	if err != nil {
		return 0, fmt.Errorf("reach %s: %w", RedactURL(r.URL), err)
	}
	if strings.TrimSpace(heads) == "" {
		return 0, ErrEmptyBackup
	}

	phase(PhaseFetching)
	fctx, fcancel := context.WithTimeout(ctx, restoreTimeout)
	defer fcancel()
	if _, err := l.gitEnv(fctx, config, env, "fetch", "--no-tags", "--", r.URL, "+HEAD:"+RestoreRef); err != nil {
		return 0, fmt.Errorf("fetch %s: %w", RedactURL(r.URL), err)
	}

	// Every path the backup brings must be new: the checkout then
	// overwrites nothing and a rollback removes only what it wrote.
	if err := l.checkFetchedTree(ctx); err != nil {
		return 0, err
	}
	prev := ""
	if out, err := l.git(ctx, "rev-parse", "--verify", "HEAD"); err == nil {
		prev = strings.TrimSpace(out)
	}
	if err := fsutil.MkdirInherit(filepath.Dir(l.markerPath())); err != nil {
		return 0, err
	}
	if err := fsutil.WriteFileAtomic(l.markerPath(), []byte(prev), 0o644); err != nil {
		return 0, err
	}
	if err := l.moveTo(ctx); err != nil {
		if rerr := l.rollbackBootstrap(prev); rerr != nil {
			l.log.Error("could not roll back a failed restore", "err", rerr)
		}
		return 0, fmt.Errorf("check out the backup: %w", err)
	}
	commits = l.commitCount(ctx)
	l.mu.Lock()
	l.commits = commits
	l.mu.Unlock()
	l.log.Info("restored an empty notes folder from a backup", "url", RedactURL(r.URL), "commits", commits)
	return commits, nil
}

// fetchedPaths lists every file in the fetched backup's tree.
func (l *Layer) fetchedPaths(ctx context.Context) ([]string, error) {
	out, err := l.git(ctx, "ls-tree", "-r", "--name-only", "-z", RestoreRef)
	if err != nil {
		return nil, fmt.Errorf("list the restored tree: %w", err)
	}
	var paths []string
	for _, rel := range strings.Split(out, "\x00") {
		if rel != "" {
			paths = append(paths, rel)
		}
	}
	return paths, nil
}

// treePathOK reports whether a path from a backup's tree may be written:
// a clean relative path that stays in the root and is not under .git,
// .sync or .trash, which belong to this server. A crafted tree can hold
// any of those; git fetch does not refuse them.
func treePathOK(rel string) bool {
	if !fs.ValidPath(rel) || rel == "." {
		return false
	}
	first, _, _ := strings.Cut(rel, "/")
	for _, own := range []string{".git", ".sync", ".trash"} {
		if strings.EqualFold(first, own) {
			return false
		}
	}
	return true
}

// checkFetchedTree refuses a backup whose tree holds a path that may not
// be written, a path that already exists on disk, or a path whose parent
// exists as something other than a directory (a file or a symlink in the
// way). An existing directory above a path is fine — a leftover empty
// journal/ must not block a restore. Only .gitignore, which the server
// owns, may already be there.
func (l *Layer) checkFetchedTree(ctx context.Context) error {
	paths, err := l.fetchedPaths(ctx)
	if err != nil {
		return err
	}
	files := map[string]bool{}
	for _, rel := range paths {
		files[rel] = true
	}
	for _, rel := range paths {
		if !treePathOK(rel) {
			return fmt.Errorf("the backup holds %q, which cannot be restored", rel)
		}
		if rel == ".gitignore" {
			continue
		}
		// Top down, so a file in the way is found before a lookup
		// beneath it fails with "not a directory".
		segs := strings.Split(rel, "/")
		for i := range segs {
			p := strings.Join(segs[:i+1], "/")
			// A file that is also a directory above another file is a
			// crafted tree, not a backup.
			if p != rel && files[p] {
				return fmt.Errorf("the backup holds %q as both a file and a folder", p)
			}
			fi, err := os.Lstat(filepath.Join(l.root, filepath.FromSlash(p)))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if p == rel || !fi.IsDir() {
				return fmt.Errorf("the notes folder holds %q, which the backup also has; move it away and try again", p)
			}
		}
	}
	return nil
}

// realParents reports whether every directory above rel is a real
// directory, not a symlink that would lead a removal out of the root.
func (l *Layer) realParents(rel string) bool {
	for d := path.Dir(rel); d != "."; d = path.Dir(d) {
		fi, err := os.Lstat(filepath.Join(l.root, filepath.FromSlash(d)))
		if err != nil || !fi.IsDir() {
			return false
		}
	}
	return true
}

// moveTo points the branch and the index at the fetched backup, writes
// its files, and re-asserts the ignore file. A mixed reset moves the
// branch and the index; checkout-index then writes the index's files.
// Unlike reset --hard it never deletes a file the previous commit
// tracked and the backup lacks.
func (l *Layer) moveTo(ctx context.Context) error {
	if _, err := l.git(ctx, "reset", "-q", RestoreRef, "--"); err != nil {
		return err
	}
	if _, err := l.git(ctx, "checkout-index", "-a", "-f"); err != nil {
		return err
	}
	return l.ensureIgnore()
}

// FinishBootstrap marks a bootstrap as taken on: from here a restart
// keeps the restored tree.
func (l *Layer) FinishBootstrap() error {
	if err := os.Remove(l.markerPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// rollbackBootstrap undoes a bootstrap: every file in the fetched tree is
// removed, then the directories that leaves empty, and the branch goes
// back to prev (or is deleted when prev is empty, the branch unborn
// again) with the index to match. Files the backup did not bring —
// untracked or tracked before — are never touched.
func (l *Layer) rollbackBootstrap(prev string) error {
	if prev != "" && !ValidRevision(prev) {
		return errors.New("the restore marker does not name a commit")
	}
	ctx := context.Background()
	paths, err := l.fetchedPaths(ctx)
	if err != nil {
		return err
	}
	dirs := map[string]bool{}
	for _, rel := range paths {
		// Bootstrap refused such trees before writing anything; this
		// holds even for a ref that changed under a pending marker.
		if !treePathOK(rel) || !l.realParents(rel) {
			continue
		}
		if err := os.Remove(filepath.Join(l.root, filepath.FromSlash(rel))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		for d := path.Dir(rel); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	// Deepest first, so a parent is empty by the time it is tried; a
	// directory that still holds something stays, and the root is never
	// in the list.
	// ponytail: an empty directory that existed before the bootstrap
	// (a leftover journal/) is pruned too; record pre-existing dirs in
	// the marker if that ever matters.
	ordered := make([]string, 0, len(dirs))
	for d := range dirs {
		ordered = append(ordered, d)
	}
	sort.Slice(ordered, func(i, j int) bool { return strings.Count(ordered[i], "/") > strings.Count(ordered[j], "/") })
	for _, d := range ordered {
		_ = os.Remove(filepath.Join(l.root, filepath.FromSlash(d)))
	}
	if prev != "" {
		if _, err := l.git(ctx, "reset", "-q", prev, "--"); err != nil {
			return err
		}
	} else {
		if _, err := l.git(ctx, "update-ref", "-d", "HEAD"); err != nil {
			return err
		}
		if _, err := l.git(ctx, "read-tree", "--empty"); err != nil {
			return err
		}
	}
	// The backup may have held its own .gitignore, just removed.
	if err := l.ensureIgnore(); err != nil {
		return err
	}
	count := l.commitCount(ctx)
	l.mu.Lock()
	l.commits = count
	l.mu.Unlock()
	return l.FinishBootstrap()
}

// AbortBootstrap undoes a bootstrap that has not been finished, exactly
// as a restart would; the server calls it when it cannot take the
// restored tree on. Without a pending bootstrap it does nothing.
func (l *Layer) AbortBootstrap() error {
	l.committing.Lock()
	defer l.committing.Unlock()
	prev, err := os.ReadFile(l.markerPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return l.rollbackBootstrap(strings.TrimSpace(string(prev)))
}

// recoverBootstrap runs from Ensure: a marker left behind is a bootstrap
// the server never finished taking on, and it is undone.
func (l *Layer) recoverBootstrap() {
	if _, err := os.Stat(l.markerPath()); err != nil {
		return
	}
	if err := l.AbortBootstrap(); err != nil {
		l.log.Error("could not undo an interrupted restore", "err", err)
		return
	}
	l.log.Warn("a restore was interrupted; the notes folder is empty again")
}

// --- restore -----------------------------------------------------------------

// FetchRestore fetches the remote's HEAD into the hidden ref and returns
// the commit it landed on. The working tree is not touched; an
// unreachable remote fails here, before anything is at stake.
func (l *Layer) FetchRestore(ctx context.Context, r index.GitRemote) (string, error) {
	if !l.available {
		return "", errors.New("git history is unavailable")
	}
	config, env, cleanup, err := l.remoteEnv(r)
	if err != nil {
		return "", err
	}
	defer cleanup()
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
	// Moved counts renames; point-in-time restores report it, backup
	// restores leave it at zero.
	Moved int `json:"moved"`
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
