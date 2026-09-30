package crdt

import (
	"sync"

	ycrdt "github.com/reearth/ygo/crdt"
)

// UndoManager reverts and re-applies this device's edits on a Doc. It
// tracks only the document's local origin, so an undo steps back
// through the local user's writing and never through another writer's,
// however their edits interleave. Updates integrated from a peer are not
// on the stack; undoing while someone else types reverts the local edit
// and merges with theirs.
//
// One edit is one step: each Insert, Delete, or ReplaceText call undoes
// as a unit. (The port's capture-timeout grouping mis-handles a group
// that starts at the client's first clock — the merge reads a missing
// clock as absent rather than zero, so the first edit of the group
// survives the undo — so this package does not expose it. An editor that
// wants coarser steps calls Undo the extra times; the Kotlin side knows
// where its grouping boundaries are.)
//
// Destroy stops tracking (as Close on the Doc does). Afterwards Undo and
// Redo report nil and nothing panics.
type UndoManager struct {
	d      *Doc
	um     *ycrdt.UndoManager
	mu     sync.Mutex
	closed bool
}

// NewUndoManager tracks local edits on d. Creating it on a closed Doc
// gives an inert manager whose methods report nil and ErrClosed.
func NewUndoManager(d *Doc) *UndoManager {
	u := &UndoManager{d: d}
	d.mu.Lock()
	if d.d != nil {
		u.um = ycrdt.NewUndoManager(d.d, []ycrdt.SharedType{d.text},
			ycrdt.WithTrackedOrigins(d.local),
			ycrdt.WithCaptureTimeout(-1))
		if d.ums == nil {
			d.ums = make(map[*UndoManager]struct{})
		}
		d.ums[u] = struct{}{}
	} else {
		u.closed = true
	}
	d.mu.Unlock()
	return u
}

// Undo reverts the most recent local edit and returns the update it
// produced, for forwarding. It returns nil when there is nothing left to
// undo or the manager is destroyed; ErrClosed after Close on the Doc.
func (u *UndoManager) Undo() ([]byte, error) {
	return u.apply(func(um *ycrdt.UndoManager) { um.Undo() })
}

// Redo re-applies the most recently undone local edit and returns the
// update it produced, for forwarding. It returns nil when there is
// nothing to redo or the manager is destroyed; ErrClosed after Close on
// the Doc.
func (u *UndoManager) Redo() ([]byte, error) {
	return u.apply(func(um *ycrdt.UndoManager) { um.Redo() })
}

// UndoStackSize returns how many undo steps remain.
func (u *UndoManager) UndoStackSize() int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.um == nil {
		return 0
	}
	return int64(u.um.UndoStackSize())
}

// RedoStackSize returns how many redo steps remain.
func (u *UndoManager) RedoStackSize() int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.um == nil {
		return 0
	}
	return int64(u.um.RedoStackSize())
}

// Clear drops every step from both stacks without applying anything.
func (u *UndoManager) Clear() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.um == nil {
		return
	}
	u.um.Clear()
}

// Destroy stops tracking. Undo and Redo report false afterwards. Close
// on the owning Doc destroys every manager attached to it.
func (u *UndoManager) Destroy() { u.close() }

func (u *UndoManager) close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return
	}
	u.closed = true
	if u.um != nil {
		u.um.Destroy()
		u.um = nil
	}
}

func (u *UndoManager) apply(fn func(*ycrdt.UndoManager)) ([]byte, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.um == nil {
		return nil, ErrClosed
	}
	d := u.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.d == nil {
		return nil, ErrClosed
	}
	d.out = nil
	fn(u.um)
	return d.take(), nil
}
