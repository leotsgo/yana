// Package ydoc is the server's adapter over the Yjs port chosen in Phase 0
// (github.com/reearth/ygo). One Doc holds one note body as a Y.Text named
// "body"; the frontmatter is not part of the document.
//
// The port's document lock is not re-entrant, so nothing here reads the
// text from inside a transaction. Every mutating method returns the V1
// update bytes it produced so the caller can log, persist, and forward
// them without a second encode.
package ydoc

import (
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/reearth/ygo/crdt"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// TextName is the name of the shared text every client opens.
const TextName = "body"

// Doc is one note body.
type Doc struct {
	doc  *crdt.Doc
	text *crdt.YText
	mu   sync.Mutex // serialises mutations so the update capture is exact
	out  [][]byte
	stop func()
}

// New creates an empty document.
func New() *Doc {
	d := crdt.New()
	y := &Doc{doc: d, text: d.GetText(TextName)}
	y.stop = d.OnUpdate(func(update []byte, origin any) {
		y.out = append(y.out, update)
	})
	return y
}

// Load creates a document from a V1 state (as produced by State).
func Load(state []byte) (*Doc, error) {
	y := New()
	if len(state) == 0 {
		return y, nil
	}
	if _, err := y.Apply(state, "load"); err != nil {
		y.Close()
		return nil, err
	}
	return y, nil
}

// Close drops the update subscription. The document is unusable afterwards.
func (y *Doc) Close() {
	if y.stop != nil {
		y.stop()
		y.stop = nil
	}
	y.doc.Destroy()
}

// Text returns the current body.
func (y *Doc) Text() string { return y.text.ToString() }

// State encodes the whole document as one V1 update.
func (y *Doc) State() []byte { return y.doc.EncodeStateAsUpdate() }

// StateVector encodes the document's clock for delta requests.
func (y *Doc) StateVector() []byte { return crdt.EncodeStateVectorV1(y.doc) }

// Diff encodes everything a peer with the given state vector is missing.
// A nil vector returns the full state.
func (y *Doc) Diff(sv []byte) ([]byte, error) {
	if len(sv) == 0 {
		return y.State(), nil
	}
	remote, err := crdt.DecodeStateVectorV1(sv)
	if err != nil {
		return nil, err
	}
	return crdt.EncodeStateAsUpdateV1(y.doc, remote), nil
}

// Apply integrates an update authored elsewhere. It returns the
// incremental update the port re-emits for it, which is empty when the
// document already contained everything in it.
func (y *Doc) Apply(update []byte, origin string) ([]byte, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.out = nil
	if err := crdt.ApplyUpdateV1(y.doc, update, origin); err != nil {
		return nil, err
	}
	return y.take(), nil
}

// Insert inserts s at the UTF-16 index pos.
func (y *Doc) Insert(pos int, s string, origin string) []byte {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.out = nil
	y.doc.Transact(func(txn *crdt.Transaction) {
		y.text.Insert(txn, pos, s, nil)
	}, origin)
	return y.take()
}

// Delete removes n UTF-16 units at pos.
func (y *Doc) Delete(pos, n int, origin string) []byte {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.out = nil
	y.doc.Transact(func(txn *crdt.Transaction) {
		y.text.Delete(txn, pos, n)
	}, origin)
	return y.take()
}

// SetText mutates the body so it reads as want, expressed as the minimal
// insert/delete operations between the current text and want. Characters
// the caller left alone keep their identity, which is what lets an
// external file edit merge with concurrent typing instead of replacing it.
// It returns the update, or nil when nothing changed.
func (y *Doc) SetText(want string, origin string) []byte {
	want = strings.ToValidUTF8(want, "�")
	y.mu.Lock()
	defer y.mu.Unlock()
	have := y.text.ToString()
	if have == want {
		return nil
	}
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffCleanupSemantic(dmp.DiffMain(have, want, false))
	y.out = nil
	y.doc.Transact(func(txn *crdt.Transaction) {
		pos := 0
		for _, d := range diffs {
			n := utf16Len(d.Text)
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				pos += n
			case diffmatchpatch.DiffDelete:
				y.text.Delete(txn, pos, n)
			case diffmatchpatch.DiffInsert:
				y.text.Insert(txn, pos, d.Text, nil)
				pos += n
			}
		}
	}, origin)
	return y.take()
}

// take merges the updates captured since out was cleared. A transaction
// emits one update; Apply of a bundle may emit several.
func (y *Doc) take() []byte {
	switch len(y.out) {
	case 0:
		return nil
	case 1:
		u := y.out[0]
		y.out = nil
		if isEmptyUpdate(u) {
			return nil
		}
		return u
	}
	merged, err := crdt.MergeUpdatesV1(y.out...)
	y.out = nil
	if err != nil || isEmptyUpdate(merged) {
		return nil
	}
	return merged
}

// isEmptyUpdate reports whether a V1 update carries no structs and no
// deletes (the port emits one for a transaction that changed nothing).
func isEmptyUpdate(u []byte) bool {
	return len(u) == 2 && u[0] == 0 && u[1] == 0
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// Merge combines updates into one without loading a document.
func Merge(updates ...[]byte) ([]byte, error) {
	return crdt.MergeUpdatesV1(updates...)
}
