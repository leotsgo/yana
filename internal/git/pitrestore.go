// Point-in-time restore: bringing the tree, or one space of it, back to
// how a commit found it — the undo for a bad sync, a script that rewrote
// fifty files, an agent that deleted a folder.
//
// Unlike a backup restore (reset --hard onto a fetched ref) a
// point-in-time restore never moves the branch: the diff from HEAD to
// the chosen commit is applied to the working tree and committed as new
// work, so the history stays linear and honest — the feed reads "so-and-
// so restored this to that commit" rather than as a mysterious bulk edit.
//
// The same safety rails as a backup restore: pending edits are flushed
// and committed first, the pre-restore state is tagged pre-restore/
// <timestamp> so it is reachable without the reflog, the touched paths
// are handed to the reconciliation loop so open editors converge, and
// files the restore removes are moved under .trash by the caller rather
// than deleted — a restore is itself undoable.
package git

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
)

// ErrNoteNotInHistory is returned when no revision of a path holds the
// note the caller asked for.
var ErrNoteNotInHistory = errors.New("the history does not hold that note")

// PITChange is one path a point-in-time restore would touch. Status is
// the diff letter from the current state to the target commit: A the
// file returns, M its content returns, D it goes, R it moves back from
// Orig to Path.
type PITChange struct {
	Status byte
	Path   string
	Orig   string // the path the file holds now; renames only
}

// PITPreview describes exactly what restoring to a commit would do.
// Nothing is touched.
type PITPreview struct {
	// Commit is the target commit's full hash.
	Commit string `json:"commit"`
	// Subject, Author and Date describe the target commit, so a
	// preview can say what moment it restores to.
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Date    string `json:"date"`
	// Space is the scope the preview was computed for ("" is the tree).
	Space string `json:"space"`
	// Changes lists every path, ordered by path, with the counts
	// already tallied.
	Changes []PITChange `json:"changes"`
	Added   int         `json:"added"`
	Changed int         `json:"changed"`
	Deleted int         `json:"deleted"`
	Moved   int         `json:"moved"`
}

// PITAuthor names who ran a restore, so the commit it lands is theirs.
type PITAuthor struct{ Name, Email string }

// PITApply is what a point-in-time restore does around git's own
// checkout: the parts that belong to the caller's machinery.
type PITApply struct {
	// Trash moves one existing working-tree file out of the tree. The
	// restore removes the file, but it must not vanish: this is what
	// makes a restore undoable.
	Trash func(ctx context.Context, rel string) error
	// Sync reports a path the reconciliation loop should look at now —
	// written, returned, or removed — so open documents converge.
	Sync func(ctx context.Context, rel string)
}

// PreviewPIT describes what restoring to ref would do, optionally
// inside one top-level directory (scope; "" is the whole tree).
func (l *Layer) PreviewPIT(ctx context.Context, ref, scope string) (PITPreview, error) {
	if !l.available {
		return PITPreview{}, errors.New("git history is unavailable")
	}
	if err := l.verifyCommit(ctx, ref); err != nil {
		return PITPreview{}, err
	}
	p := PITPreview{Commit: ref, Space: scope}
	if out, err := l.git(ctx, "log", "-1", "--format=%s%x1f%an%x1f%aI", ref); err == nil {
		if parts := strings.SplitN(strings.TrimSpace(out), "\x1f", 3); len(parts) == 3 {
			p.Subject, p.Author, p.Date = parts[0], parts[1], parts[2]
		}
	}
	base := l.headOrEmpty(ctx)
	changes, err := l.pitPlan(ctx, base, ref, scope)
	if err != nil {
		return PITPreview{}, err
	}
	p.Changes, p.Added, p.Changed, p.Deleted, p.Moved = tally(changes)
	return p, nil
}

// RestoreTo applies the state ref found, inside scope ("" is the whole
// tree), to the working tree: pending work is committed, the
// pre-restore state tagged, removed files handed to Trash, returned
// files checked out of ref, and everything committed as one commit
// authored by who. The plan is recomputed after the snapshot, so what
// the result reports is what was done, whatever moved between a preview
// and the restore itself.
func (l *Layer) RestoreTo(ctx context.Context, ref, scope string, who PITAuthor, apply PITApply) (RestoreResult, error) {
	if !l.available {
		return RestoreResult{}, errors.New("git history is unavailable")
	}
	if err := l.verifyCommit(ctx, ref); err != nil {
		return RestoreResult{}, err
	}
	// Snapshot what is here now, so the restore starts from a committed
	// state and its diff is the whole story.
	if _, err := l.commitPending(ctx, "snapshot"); err != nil {
		return RestoreResult{}, err
	}
	l.committing.Lock()
	defer l.committing.Unlock()

	base := l.headOrEmpty(ctx)
	changes, err := l.pitPlan(ctx, base, ref, scope)
	if err != nil {
		return RestoreResult{}, err
	}
	res := RestoreResult{Commit: base}
	res.Added, res.Changed, res.Deleted, res.Moved = tallyCounts(changes)

	if len(changes) > 0 {
		res.Tag = "pre-restore/" + l.opts.Now().Format("20060102-150405")
		if _, err := l.git(ctx, "tag", res.Tag); err != nil {
			l.log.Warn("could not tag the pre-restore state", "err", err)
			res.Tag = ""
		}
	}

	// The removals first, so a later checkout can bring a file back to
	// a path an earlier change emptied. A trash failure keeps going:
	// the file stays in the tree and the next window commits it, which
	// is a truer outcome than a half-restored one.
	var syncPaths []string
	var checkout []string
	for _, c := range changes {
		switch c.Status {
		case 'D':
			if apply.Trash != nil {
				if err := apply.Trash(ctx, c.Path); err != nil {
					l.log.Warn("could not move a removed file to the trash; it stays in the tree", "path", c.Path, "err", err)
				}
			}
			syncPaths = append(syncPaths, c.Path)
		case 'R':
			if apply.Trash != nil {
				if err := apply.Trash(ctx, c.Orig); err != nil {
					l.log.Warn("could not move a renamed-away file to the trash; it stays in the tree", "path", c.Orig, "err", err)
				}
			}
			syncPaths = append(syncPaths, c.Orig)
			checkout = append(checkout, c.Path)
		default:
			checkout = append(checkout, c.Path)
		}
	}
	// Literal pathspecs: a file name that happens to hold a glob
	// character is a path, not a pattern.
	for i := 0; i < len(checkout); i += 100 {
		end := i + 100
		if end > len(checkout) {
			end = len(checkout)
		}
		args := []string{"checkout", ref, "--"}
		for _, p := range checkout[i:end] {
			args = append(args, ":(literal)"+p)
		}
		if _, err := l.git(ctx, args...); err != nil {
			return res, fmt.Errorf("check out the restored files: %w", err)
		}
	}
	syncPaths = append(syncPaths, checkout...)
	sort.Strings(syncPaths)

	if len(changes) > 0 {
		if err := l.ensureIgnore(); err != nil {
			l.log.Warn("could not re-write .gitignore after the restore", "err", err)
		}
		if _, err := l.git(ctx, "add", "-A"); err != nil {
			return res, fmt.Errorf("stage the restore: %w", err)
		}
		if staged, err := l.stagedCount(ctx); err == nil && staged > 0 {
			subject := fmt.Sprintf("restore the tree to %s", short(ref))
			if scope != "" {
				subject = fmt.Sprintf("restore %s to %s", scope, short(ref))
			}
			name, email := who.Name, who.Email
			if name == "" || email == "" || strings.ContainsAny(name, "<>\n\r") {
				name, email = l.opts.HumanName, l.opts.HumanEmail
			}
			if _, err := l.git(ctx,
				"-c", "user.name=YANA", "-c", "user.email=yana@local",
				"commit", "--author="+name+" <"+email+">",
				"-m", subject,
			); err != nil {
				return res, fmt.Errorf("commit the restore: %w", err)
			}
			l.log.Info("point-in-time restore", "scope", scope, "commit", ref, "author", name, "subject", subject)
		}
	}

	if out, err := l.git(ctx, "rev-parse", "--verify", "HEAD"); err == nil {
		res.Commit = strings.TrimSpace(out)
	}
	now := l.opts.Now()
	l.mu.Lock()
	l.commits = l.commitCount(ctx)
	l.lastCommit = now
	l.lastMade = now
	l.mu.Unlock()
	l.saveWindowState(now)

	if apply.Sync != nil {
		for _, p := range syncPaths {
			apply.Sync(ctx, p)
		}
	}
	return res, nil
}

// FindNote walks rel's history newest-first and returns the content of
// the newest revision whose frontmatter carries id — the deleted note a
// restore-from-history brings back. Revisions that held a different
// note at that path, and the deletion itself, are stepped over.
func (l *Layer) FindNote(ctx context.Context, rel, id string) ([]byte, string, error) {
	if !l.available {
		return nil, "", errors.New("git history is unavailable")
	}
	if _, err := l.git(ctx, "rev-parse", "--verify", "HEAD"); err != nil {
		return nil, "", ErrNoteNotInHistory
	}
	out, err := l.git(ctx, "log", "-n", "200", "--format=%H", "--", ":(literal)"+rel)
	if err != nil {
		return nil, "", err
	}
	for _, hash := range strings.Fields(out) {
		content, err := l.Show(ctx, hash, rel)
		if err != nil {
			continue // the commit that removed the path
		}
		if frontmatter.Parse(content).Meta.ID == id {
			return content, hash, nil
		}
	}
	return nil, "", ErrNoteNotInHistory
}

// pitPlan lists what takes the tree from base to ref inside scope.
func (l *Layer) pitPlan(ctx context.Context, base, ref, scope string) ([]PITChange, error) {
	args := []string{"diff", "--name-status", "-z", "-M", base, ref}
	if scope != "" {
		args = append(args, "--", ":(literal)"+scope)
	}
	out, err := l.git(ctx, args...)
	if err != nil {
		return nil, err
	}
	var changes []PITChange
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		st := fields[i]
		if st == "" {
			continue
		}
		c := PITChange{Status: st[0]}
		switch c.Status {
		case 'R', 'C':
			// A rename or copy lists the current path before the
			// target one, each its own NUL-separated field.
			if i+2 < len(fields) {
				c.Orig, c.Path = fields[i+1], fields[i+2]
				i += 2
			}
		default:
			if i+1 < len(fields) {
				c.Path = fields[i+1]
				i++
			}
		}
		if c.Path == "" {
			continue
		}
		changes = append(changes, c)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// tally fills a preview from a plan.
func tally(changes []PITChange) ([]PITChange, int, int, int, int) {
	a, c, d, m := tallyCounts(changes)
	return changes, a, c, d, m
}

func tallyCounts(changes []PITChange) (added, changed, deleted, moved int) {
	for _, ch := range changes {
		switch ch.Status {
		case 'A':
			added++
		case 'D':
			deleted++
		case 'R':
			moved++
		default: // M, and anything else a diff can say
			changed++
		}
	}
	return added, changed, deleted, moved
}

// verifyCommit reports whether ref names a commit in this repository.
func (l *Layer) verifyCommit(ctx context.Context, ref string) error {
	if !ValidRevision(ref) {
		return errors.New("the restore point must be a commit hash")
	}
	if _, err := l.git(ctx, "rev-parse", "--verify", ref+"^{commit}"); err != nil {
		return ErrNoSuchCommit
	}
	return nil
}

// headOrEmpty is HEAD's hash, or git's empty tree when there is no
// history yet.
func (l *Layer) headOrEmpty(ctx context.Context) string {
	out, err := l.git(ctx, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return emptyTree
	}
	return strings.TrimSpace(out)
}

// short is a commit hash the way a subject line shows it.
func short(ref string) string {
	if len(ref) > 7 {
		return ref[:7]
	}
	return ref
}
