// Soft delete and recovery. Deleting a note from the app moves its file
// to .trash/<space>/... — a real copy at a real path, recoverable by hand
// with mv — and retires the CRDT sidecar to the retention area, so both
// the last file content and the whole edit history survive. A file that
// vanishes externally (rm, a sync tool) has no trash copy; its recovery
// is the retired sidecar, which is why every deletion path records where
// the note used to live in deleted_notes. Everything is swept after the
// retention window; until then the only permanent destruction is an
// explicit destroy from the trash (or emptying it).
package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/fsutil"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/scanner"
	"github.com/madeofpendletonwool/yana/internal/ydoc"
)

// ErrNothingToRestore is returned when neither a trash copy nor a sidecar
// of the deleted note exists anymore.
var ErrNothingToRestore = errors.New("reconcile: nothing left to restore")

// ErrTrashGone is returned when the deleted note id is unknown.
var ErrTrashGone = errors.New("reconcile: no deleted note with that id")

// DeleteResult reports what a soft delete did.
type DeleteResult struct {
	// TrashPath is where the file landed under .trash ("" when the file
	// was already gone and only the sidecar was retained).
	TrashPath string `json:"trash_path"`
}

// TrashEntry is one deleted note in the listing. HasFile and HasSidecar
// say what recovery would restore from; a note with neither is not
// listed.
type TrashEntry struct {
	index.DeletedNote
	HasFile    bool `json:"has_file"`
	HasSidecar bool `json:"has_sidecar"`
	// Untracked marks a .trash copy with no index row (the index was
	// rebuilt); its original path is derived from the trash path.
	Untracked bool `json:"untracked,omitempty"`
}

// RestoreResult reports what a restore did.
type RestoreResult struct {
	// Path is where the note landed: the original path, or a
	// conflict-suffixed one when something new had taken it.
	Path string `json:"path"`
	// Conflict reports whether the original path was occupied.
	Conflict bool `json:"conflict"`
	// Note is the re-indexed note; Deferred reports the index pass was
	// rescheduled (the note appears a settle window later).
	Note     index.Note `json:"note"`
	Deferred bool       `json:"deferred"`
}

// Delete soft-deletes a note: the file moves under .trash preserving its
// structure, the document is retired to the retention area with any
// edits that had not reached the file, and the index row becomes a trash
// record. Inbound links stay where they are; they resolve again if the
// note returns.
func (r *Reconciler) Delete(ctx context.Context, id string, may CanWrite) (DeleteResult, error) {
	row, err := r.db.GetNote(ctx, id)
	if err != nil {
		return DeleteResult{}, err
	}
	if may != nil {
		if err := may(row.Space); err != nil {
			return DeleteResult{}, &SpaceDeniedError{Space: row.Space, Err: err}
		}
	}
	now := r.opts.Now()

	trashRel := ""
	absOld, _, rerr := r.root.Resolve(row.RelPath)
	if rerr == nil {
		if _, statErr := os.Lstat(absOld); statErr == nil {
			trashRel, err = r.moveToTrash(row.RelPath, absOld, now)
			if err != nil {
				return DeleteResult{}, err
			}
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return DeleteResult{}, statErr
		}
	}

	// The record is written while the row is still known; a database
	// failure compensates by moving the file back, the same all-or-
	// nothing shape as Move.
	if err := r.db.Write(ctx, func(tx *sql.Tx) error {
		if err := index.RetireNote(tx, row, trashRel, now); err != nil {
			return err
		}
		// A removed note can free a basename (uniqueness flips) and
		// breaks inbound links; recompute the space it lived in.
		return index.RecomputeSpaceLinks(tx, row.Space)
	}); err != nil {
		if trashRel != "" {
			_ = os.Rename(filepath.Join(r.root.Dir(), filepath.FromSlash(trashRel)), absOld)
		}
		return DeleteResult{}, fmt.Errorf("reconcile: delete %s: %w", id, err)
	}

	// Close and retain the loaded document, or retire an on-disk sidecar
	// the loop is not holding. Either way open clients hear "deleted".
	r.mu.Lock()
	n := r.notes[id]
	r.mu.Unlock()
	if n != nil {
		r.retire(ctx, n, row.RelPath, "deleted from the app")
	} else {
		r.retireSidecarOnDisk(id, now)
	}
	r.log.Info("note deleted", "id", id, "path", row.RelPath, "trash_path", trashRel)
	return DeleteResult{TrashPath: trashRel}, nil
}

// moveToTrash renames a note file into .trash under its own path. A name
// already held there (delete, recreate, delete again) gets a timestamped
// suffix; the deletion time is the file's mtime, which the sweep reads.
func (r *Reconciler) moveToTrash(rel, abs string, now time.Time) (string, error) {
	trashRel := path.Join(".trash", rel)
	target := filepath.Join(r.root.Dir(), filepath.FromSlash(trashRel))
	if _, err := os.Lstat(target); err == nil {
		ext := path.Ext(rel)
		trashRel = path.Join(".trash", strings.TrimSuffix(rel, ext)+".deleted-"+now.UTC().Format("20060102T150405Z")+ext)
		target = filepath.Join(r.root.Dir(), filepath.FromSlash(trashRel))
		if _, err := os.Lstat(target); err == nil {
			return "", fmt.Errorf("reconcile: trash already holds %s", trashRel)
		}
	}
	if err := fsutil.MkdirInherit(filepath.Dir(target)); err != nil {
		return "", err
	}
	if err := os.Rename(abs, target); err != nil {
		return "", err
	}
	_ = os.Chtimes(target, now, now)
	return trashRel, nil
}

// Restore returns a deleted note to its original path and re-resolves
// the space's links. The copy under .trash is moved back when it exists;
// otherwise the file is rebuilt from the retained sidecar (the external-
// rm case), which carries the edits that had not reached the file.
func (r *Reconciler) Restore(ctx context.Context, id string, may CanWrite) (RestoreResult, error) {
	row, untracked, err := r.trashRow(ctx, id)
	if err != nil {
		return RestoreResult{}, err
	}
	if may != nil {
		if err := may(row.Space); err != nil {
			return RestoreResult{}, &SpaceDeniedError{Space: row.Space, Err: err}
		}
	}
	now := r.opts.Now()

	target := row.RelPath
	absTarget, _, err := r.root.Resolve(target)
	if err != nil {
		return RestoreResult{}, err
	}
	res := RestoreResult{}
	if _, err := os.Lstat(absTarget); err == nil {
		// Something new lives at the original path; restore beside it
		// rather than clobber it, the same convention as colliding
		// HTML saves.
		res.Conflict = true
		ext := path.Ext(target)
		target = strings.TrimSuffix(target, ext) + ".conflict-" + now.UTC().Format("20060102T150405") + ext
		absTarget, _, err = r.root.Resolve(target)
		if err != nil {
			return RestoreResult{}, err
		}
		if _, err := os.Lstat(absTarget); err == nil {
			return RestoreResult{}, ErrMoveTargetTaken
		}
	}

	var trashAbs string
	if row.TrashPath != "" {
		trashAbs = filepath.Join(r.root.Dir(), filepath.FromSlash(row.TrashPath))
	}
	_, statErr := os.Lstat(trashAbs)
	switch {
	case statErr == nil:
		if err := fsutil.MkdirInherit(filepath.Dir(absTarget)); err != nil {
			return RestoreResult{}, err
		}
		if err := os.Rename(trashAbs, absTarget); err != nil {
			return RestoreResult{}, err
		}
	case errors.Is(statErr, fs.ErrNotExist) && r.hasSidecar(row.ID):
		if err := r.rebuildFromSidecar(row, absTarget); err != nil {
			return RestoreResult{}, err
		}
	default:
		return RestoreResult{}, ErrNothingToRestore
	}

	// The sidecar returns with the note so the edit history comes back
	// with it; the reindex restores the row and re-resolves links.
	r.restoreRetired(row.ID)
	r.reindex(ctx, target)
	res.Path = target
	if n, err := r.db.GetNote(ctx, row.ID); err == nil {
		res.Note = n
	} else if !errors.Is(err, index.ErrNotFound) {
		return RestoreResult{}, err
	} else {
		res.Deferred = true
	}
	r.log.Info("note restored", "id", row.ID, "path", target, "from_trash", statErr == nil, "conflict", res.Conflict, "untracked", untracked)
	return res, nil
}

// rebuildFromSidecar writes the file back from the retained document.
func (r *Reconciler) rebuildFromSidecar(row index.DeletedNote, abs string) error {
	state, err := r.readSidecarBytes(row.ID)
	if err != nil {
		return err
	}
	d, err := ydoc.Load(state)
	if err != nil {
		return err
	}
	defer d.Close()
	content, _, _ := frontmatter.EnsureID([]byte(d.Text()), row.ID, row.Created)
	if err := fsutil.MkdirInherit(filepath.Dir(abs)); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(abs, content, 0o644)
}

// Destroy permanently removes one deleted note: the trash copy, the
// retained sidecar, and the edit log. This and EmptyTrash are the only
// permanent destruction in the app.
func (r *Reconciler) Destroy(ctx context.Context, id string, may CanWrite) error {
	row, _, err := r.trashRow(ctx, id)
	if err != nil {
		return err
	}
	if may != nil {
		if err := may(row.Space); err != nil {
			return &SpaceDeniedError{Space: row.Space, Err: err}
		}
	}
	if err := r.destroyDeleted(ctx, row); err != nil {
		return err
	}
	r.log.Info("trash entry destroyed", "id", id, "path", row.RelPath)
	return nil
}

// EmptyTrash destroys every deleted note the caller may write. It
// returns how many entries were destroyed.
func (r *Reconciler) EmptyTrash(ctx context.Context, allow func(space string) bool) (int, error) {
	entries, err := r.ListTrash(ctx, allow)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if err := r.destroyDeleted(ctx, e.DeletedNote); err != nil {
			return n, err
		}
		n++
	}
	if n > 0 {
		r.log.Info("trash emptied", "destroyed", n)
	}
	return n, nil
}

// ListTrash lists deleted notes with something left to recover, allowed
// permitting spaces (nil lists everything; "" is the root, which only
// the owner sees). Entries with neither a trash copy nor a sidecar are
// skipped, and .trash files the index knows nothing about (it was
// rebuilt) are listed from the filesystem.
func (r *Reconciler) ListTrash(ctx context.Context, allow func(space string) bool) ([]TrashEntry, error) {
	rows, err := r.db.ListDeleted(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []TrashEntry
	for _, d := range rows {
		if allow != nil && !allow(d.Space) {
			continue
		}
		e := TrashEntry{DeletedNote: d}
		if d.TrashPath != "" {
			if _, err := os.Lstat(filepath.Join(r.root.Dir(), filepath.FromSlash(d.TrashPath))); err == nil {
				e.HasFile = true
				seen[d.TrashPath] = true
			}
		}
		e.HasSidecar = r.hasSidecar(d.ID)
		if !e.HasFile && !e.HasSidecar {
			continue
		}
		out = append(out, e)
	}
	untracked, err := r.untrackedTrash(allow)
	if err != nil {
		return nil, err
	}
	for _, d := range untracked {
		if seen[d.TrashPath] {
			continue
		}
		out = append(out, TrashEntry{DeletedNote: d, HasFile: true, Untracked: true})
	}
	return out, nil
}

// trashRow resolves a deleted note id to its record, falling back to an
// untracked .trash copy when the index was rebuilt.
func (r *Reconciler) trashRow(ctx context.Context, id string) (index.DeletedNote, bool, error) {
	row, err := r.db.GetDeleted(ctx, id)
	if err == nil {
		return row, false, nil
	}
	if !errors.Is(err, index.ErrDeletedNotFound) {
		return row, false, err
	}
	untracked, err := r.untrackedTrash(nil)
	if err != nil {
		return row, false, err
	}
	for _, d := range untracked {
		if d.ID == id {
			return d, true, nil
		}
	}
	return row, false, ErrTrashGone
}

// untrackedTrash walks .trash for files no row points at.
func (r *Reconciler) untrackedTrash(allow func(space string) bool) ([]index.DeletedNote, error) {
	rows, err := r.db.ListDeleted(context.Background())
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(rows))
	for _, d := range rows {
		known[d.TrashPath] = true
	}
	var out []index.DeletedNote
	err = r.walkTrash(func(trashRel string, info fs.FileInfo) {
		if known[trashRel] {
			return
		}
		space := trashSpace(trashRel)
		if allow != nil && !allow(space) {
			return
		}
		d := index.DeletedNote{
			Space:     space,
			RelPath:   originalOf(trashRel),
			Kind:      scanner.KindOf(strings.TrimPrefix(trashRel, ".trash/")),
			DeletedAt: info.ModTime().UTC(),
			TrashPath: trashRel,
		}
		d.Title = strings.TrimSuffix(path.Base(d.RelPath), path.Ext(d.RelPath))
		if raw, err := os.ReadFile(filepath.Join(r.root.Dir(), filepath.FromSlash(trashRel))); err == nil {
			if id := frontmatter.Parse(raw).Meta.ID; validTrashID(id) {
				d.ID = id
			}
		}
		// A file with no id can still be listed (and swept or destroyed
		// by path) but not restored by id.
		out = append(out, d)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// walkTrash calls fn for every regular file under .trash with its
// root-relative slash path and info.
func (r *Reconciler) walkTrash(fn func(trashRel string, info fs.FileInfo)) error {
	root := filepath.Join(r.root.Dir(), ".trash")
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p == root {
				return nil
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(r.root.Dir(), p)
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fn(filepath.ToSlash(rel), info)
		return nil
	})
}

// trashSpace is the first path segment under .trash, or "" for files
// loose in it.
func trashSpace(trashRel string) string {
	rest := strings.TrimPrefix(trashRel, ".trash/")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return ""
}

var trashSuffixRe = regexp.MustCompile(`\.deleted-\d{8}T\d{6}Z(\.[^./]+)$`)

// originalOf turns a trash path back into the note's original path,
// stripping the collision suffix a second deletion of the same name
// carries.
func originalOf(trashRel string) string {
	rel := strings.TrimPrefix(trashRel, ".trash/")
	if m := trashSuffixRe.FindStringSubmatchIndex(rel); m != nil {
		rel = rel[:m[0]] + rel[m[2]:m[3]]
	}
	return rel
}

func validTrashID(id string) bool {
	if len(id) != 26 {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}
	return true
}

// hasSidecar reports whether a retained document exists for the id,
// retired or still live (a note deleted while its document was unloaded
// keeps a live sidecar until it is opened or swept).
func (r *Reconciler) hasSidecar(id string) bool {
	if id == "" {
		return false
	}
	if _, err := os.Lstat(r.retiredPath(id)); err == nil {
		return true
	}
	_, err := os.Lstat(r.sidecarPath(id))
	return err == nil
}

// readSidecarBytes returns the retained state from wherever it lives.
func (r *Reconciler) readSidecarBytes(id string) ([]byte, error) {
	if b, err := os.ReadFile(r.retiredPath(id)); err == nil {
		return b, nil
	}
	b, err := os.ReadFile(r.sidecarPath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNothingToRestore
	}
	return b, err
}

// retireSidecarOnDisk moves a live sidecar of an unloaded note into the
// retention area, starting its window at the deletion.
func (r *Reconciler) retireSidecarOnDisk(id string, now time.Time) {
	if err := os.Rename(r.sidecarPath(id), r.retiredPath(id)); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			r.log.Warn("could not retire sidecar", "id", id, "err", err)
		}
		return
	}
	_ = os.Chtimes(r.retiredPath(id), now, now)
}

// destroyDeleted removes every artifact of a deleted note: the trash
// copy, the sidecar wherever it lives, the edit log, and the row.
func (r *Reconciler) destroyDeleted(ctx context.Context, d index.DeletedNote) error {
	if d.TrashPath != "" {
		if err := os.Remove(filepath.Join(r.root.Dir(), filepath.FromSlash(d.TrashPath))); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	for _, p := range []string{r.retiredPath(d.ID), r.sidecarPath(d.ID)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	r.dropLog(d.ID)
	return r.db.Write(ctx, func(tx *sql.Tx) error { return index.DeleteDeleted(tx, d.ID) })
}

// sweepTrash destroys entries past the retention window and removes
// .trash copies the index knows nothing about once they age out.
func (r *Reconciler) sweepTrash() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := r.opts.Now()
	rows, err := r.db.ListDeleted(ctx)
	if err != nil {
		return
	}
	for _, d := range rows {
		if now.Sub(d.DeletedAt) <= r.opts.Retention {
			continue
		}
		if err := r.destroyDeleted(ctx, d); err != nil {
			r.log.Warn("trash sweep: could not destroy entry", "id", d.ID, "err", err)
			continue
		}
		r.log.Info("trash entry expired", "id", d.ID, "path", d.RelPath,
			"age", now.Sub(d.DeletedAt).Round(time.Hour))
	}
	// A rebuilt index forgets the rows; the files still age out.
	cutoff := now.Add(-r.opts.Retention)
	_ = r.walkTrash(func(trashRel string, info fs.FileInfo) {
		if info.ModTime().After(cutoff) {
			return
		}
		if err := os.Remove(filepath.Join(r.root.Dir(), filepath.FromSlash(trashRel))); err != nil {
			return
		}
		r.log.Info("untracked trash file expired", "path", trashRel,
			"age", now.Sub(info.ModTime()).Round(time.Hour))
	})
}
