// Package crdt is the Phase 0 spike harness: it exercises the pure-Go Yjs
// port (github.com/Deln0r/ygo) against the acceptance criteria in the build
// plan. Nothing in here is production code; the production reconciliation
// loop lives in internal/ once Phase 2 starts.
package crdt

import (
	"unicode/utf16"

	"github.com/Deln0r/ygo"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// ApplyTextDiff mutates text so that it reads as want, expressing the change
// as a minimal set of insert/delete operations rather than a full replace.
// Untouched characters keep their CRDT identity, which is what lets an
// external file edit merge with concurrent client typing instead of
// stomping it.
//
// Y.Text indexes in UTF-16 code units, so each diff chunk is measured with
// utf16 lengths rather than rune or byte counts.
func ApplyTextDiff(txn *ygo.TransactionMut, text *ygo.Text, want string) error {
	have := text.String()
	if have == want {
		return nil
	}
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffMain(have, want, false)
	diffs = dmp.DiffCleanupSemantic(diffs)

	var pos uint64
	for _, d := range diffs {
		n := uint64(len(utf16.Encode([]rune(d.Text))))
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			pos += n
		case diffmatchpatch.DiffDelete:
			if err := text.Delete(txn, pos, n); err != nil {
				return err
			}
		case diffmatchpatch.DiffInsert:
			if err := text.Insert(txn, pos, d.Text); err != nil {
				return err
			}
			pos += n
		}
	}
	return nil
}
