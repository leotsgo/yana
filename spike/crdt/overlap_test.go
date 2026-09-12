package crdt

import (
	"testing"

	"github.com/Deln0r/ygo"
)

// TestPartialOverlapApply: a receiver that already holds the first part of
// a squashed block must integrate the unseen tail rather than drop the
// whole block. This is the apply-side counterpart of the whole-cell
// EncodeDiff behaviour measured in TestIncrementalDiffSize.
func TestPartialOverlapApply(t *testing.T) {
	src := newReplica("src")
	u1 := src.edit(t, func(txn *ygo.TransactionMut) { _ = src.text.Insert(txn, 0, "one ") })
	u2 := src.edit(t, func(txn *ygo.TransactionMut) { _ = src.text.Insert(txn, 4, "two ") })
	u3 := src.edit(t, func(txn *ygo.TransactionMut) { _ = src.text.Insert(txn, 8, "three") })
	_ = u2

	peer := newReplica("peer")
	peer.apply(t, u1)
	peer.apply(t, u3) // u3 covers clocks 0..12 if whole-cell; peer knows 0..3
	if got, want := peer.String(), src.String(); got != want {
		t.Fatalf("partial overlap dropped: got %q want %q (pending=%v)", got, want, ygo.HasPending(peer.doc))
	}
	// Skipping u2 entirely: peer has 0..3, u3 carries 4..12 (or 0..12).
	peer2 := newReplica("peer2")
	peer2.apply(t, u1)
	peer2.apply(t, u3)
	peer2.apply(t, u2)
	if got, want := peer2.String(), src.String(); got != want {
		t.Fatalf("out-of-order overlap: got %q want %q", got, want)
	}
}
