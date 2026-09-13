// Rename propagation. Moving or renaming a note rewrites the raw targets
// of every inbound wikilink as document edits with author "filesystem", so
// the change reaches open clients as CRDT updates and reaches the files
// through the ordinary write-back. The rewrite itself never writes a file.
//
// The move is all-or-nothing: every check that can fail — path safety,
// target collisions, write permission on the spaces involved, the inbound
// link set, the rewrite targets — runs before the file is renamed. Only
// then are the rename and the rewrites applied. A failure while applying
// (disk or database level, none of which the buffer phase can see) is
// compensated by rolling the rename back and restoring the already
// rewritten texts; that path is logged loudly because it is the one place
// the loop chooses damage control over refusal.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/wikilink"
)

// ErrMoveSamePath is returned when the note already sits at the target.
var ErrMoveSamePath = errors.New("reconcile: the note is already at that path")

// ErrMoveTargetTaken is returned when another file occupies the target.
var ErrMoveTargetTaken = errors.New("reconcile: a file already exists at that path")

// SpaceDeniedError reports a move refused because one of the spaces it
// touches may not be written. Phase 4 fills the check with real roles;
// until then callers may pass their own.
type SpaceDeniedError struct {
	Space string
	Err   error
}

func (e *SpaceDeniedError) Error() string {
	return fmt.Sprintf("reconcile: no write access to space %q: %v", e.Space, e.Err)
}

func (e *SpaceDeniedError) Unwrap() error { return e.Err }

// MoveResult reports what a move did.
type MoveResult struct {
	// NewPath is the note's path after the move.
	NewPath string
	// Rewritten is the number of inbound link targets rewritten.
	Rewritten int
	// Broken is the number of inbound links a cross-space move leaves
	// behind; they resolve to nothing in their space until the target
	// returns.
	Broken int
}

// CanWrite decides whether the actor of a move may change a space. nil
// means everything is allowed, which is the state until Phase 4.
type CanWrite func(space string) error

// Move renames the note id to newRel and rewrites its inbound wikilinks.
// The rewrites are document edits with author "filesystem": live for open
// clients, durable through write-back.
func (r *Reconciler) Move(ctx context.Context, id, newRel string, may CanWrite) (MoveResult, error) {
	row, err := r.db.GetNote(ctx, id)
	if err != nil {
		return MoveResult{}, err
	}
	clean, err := r.root.Clean(newRel)
	if err != nil {
		return MoveResult{}, fmt.Errorf("reconcile: move %s: %w", newRel, err)
	}
	if scanner.KindOf(clean) == "" {
		return MoveResult{}, fmt.Errorf("reconcile: move target %q is not a note file", clean)
	}
	if scanner.IsAsset(clean) {
		return MoveResult{}, fmt.Errorf("reconcile: move target %q sits under _assets", clean)
	}
	if clean == row.RelPath {
		return MoveResult{}, ErrMoveSamePath
	}
	absNew, _, err := r.root.Resolve(clean)
	if err != nil {
		return MoveResult{}, fmt.Errorf("reconcile: move %s: %w", newRel, err)
	}
	if _, err := os.Lstat(absNew); err == nil {
		return MoveResult{}, ErrMoveTargetTaken
	} else if !errors.Is(err, fs.ErrNotExist) {
		return MoveResult{}, err
	}
	if err := r.root.CheckCollision(clean); err != nil {
		return MoveResult{}, fmt.Errorf("reconcile: move %s: %w", clean, err)
	}

	oldSpace, newSpace := row.Space, spaceOfRel(clean)
	allow := func(sp string) error {
		if may == nil {
			return nil
		}
		if err := may(sp); err != nil {
			return &SpaceDeniedError{Space: sp, Err: err}
		}
		return nil
	}
	// The origin space is written twice, by the rename and by the link
	// rewrites; the destination by the rename. All are checked first.
	for _, sp := range []string{oldSpace, newSpace} {
		if err := allow(sp); err != nil {
			return MoveResult{}, err
		}
	}

	rewrites, broken, err := r.planRewrites(ctx, row, clean, newSpace != oldSpace)
	if err != nil {
		return MoveResult{}, err
	}

	res := MoveResult{NewPath: clean, Broken: broken}
	if err := r.applyMove(ctx, row, clean, absNew); err != nil {
		return res, err
	}
	applied := make([]linkRewrite, 0, len(rewrites))
	for _, rw := range rewrites {
		if err := r.transformText(ctx, rw.fromID, func(cur string) (string, bool) {
			return replaceRawTarget(cur, rw.oldRaw, rw.newRaw)
		}); err != nil {
			r.log.Error("rewrite failed after the rename; rolling the move back", "id", id, "note", rw.fromID, "err", err)
			r.rollbackMove(ctx, row, clean, applied)
			return res, fmt.Errorf("reconcile: rewrite of %s failed after the rename (move rolled back): %w", rw.fromID, err)
		}
		applied = append(applied, rw)
		res.Rewritten++
	}
	r.log.Info("note moved", "id", id, "from", row.RelPath, "to", clean,
		"rewrites", res.Rewritten, "broken", res.Broken)
	return res, nil
}

// linkRewrite is one buffered inbound-link rewrite.
type linkRewrite struct {
	fromID string
	oldRaw string
	newRaw string
}

// planRewrites computes every inbound raw target's replacement against the
// note's old position, without changing anything. Cross-space moves leave
// links behind (counted as broken): a raw target cannot reach into another
// space, so rewriting it would only disguise the break.
func (r *Reconciler) planRewrites(ctx context.Context, row index.Note, clean string, crossSpace bool) ([]linkRewrite, int, error) {
	inbound, err := r.db.InboundLinks(ctx, row.ID)
	if err != nil {
		return nil, 0, err
	}
	if len(inbound) == 0 {
		return nil, 0, nil
	}
	notes, err := r.db.ListNotes(ctx, row.Space)
	if err != nil {
		return nil, 0, err
	}
	refs := make([]wikilink.NoteRef, len(notes))
	for i, n := range notes {
		refs[i] = wikilink.NoteRef{ID: n.ID, RelPath: n.RelPath}
	}
	res := wikilink.NewResolver(row.Space, refs)

	var rewrites []linkRewrite
	broken := 0
	for _, in := range inbound {
		if in.FromSpace != row.Space {
			continue
		}
		got := res.Resolve(in.RawTarget, in.FromRel)
		if !got.OK || got.ToID != row.ID {
			// The link row is stale (the body changed under us); the
			// reindex that follows the body already recomputed it.
			continue
		}
		if crossSpace {
			broken++
			continue
		}
		rewrites = append(rewrites, linkRewrite{
			fromID: in.FromID,
			oldRaw: in.RawTarget,
			newRaw: wikilink.RewriteTarget(got.Rule, in.FromRel, clean, wikilink.HasExtension(in.RawTarget)),
		})
	}
	return rewrites, broken, nil
}

// transformText applies fn to the note's current text under its lock and
// records the result as a filesystem edit. fn returns the new text and
// whether anything changed; computing and applying under one hold of the
// note lock means concurrent client edits are part of the text fn sees,
// never something the rewrite reverts.
func (r *Reconciler) transformText(ctx context.Context, id string, fn func(cur string) (string, bool)) error {
	n, err := r.acquire(ctx, id)
	if err != nil {
		return err
	}
	defer n.mu.Unlock()
	next, changed := fn(n.main.Text())
	if !changed {
		return nil
	}
	u := n.main.SetText(next, AuthorFilesystem)
	if u == nil {
		return nil
	}
	if err := r.record(ctx, n, u, AuthorFilesystem); err != nil {
		return err
	}
	r.markDirty(n)
	r.emit(Event{NoteID: id, Kind: EventUpdate, Path: n.rel, Update: u, Author: AuthorFilesystem})
	return nil
}

// replaceRawTarget rewrites every wikilink whose target is exactly oldRaw
// to newRaw, keeping display text. Occurrences inside fenced code blocks
// are left alone; they are samples, not links.
func replaceRawTarget(text, oldRaw, newRaw string) (string, bool) {
	needle := "[[" + oldRaw
	lines := strings.Split(text, "\n")
	fence := false
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		if replaced, hit := replaceInLine(line, needle, newRaw); hit {
			lines[i] = replaced
			changed = true
		}
	}
	if !changed {
		return text, false
	}
	return strings.Join(lines, "\n"), true
}

// replaceInLine rewrites the target inside one line. A match counts only
// when it ends the link: the character after the target is ], |, or the
// end of the line.
func replaceInLine(line, needle, newRaw string) (string, bool) {
	hit := false
	for from := 0; ; {
		i := strings.Index(line[from:], needle)
		if i < 0 {
			break
		}
		i += from
		rest := i + len(needle)
		if rest >= len(line) || line[rest] == ']' || line[rest] == '|' {
			line = line[:i] + "[[" + newRaw + line[rest:]
			from = i + len(newRaw) + 2
			hit = true
			continue
		}
		from = i + 1
	}
	return line, hit
}

// applyMove renames the file and moves the loaded document with it.
func (r *Reconciler) applyMove(ctx context.Context, row index.Note, clean, absNew string) error {
	absOld, _, err := r.root.Resolve(row.RelPath)
	if err != nil {
		return err
	}
	if err := fsutil.MkdirInherit(filepath.Dir(absNew)); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}
	if err := os.Rename(absOld, absNew); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	r.mu.Lock()
	if n := r.notes[row.ID]; n != nil {
		n.mu.Lock()
		if n.rel == row.RelPath && !n.gone && !n.unloaded {
			delete(r.byPath, n.rel)
			n.rel = clean
			r.byPath[clean] = row.ID
			n.mu.Unlock()
			r.mu.Unlock()
			r.emit(Event{NoteID: row.ID, Kind: EventMoved, Path: clean})
		} else {
			n.mu.Unlock()
			r.mu.Unlock()
			// The watcher moved it elsewhere mid-flight; put the file
			// back where the index expects it.
			_ = os.Rename(absNew, absOld)
			return fmt.Errorf("reconcile: note %s changed under the move", row.ID)
		}
	} else {
		r.mu.Unlock()
	}
	// The index row and the space's links follow the id to the new path.
	r.reindex(ctx, clean)
	return nil
}

// rollbackMove undoes an applied move whose rewrites failed. The already
// applied rewrites are reversed first, then the rename itself.
func (r *Reconciler) rollbackMove(ctx context.Context, row index.Note, clean string, applied []linkRewrite) {
	absOld, _, err := r.root.Resolve(row.RelPath)
	if err != nil {
		r.log.Error("rollback: cannot resolve the old path", "path", row.RelPath, "err", err)
		return
	}
	absNew, _, err := r.root.Resolve(clean)
	if err != nil {
		r.log.Error("rollback: cannot resolve the new path", "path", clean, "err", err)
		return
	}
	for i := len(applied) - 1; i >= 0; i-- {
		rw := applied[i]
		if err := r.transformText(ctx, rw.fromID, func(cur string) (string, bool) {
			return replaceRawTarget(cur, rw.newRaw, rw.oldRaw)
		}); err != nil {
			r.log.Error("rollback: could not restore a link target", "note", rw.fromID, "raw", rw.newRaw, "err", err)
		}
	}
	if err := os.Rename(absNew, absOld); err != nil {
		r.log.Error("rollback: could not rename the file back", "to", row.RelPath, "err", err)
		return
	}
	r.mu.Lock()
	if n := r.notes[row.ID]; n != nil {
		n.mu.Lock()
		if n.rel == clean {
			delete(r.byPath, n.rel)
			n.rel = row.RelPath
			r.byPath[row.RelPath] = row.ID
		}
		n.mu.Unlock()
	}
	r.mu.Unlock()
	r.reindex(ctx, row.RelPath)
	r.log.Warn("move rolled back after a failed rewrite", "id", row.ID, "path", row.RelPath)
}

func spaceOfRel(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}
