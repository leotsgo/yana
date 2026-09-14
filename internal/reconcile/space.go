// Space membership sync: .space.yml files are read on every watcher
// event (and by the scanner's full walk), cached into spaces and
// space_members, and every change fires the hook that re-authorizes
// open realtime subscriptions.
package reconcile

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/spaces"
)

// syncSpaceFile handles one changed .space.yml path from the watcher.
// A missing file empties the space's membership rather than deleting
// the space: the directory may still hold notes only the owner can
// reach now.
func (r *Reconciler) syncSpaceFile(ctx context.Context, rel string) {
	space := strings.SplitN(rel, "/", 2)[0]
	abs := filepath.Join(r.root.Dir(), filepath.FromSlash(rel))
	spec, err := readSpaceFile(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			r.syncSpaceMembers(ctx, space, spaces.Spec{})
			return
		}
		r.log.Warn("cannot read space membership; keeping the previous members", "path", rel, "err", err)
		return
	}
	r.syncSpaceMembers(ctx, space, spec)
}

// readSpaceFile parses one membership file.
func readSpaceFile(abs string) (spaces.Spec, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return spaces.Spec{}, err
	}
	spec, err := spaces.Parse(data)
	if err != nil {
		return spaces.Spec{}, err
	}
	return spec, nil
}

// syncSpaceMembers resolves a spec's user references against the
// accounts table, replaces the cached rows, and fires the change hook.
func (r *Reconciler) syncSpaceMembers(ctx context.Context, space string, spec spaces.Spec) {
	n, err := r.db.SyncSpaceSpec(ctx, space, spec, r.log)
	if err != nil {
		r.log.Error("cannot cache space membership", "space", space, "err", err)
		return
	}
	r.log.Info("space membership loaded", "space", space, "members", n)
	r.fireSpaceChange(space)
}

// fireSpaceChange invokes the membership-change hook if one is set.
func (r *Reconciler) fireSpaceChange(space string) {
	r.mu.Lock()
	fn := r.opts.OnSpaceMembersChanged
	r.mu.Unlock()
	if fn != nil {
		fn(space)
	}
}

// SetOnSpaceMembersChanged sets the membership-change hook after
// construction (the realtime relay is built after the reconciler and is
// its usual consumer). Safe to call while running.
func (r *Reconciler) SetOnSpaceMembersChanged(fn func(space string)) {
	r.mu.Lock()
	r.opts.OnSpaceMembersChanged = fn
	r.mu.Unlock()
}

// retireSpace drops a gone space's cached rows and fires the change
// hook so its rooms are re-checked (everyone but the global owner
// loses access).
func (r *Reconciler) retireSpace(ctx context.Context, space string) {
	if err := r.db.Write(ctx, func(tx *sql.Tx) error { return index.RetireSpace(tx, space) }); err != nil {
		r.log.Warn("cannot retire space rows", "space", space, "err", err)
		return
	}
	r.log.Info("space directory gone; membership cleared", "space", space)
	r.fireSpaceChange(space)
}
