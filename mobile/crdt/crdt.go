// Package crdt is the yana-owned gomobile bind package over the Go Yjs
// port chosen in Phase 0 (github.com/reearth/ygo). It gives the Android
// client everything it needs from a note's document: local edits, remote
// updates, delta encoding, full-text diff application, snapshots, and an
// undo manager whose stack is scoped to this device — the pieces the
// port's own mobile façade has except the last.
//
// One Doc is one note body: a shared text named "body", the same name the
// server and the web client use. The frontmatter is not part of the
// document.
//
// # Offsets are UTF-16 code units
//
// Len, Insert and Delete count UTF-16 code units, not runes or bytes:
// a character outside the BMP (an emoji, for instance) counts as two.
// That is the same unit the wire format, the web client, and Kotlin's
// String.length and TextField indices use, so an index that is valid for
// the text field is valid here. A position must never fall inside a
// surrogate pair; text-field selections never do, and ReplaceText never
// splits one because it derives positions from a character diff.
//
// # Updates
//
// Every mutating method returns the V1 update bytes it produced, so the
// caller can persist or forward them without a second encode; it returns
// nil when the call changed nothing. ObserveUpdates delivers the same
// incremental updates to a callback, one per committed transaction, never
// a re-emission of a whole block. Updates produced by local edits, undo,
// and redo come with local=true; updates integrated from a remote peer
// come with local=false, so an echo guard knows what to broadcast.
//
// # Undo scope
//
// Each Doc has an origin token of its own. Local edits carry it; remote
// updates carry a different one. NewUndoManager tracks only the local
// token, so Undo reverts this device's edits and never another writer's.
//
// # Threading
//
// Methods are safe from any thread but are synchronous and blocking.
// Call ApplyUpdate and the encode methods off the main thread; a large
// update on it can jank the app. ObserveUpdates callbacks run on a
// background goroutine, in commit order, holding no locks — post to the
// app's own dispatcher from there and return promptly.
//
// # Lifecycle
//
// Call Close when a note closes (ViewModel.onCleared). Close releases the
// document and stops every observer and undo manager attached to it.
// Afterwards error-returning methods report ErrClosed, value-returning
// methods return zero values, and nothing panics.
package crdt

import (
	"errors"
	"sync"

	ycrdt "github.com/reearth/ygo/crdt"
)

// ErrClosed is returned by methods called after Close.
var ErrClosed = errors.New("yana/crdt: used after Close")

// TextName is the name of the shared text every client opens.
const TextName = "body"

// maxSafeInteger is the largest integer the web client's runtime
// represents exactly; a client id above it cannot round-trip there.
const maxSafeInteger = int64(1)<<53 - 1

// localToken is the origin of one device's edits. The underscore field
// makes the type non-zero-size, so every allocation is a distinct
// pointer: origin tokens compare with ==, and two zero-size allocations
// may share an address and merge into one origin.
type localToken struct{ _ byte }

// remoteOrigin tags updates integrated from a peer. Distinct from every
// Doc's local token by construction.
var remoteOrigin = &localToken{}

// Doc is one note body.
type Doc struct {
	mu    sync.Mutex
	d     *ycrdt.Doc // nil after Close
	text  *ycrdt.YText
	local *localToken
	ums   map[*UndoManager]struct{}

	// out collects the updates fired during the call in progress; the
	// call holds mu, and the port fires its update callbacks on the
	// calling goroutine inside the transaction, so no second lock.
	out [][]byte

	stopCap func() // unsubscribes the capture feed

	subsMu sync.Mutex
	subs   map[*Subscription]struct{}
}

// NewDoc creates an empty document with a random client id.
func NewDoc() *Doc {
	return newDoc(ycrdt.New())
}

// NewDocWithClientID creates an empty document with the given client id.
// The id must be in [0, 2^53 - 1] so the web client can represent it
// exactly.
func NewDocWithClientID(clientID int64) (*Doc, error) {
	if clientID < 0 || clientID > maxSafeInteger {
		return nil, errors.New("yana/crdt: client id must be in [0, 2^53 - 1]")
	}
	return newDoc(ycrdt.New(ycrdt.WithClientID(ycrdt.ClientID(uint64(clientID))))), nil
}

// LoadDoc creates a document and applies state to it (as produced by
// State, or by a peer's full-state encode). Empty state gives an empty
// document. The state counts as remote: it produces nothing to broadcast.
func LoadDoc(state []byte) (*Doc, error) {
	y := NewDoc()
	if len(state) == 0 {
		return y, nil
	}
	if _, err := y.ApplyUpdate(state); err != nil {
		y.Close()
		return nil, err
	}
	return y, nil
}

func newDoc(d *ycrdt.Doc) *Doc {
	y := &Doc{
		d:     d,
		text:  d.GetText(TextName),
		local: &localToken{},
	}
	y.stopCap = d.OnUpdate(func(update []byte, _ any) {
		y.out = append(y.out, update)
	})
	return y
}

// Close releases the document, stops its undo managers and observers,
// and makes every later call a safe no-op. It is idempotent.
func (y *Doc) Close() {
	y.mu.Lock()
	d := y.d
	y.d = nil
	if y.stopCap != nil {
		y.stopCap()
		y.stopCap = nil
	}
	ums := make([]*UndoManager, 0, len(y.ums))
	for um := range y.ums {
		ums = append(ums, um)
	}
	y.ums = nil
	y.mu.Unlock()

	for _, um := range ums {
		um.close()
	}
	if d != nil {
		d.Destroy()
	}

	y.subsMu.Lock()
	subs := make([]*Subscription, 0, len(y.subs))
	for s := range y.subs {
		subs = append(subs, s)
	}
	y.subsMu.Unlock()
	for _, s := range subs {
		s.Close()
	}
}

// ClientID returns the document's client id, or 0 after Close.
func (y *Doc) ClientID() int64 {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return 0
	}
	return int64(y.d.ClientID())
}

// Text returns the body as a string. Every client converges on the same
// one.
func (y *Doc) Text() string {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return ""
	}
	return y.text.ToString()
}

// Len returns the body's length in UTF-16 code units — the unit
// TextField indices use. A character outside the BMP counts as two.
func (y *Doc) Len() int64 {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return 0
	}
	return int64(y.text.Len())
}

// State encodes the whole document as one V1 update: the snapshot used
// for compaction and cold starts. A fresh LoadDoc of it reads the same.
func (y *Doc) State() []byte {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return nil
	}
	return y.d.EncodeStateAsUpdate()
}

// StateVector encodes the document's clock; two peers exchange these to
// learn what the other is missing.
func (y *Doc) StateVector() []byte {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return nil
	}
	return ycrdt.EncodeStateVectorV1(y.d)
}

// Diff encodes everything a peer holding remoteStateVector is missing.
// An empty vector returns the full state.
func (y *Doc) Diff(remoteStateVector []byte) ([]byte, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return nil, ErrClosed
	}
	if len(remoteStateVector) == 0 {
		return y.d.EncodeStateAsUpdate(), nil
	}
	sv, err := ycrdt.DecodeStateVectorV1(remoteStateVector)
	if err != nil {
		return nil, err
	}
	return ycrdt.EncodeStateAsUpdateV1(y.d, sv), nil
}

// ApplyUpdate integrates an update from a peer, in any order relative to
// the ones before it. It returns the incremental update the port emits
// for what was new in it — nil when the document already held everything
// — which the caller may forward, though normally it stays local: the
// server relays each update once.
func (y *Doc) ApplyUpdate(update []byte) ([]byte, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.d == nil {
		return nil, ErrClosed
	}
	if len(update) == 0 {
		return nil, nil
	}
	y.out = nil
	if err := ycrdt.ApplyUpdateV1(y.d, update, remoteOrigin); err != nil {
		y.out = nil
		return nil, err
	}
	return y.take(), nil
}

// MergeUpdates combines two V1 updates into one a fresh replica can load
// directly — the compaction form that needs no document. Fold it over an
// update log to compact it.
func MergeUpdates(a, b []byte) ([]byte, error) {
	return ycrdt.MergeUpdatesV1(a, b)
}
