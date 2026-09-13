package reconcile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/madeofpendletonwool/yana/internal/fsutil"
)

// The sidecar is the document's full state as one Yjs V1 update, written
// after every write-back. It survives deletion of index.db, which is what
// makes .sync/crdt/ the one part of .sync/ worth keeping.

func (r *Reconciler) crdtDir() string    { return filepath.Join(r.root.Dir(), ".sync", "crdt") }
func (r *Reconciler) retiredDir() string { return filepath.Join(r.crdtDir(), "retired") }
func (r *Reconciler) sidecarPath(id string) string {
	return filepath.Join(r.crdtDir(), id+".bin")
}
func (r *Reconciler) retiredPath(id string) string {
	return filepath.Join(r.retiredDir(), id+".bin")
}

// readSidecar returns the stored state, restoring a retired one when the
// note has come back, or nil when there is none.
func (r *Reconciler) readSidecar(id string) ([]byte, error) {
	r.restoreRetired(id)
	b, err := os.ReadFile(r.sidecarPath(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// restoreRetired moves a retired sidecar back into service. It reports
// whether one was restored.
func (r *Reconciler) restoreRetired(id string) bool {
	if _, err := os.Lstat(r.sidecarPath(id)); err == nil {
		return false
	}
	if err := os.Rename(r.retiredPath(id), r.sidecarPath(id)); err != nil {
		return false
	}
	return true
}

// writeSidecar stores the main document's state. n.mu is held.
func (r *Reconciler) writeSidecar(n *note) error {
	if err := fsutil.MkdirInherit(r.crdtDir()); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(r.sidecarPath(n.id), n.main.State(), 0o644)
}

// retireSidecar moves a deleted note's document to the retention area,
// with any edits that had not reached the file. n.mu is held.
func (r *Reconciler) retireSidecar(n *note) error {
	if err := fsutil.MkdirInherit(r.retiredDir()); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(r.retiredPath(n.id), n.main.State(), 0o644); err != nil {
		return err
	}
	err := os.Remove(r.sidecarPath(n.id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// sweepRetired deletes retired sidecars older than the retention window,
// and the log rows that go with them.
func (r *Reconciler) sweepRetired() {
	entries, err := os.ReadDir(r.retiredDir())
	if err != nil {
		return
	}
	cutoff := r.opts.Now().Add(-r.opts.Retention)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() || info.ModTime().After(cutoff) {
			continue
		}
		id := e.Name()[:len(e.Name())-len(filepath.Ext(e.Name()))]
		if err := os.Remove(filepath.Join(r.retiredDir(), e.Name())); err != nil {
			continue
		}
		r.dropLog(id)
		r.log.Info("retired sidecar expired", "id", id, "age", r.opts.Now().Sub(info.ModTime()).Round(time.Hour))
	}
}
