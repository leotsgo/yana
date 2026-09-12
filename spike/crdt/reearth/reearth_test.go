// Package reearth runs the same three-writer scenario against the second
// pure-Go Yjs port (github.com/reearth/ygo) so the decision document can
// compare both on identical inputs.
package reearth

import (
	"fmt"
	"math/rand"
	"testing"
	"unicode/utf16"

	"github.com/reearth/ygo/crdt"
	"github.com/sergi/go-diff/diffmatchpatch"
)

type replica struct {
	doc   *crdt.Doc
	text  *crdt.YText
	inbox [][]byte
	out   [][]byte
}

func newReplica() *replica {
	d := crdt.New()
	r := &replica{doc: d, text: d.GetText("body")}
	d.OnUpdate(func(update []byte, origin any) {
		r.out = append(r.out, update)
	})
	return r
}

func (r *replica) edit(fn func(txn *crdt.Transaction)) []byte {
	r.out = nil
	r.doc.Transact(fn, "local")
	if len(r.out) != 1 {
		panic(fmt.Sprintf("expected one update event, got %d", len(r.out)))
	}
	return r.out[0]
}

func (r *replica) apply(t testing.TB, u []byte) {
	if err := crdt.ApplyUpdateV1(r.doc, u, "remote"); err != nil {
		t.Fatal(err)
	}
}

func u16len(s string) int { return len(utf16.Encode([]rune(s))) }

func applyTextDiff(txn *crdt.Transaction, text *crdt.YText, have, want string) {
	dmp := diffmatchpatch.New()
	diffs := dmp.DiffCleanupSemantic(dmp.DiffMain(have, want, false))
	pos := 0
	for _, d := range diffs {
		n := u16len(d.Text)
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			pos += n
		case diffmatchpatch.DiffDelete:
			text.Delete(txn, pos, n)
		case diffmatchpatch.DiffInsert:
			text.Insert(txn, pos, d.Text, nil)
			pos += n
		}
	}
}

var words = []string{"alpha", "beta", "gamma", "ε", "🙂", "\n", "# h1", " "}

func alignUTF16(s string, start, length int) (int, int) {
	u := utf16.Encode([]rune(s))
	isLow := func(i int) bool { return i < len(u) && u[i] >= 0xDC00 && u[i] <= 0xDFFF }
	for isLow(start) && start > 0 {
		start--
		if length > 0 {
			length++
		}
	}
	for length > 0 && isLow(start+length) {
		length++
	}
	if start+length > len(u) {
		length = len(u) - start
	}
	return start, length
}

func TestThreeWriterConvergence(t *testing.T) {
	for seed := int64(0); seed < 1000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a, b, fs := newReplica(), newReplica(), newReplica()
		all := []*replica{a, b, fs}
		steps := 5 + rng.Intn(40)
		for i := 0; i < steps; i++ {
			var src *replica
			var upd []byte
			switch rng.Intn(3) {
			case 0, 1:
				src = all[rng.Intn(2)]
				// Reads happen outside Transact: the doc mutex is not
				// re-entrant in this port.
				n := src.text.Len()
				cur := src.text.ToString()
				if n > 0 && rng.Intn(3) == 0 {
					start := rng.Intn(n)
					l := rng.Intn(n-start) + 1
					start, l = alignUTF16(cur, start, l)
					upd = src.edit(func(txn *crdt.Transaction) {
						if l > 0 {
							src.text.Delete(txn, start, l)
						}
					})
				} else {
					pos := 0
					if n > 0 {
						pos, _ = alignUTF16(cur, rng.Intn(n+1), 0)
					}
					w := words[rng.Intn(len(words))]
					upd = src.edit(func(txn *crdt.Transaction) { src.text.Insert(txn, pos, w, nil) })
				}
			default:
				src = fs
				cur := fs.text.ToString()
				runes := []rune(cur)
				cut := 0
				if len(runes) > 0 {
					cut = rng.Intn(len(runes))
				}
				want := string(runes[:cut]) + words[rng.Intn(len(words))] + string(runes[cut:])
				upd = fs.edit(func(txn *crdt.Transaction) { applyTextDiff(txn, fs.text, cur, want) })
			}
			for _, r := range all {
				if r != src {
					r.inbox = append(r.inbox, upd)
				}
			}
			for _, r := range all {
				for len(r.inbox) > 0 && rng.Intn(2) == 0 {
					j := rng.Intn(len(r.inbox))
					u := r.inbox[j]
					r.inbox = append(r.inbox[:j], r.inbox[j+1:]...)
					r.apply(t, u)
				}
			}
		}
		for _, r := range all {
			rng.Shuffle(len(r.inbox), func(i, j int) { r.inbox[i], r.inbox[j] = r.inbox[j], r.inbox[i] })
			for _, u := range r.inbox {
				r.apply(t, u)
			}
			r.inbox = nil
		}
		if a.text.ToString() != b.text.ToString() || b.text.ToString() != fs.text.ToString() {
			t.Fatalf("seed %d diverged", seed)
		}
	}
}

func TestIncrementalUpdateSize(t *testing.T) {
	src := newReplica()
	var sizes []int
	for i := 0; i < 201; i++ {
		n := src.text.Len()
		u := src.edit(func(txn *crdt.Transaction) {
			src.text.Insert(txn, n, fmt.Sprintf("line %d\n", i), nil)
		})
		if i < 5 || i == 200 {
			sizes = append(sizes, len(u))
		}
	}
	t.Logf("append update sizes (first five, then #201): %v", sizes)
}

func TestScopedUndo(t *testing.T) {
	d := crdt.New()
	text := d.GetText("body")
	um := crdt.NewUndoManager(d, []crdt.SharedType{text}, crdt.WithTrackedOrigins("user:a"), crdt.WithCaptureTimeout(-1))
	defer um.Destroy()
	d.Transact(func(txn *crdt.Transaction) { text.Insert(txn, 0, "hello ", nil) }, "user:a")
	d.Transact(func(txn *crdt.Transaction) { text.Insert(txn, 6, "world", nil) }, "user:b")
	d.Transact(func(txn *crdt.Transaction) { text.Insert(txn, 11, "!", nil) }, "user:a")
	um.Undo()
	um.Undo()
	if got := text.ToString(); got != "world" {
		t.Fatalf("scoped undo: %q", got)
	}
	if um.Undo() {
		t.Fatal("user:b's edit must not be undoable")
	}
}
