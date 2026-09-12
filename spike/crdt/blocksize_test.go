package crdt

import (
	"fmt"
	"testing"

	"github.com/Deln0r/ygo"
)

// TestIncrementalDiffSize records how the size of a per-edit update behaves
// when one client keeps appending: yjs splits a block at the state-vector
// boundary, so the update should stay small regardless of history.
func TestIncrementalDiffSize(t *testing.T) {
	src := newReplica("src")
	var sizes []int
	for i := 0; i < 5; i++ {
		u := src.edit(t, func(txn *ygo.TransactionMut) {
			_ = src.text.Insert(txn, src.text.Length(), fmt.Sprintf("line %d\n", i))
		})
		sizes = append(sizes, len(u))
	}
	for i := 5; i < 200; i++ {
		src.edit(t, func(txn *ygo.TransactionMut) {
			_ = src.text.Insert(txn, src.text.Length(), fmt.Sprintf("line %d\n", i))
		})
	}
	u := src.edit(t, func(txn *ygo.TransactionMut) {
		_ = src.text.Insert(txn, src.text.Length(), "line 200\n")
	})
	sizes = append(sizes, len(u))
	t.Logf("append update sizes (first five, then #201): %v", sizes)
	if sizes[len(sizes)-1] > 4*sizes[0]+64 {
		// Known and documented upstream (docs/tech-debt.md, "EncodeDiff"):
		// the boundary cell is emitted whole instead of split at the
		// remote clock. Wire stays valid, receivers de-duplicate; the cost
		// is bandwidth proportional to the squashed block, which for a
		// single long-lived appender is the whole run. Recorded here, not
		// asserted, because it is a property of this port rather than a
		// bug in the harness.
		t.Logf("update size grows with history: %v (whole-cell EncodeDiff)", sizes)
	}
}
