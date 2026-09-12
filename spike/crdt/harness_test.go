package crdt

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/Deln0r/ygo"
)

const textName = "body"

// replica is one of the three writers in the acceptance scenario: two
// headless clients and one "filesystem" writer that only ever knows the
// rendered text and replaces it wholesale.
type replica struct {
	name string
	doc  *ygo.Doc
	text *ygo.Text
	// inbox holds updates produced by peers that this replica has not
	// applied yet; the harness delivers them in random order.
	inbox [][]byte
}

func newReplica(name string) *replica {
	d := ygo.NewDoc()
	return &replica{name: name, doc: d, text: ygo.NewText(d, textName)}
}

func (r *replica) String() string {
	return r.text.String()
}

// edit runs fn in a write transaction and returns the update bytes the
// transaction produced (the delta between before and after state).
func (r *replica) edit(t testing.TB, fn func(txn *ygo.TransactionMut)) []byte {
	before := ygo.EncodeStateVector(r.doc)
	txn := r.doc.WriteTxn()
	fn(txn)
	txn.Commit()
	upd, err := ygo.EncodeDiff(r.doc, before)
	if err != nil {
		t.Fatalf("%s: encode diff: %v", r.name, err)
	}
	return upd
}

func (r *replica) apply(t testing.TB, upd []byte) {
	if err := ygo.ApplyUpdate(r.doc, upd); err != nil {
		t.Fatalf("%s: apply update: %v", r.name, err)
	}
}

var words = []string{"alpha", "beta", "gamma", "delta", "ε", "🙂", "\n", "# h1", "- item", "[[link]]", " "}

func randomClientEdit(rng *rand.Rand, r *replica, t testing.TB) []byte {
	return r.edit(t, func(txn *ygo.TransactionMut) {
		n := r.text.Length()
		if n > 0 && rng.Intn(3) == 0 {
			start := uint64(rng.Intn(int(n)))
			l := uint64(rng.Intn(int(n-start))) + 1
			start, l = alignUTF16(r.text.String(), start, l)
			if l > 0 {
				_ = r.text.Delete(txn, start, l)
			}
			return
		}
		pos := uint64(0)
		if n > 0 {
			pos = uint64(rng.Intn(int(n) + 1))
			pos, _ = alignUTF16(r.text.String(), pos, 0)
		}
		_ = r.text.Insert(txn, pos, words[rng.Intn(len(words))])
	})
}

// alignUTF16 nudges a (start, length) UTF-16 range so it never splits a
// surrogate pair. Real clients never generate split-surrogate edits; the
// random generator can.
func alignUTF16(s string, start, length uint64) (uint64, uint64) {
	u := utf16.Encode([]rune(s))
	isLow := func(i uint64) bool {
		return i < uint64(len(u)) && u[i] >= 0xDC00 && u[i] <= 0xDFFF
	}
	for isLow(start) && start > 0 {
		start--
		if length > 0 {
			length++
		}
	}
	for length > 0 && isLow(start+length) {
		length++
	}
	if start+length > uint64(len(u)) {
		length = uint64(len(u)) - start
	}
	return start, length
}

// randomFilesystemEdit simulates an external editor: take the text as the
// filesystem writer last saw it, mutate it as a plain string, then feed the
// new string back through the diff path with author "filesystem".
func randomFilesystemEdit(rng *rand.Rand, r *replica, t testing.TB) []byte {
	cur := r.text.String()
	var want string
	switch rng.Intn(3) {
	case 0: // append, like `echo >> note.md`
		want = cur + words[rng.Intn(len(words))]
	case 1: // full replacement with a partial overlap, like a save from an editor
		runes := []rune(cur)
		cut := 0
		if len(runes) > 0 {
			cut = rng.Intn(len(runes))
		}
		want = string(runes[:cut]) + words[rng.Intn(len(words))] + string(runes[cut:])
	default: // truncate + rewrite of the tail
		runes := []rune(cur)
		keep := 0
		if len(runes) > 0 {
			keep = rng.Intn(len(runes))
		}
		want = string(runes[:keep]) + words[rng.Intn(len(words))]
	}
	return r.edit(t, func(txn *ygo.TransactionMut) {
		if err := ApplyTextDiff(txn, r.text, want); err != nil {
			t.Fatalf("diff apply: %v", err)
		}
	})
}

// TestThreeWriterConvergence is the Phase 0 acceptance criterion: two
// clients plus one external text replacement converge to identical text
// across 1000 randomized interleavings.
func TestThreeWriterConvergence(t *testing.T) {
	const runs = 1000
	for seed := int64(0); seed < runs; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a, b, fs := newReplica("client-a"), newReplica("client-b"), newReplica("filesystem")
		all := []*replica{a, b, fs}

		steps := 5 + rng.Intn(40)
		for i := 0; i < steps; i++ {
			var upd []byte
			var src *replica
			switch rng.Intn(3) {
			case 0:
				src, upd = a, randomClientEdit(rng, a, t)
			case 1:
				src, upd = b, randomClientEdit(rng, b, t)
			default:
				src, upd = fs, randomFilesystemEdit(rng, fs, t)
			}
			for _, r := range all {
				if r != src {
					r.inbox = append(r.inbox, upd)
				}
			}
			// Deliver some pending updates, in random order, to random peers.
			for _, r := range all {
				for len(r.inbox) > 0 && rng.Intn(2) == 0 {
					j := rng.Intn(len(r.inbox))
					u := r.inbox[j]
					r.inbox = append(r.inbox[:j], r.inbox[j+1:]...)
					r.apply(t, u)
				}
			}
		}
		// Drain everything.
		for _, r := range all {
			rng.Shuffle(len(r.inbox), func(i, j int) { r.inbox[i], r.inbox[j] = r.inbox[j], r.inbox[i] })
			for _, u := range r.inbox {
				r.apply(t, u)
			}
			r.inbox = nil
		}
		// Belt and braces: a final state-vector exchange should be a no-op.
		for _, x := range all {
			for _, y := range all {
				if x == y {
					continue
				}
				d, err := ygo.EncodeDiff(x.doc, ygo.EncodeStateVector(y.doc))
				if err != nil {
					t.Fatal(err)
				}
				y.apply(t, d)
			}
		}
		if a.String() != b.String() || b.String() != fs.String() {
			t.Fatalf("seed %d diverged:\n a=%q\n b=%q\nfs=%q", seed, a.String(), b.String(), fs.String())
		}
		if ygo.HasPending(a.doc) || ygo.HasPending(b.doc) || ygo.HasPending(fs.doc) {
			t.Fatalf("seed %d: pending updates left after full delivery", seed)
		}
	}
}

// TestScopedUndo checks criterion 2: user A's undo never reverts user B's
// typing. Both users share one doc here (worst case: same replica), and the
// undo manager tracks only origin "user:a".
func TestScopedUndo(t *testing.T) {
	d := ygo.NewDoc()
	text := ygo.NewText(d, textName)
	um := ygo.NewUndoManagerWithOptions(d, ygo.UndoManagerOptions{
		CaptureTimeout: -1,
		TrackedOrigins: map[any]struct{}{"user:a": {}},
	}, text)
	defer um.Close()

	write := func(origin string, pos uint64, s string) {
		txn := d.WriteTxn()
		txn.Origin = origin
		if err := text.Insert(txn, pos, s); err != nil {
			t.Fatal(err)
		}
		txn.Commit()
	}
	write("user:a", 0, "hello ")
	write("user:b", 6, "world")
	write("user:a", 11, "!")
	if got := text.String(); got != "hello world!" {
		t.Fatalf("setup: %q", got)
	}
	if !um.Undo() {
		t.Fatal("expected an undo step for user:a")
	}
	if got := text.String(); got != "hello world" {
		t.Fatalf("after first undo: %q", got)
	}
	if !um.Undo() {
		t.Fatal("expected a second undo step for user:a")
	}
	if got := text.String(); got != "world" {
		t.Fatalf("after second undo: %q (user:b's text must survive)", got)
	}
	if um.Undo() {
		t.Fatal("user:b's edit must not be undoable by user:a's manager")
	}
}

// TestSnapshotCompaction checks criterion 4: a log of many small updates
// collapses into one state document that a fresh replica can load.
func TestSnapshotCompaction(t *testing.T) {
	src := newReplica("src")
	var log [][]byte
	for i := 0; i < 500; i++ {
		log = append(log, src.edit(t, func(txn *ygo.TransactionMut) {
			_ = src.text.Insert(txn, src.text.Length(), fmt.Sprintf("line %d\n", i))
		}))
	}
	var logBytes int
	for _, u := range log {
		logBytes += len(u)
	}
	merged, err := ygo.MergeUpdates(log)
	if err != nil {
		t.Fatal(err)
	}
	state := ygo.EncodeStateAsUpdate(src.doc)

	fresh := newReplica("fresh")
	fresh.apply(t, merged)
	if fresh.String() != src.String() {
		t.Fatal("merged log does not reproduce the document")
	}
	fresh2 := newReplica("fresh2")
	fresh2.apply(t, state)
	if fresh2.String() != src.String() {
		t.Fatal("state snapshot does not reproduce the document")
	}
	t.Logf("500 updates: log=%d bytes, merged=%d bytes, state=%d bytes, text=%d bytes",
		logBytes, len(merged), len(state), len(src.String()))
}

// TestLargeDocumentSize checks criterion 5: update size and merge time on a
// ~100KB document.
func TestLargeDocumentSize(t *testing.T) {
	body := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 2300) // ~103KB
	src := newReplica("src")
	full := src.edit(t, func(txn *ygo.TransactionMut) {
		_ = src.text.Insert(txn, 0, body)
	})
	// One keystroke in the middle.
	key := src.edit(t, func(txn *ygo.TransactionMut) {
		_ = src.text.Insert(txn, 50000, "x")
	})
	peer := newReplica("peer")
	start := time.Now()
	peer.apply(t, full)
	peer.apply(t, key)
	elapsed := time.Since(start)
	if peer.String() != src.String() {
		t.Fatal("diverged")
	}
	// External full-file replacement of the big doc through the diff path.
	want := strings.Replace(src.String(), "lazy", "sleepy", 100)
	start = time.Now()
	upd := src.edit(t, func(txn *ygo.TransactionMut) {
		if err := ApplyTextDiff(txn, src.text, want); err != nil {
			t.Fatal(err)
		}
	})
	diffElapsed := time.Since(start)
	peer.apply(t, upd)
	if peer.String() != want {
		t.Fatal("diff-apply diverged")
	}
	t.Logf("100KB doc: full update=%d bytes (text %d), keystroke update=%d bytes, load+apply=%s, diff-apply of 100 replacements=%s (update %d bytes)",
		len(full), len(body), len(key), elapsed, diffElapsed, len(upd))
	if len(key) > 64 {
		t.Errorf("single keystroke update unexpectedly large: %d bytes", len(key))
	}
}
