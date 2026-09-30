package crdt

import (
	"math/rand"
	"testing"
	"unicode/utf16"
)

func TestCreateAndEdit(t *testing.T) {
	d := NewDoc()
	defer d.Close()
	if got := d.Text(); got != "" {
		t.Fatalf("fresh doc text = %q", got)
	}
	if u, err := d.Insert(0, "hello "); err != nil || u == nil {
		t.Fatalf("insert: u=%v err=%v", u, err)
	}
	if _, err := d.Insert(6, "world"); err != nil {
		t.Fatal(err)
	}
	if got := d.Text(); got != "hello world" {
		t.Fatalf("text = %q", got)
	}
	if u, err := d.Delete(0, 6); err != nil || u == nil {
		t.Fatalf("delete: u=%v err=%v", u, err)
	}
	if got := d.Text(); got != "world" {
		t.Fatalf("text = %q", got)
	}
	// Empty edits change nothing and report it.
	if u, err := d.Insert(0, ""); err != nil || u != nil {
		t.Fatalf("empty insert: u=%v err=%v", u, err)
	}
	if u, err := d.Delete(0, 0); err != nil || u != nil {
		t.Fatalf("empty delete: u=%v err=%v", u, err)
	}
}

func TestUTF16Offsets(t *testing.T) {
	d := NewDoc()
	defer d.Close()
	if _, err := d.Insert(0, "a🙂b"); err != nil {
		t.Fatal(err)
	}
	// "a" (1 unit) + U+1F642 (2 units) + "b" (1 unit).
	if got := d.Len(); got != 4 {
		t.Fatalf("len = %d, want 4 UTF-16 units", got)
	}
	// Insert right after the emoji: offset 3, past both surrogates.
	if _, err := d.Insert(3, "X"); err != nil {
		t.Fatal(err)
	}
	if got := d.Text(); got != "a🙂Xb" {
		t.Fatalf("text = %q", got)
	}
	// Deleting the emoji takes both units.
	if _, err := d.Delete(1, 2); err != nil {
		t.Fatal(err)
	}
	if got := d.Text(); got != "aXb" {
		t.Fatalf("text = %q", got)
	}
	if got := d.Len(); got != 3 {
		t.Fatalf("len = %d", got)
	}
}

func TestBoundsAndUTF8(t *testing.T) {
	d := NewDoc()
	defer d.Close()
	if _, err := d.Insert(1, "x"); err == nil {
		t.Fatal("insert past end must error")
	}
	if _, err := d.Delete(0, 1); err == nil {
		t.Fatal("delete past end must error")
	}
	if _, err := d.Insert(-1, "x"); err == nil {
		t.Fatal("negative index must error")
	}
	if _, err := d.Insert(0, "a\xffb"); err == nil {
		t.Fatal("invalid UTF-8 must error, not panic")
	}
	if got := d.Text(); got != "" {
		t.Fatalf("failed calls changed the text: %q", got)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	d := newDocID(t, 7)
	var log [][]byte
	for i := 0; i < 500; i++ {
		u, err := d.Insert(d.Len(), line(i))
		if err != nil {
			t.Fatal(err)
		}
		log = append(log, u)
	}

	// The state of a loaded doc is the compaction form.
	state := d.State()
	fresh, err := LoadDoc(state)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if fresh.Text() != d.Text() {
		t.Fatal("snapshot round trip lost text")
	}
	if fresh.Len() != d.Len() {
		t.Fatalf("snapshot len = %d, want %d", fresh.Len(), d.Len())
	}

	// Merging the log without a document reaches the same state.
	merged := log[0]
	for _, u := range log[1:] {
		m, err := MergeUpdates(merged, u)
		if err != nil {
			t.Fatal(err)
		}
		merged = m
	}
	byLog, err := LoadDoc(merged)
	if err != nil {
		t.Fatal(err)
	}
	defer byLog.Close()
	if byLog.Text() != d.Text() {
		t.Fatal("merged log reached different text than the document")
	}

	// An empty state loads as an empty document.
	empty, err := LoadDoc(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	if empty.Text() != "" || empty.Len() != 0 {
		t.Fatal("empty state did not load empty")
	}
}

func TestStateVectorAndDiff(t *testing.T) {
	base := newDocID(t, 1)
	defer base.Close()
	if _, err := base.Insert(0, "shared\n"); err != nil {
		t.Fatal(err)
	}

	peer, err := LoadDoc(base.State())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peerSV := peer.StateVector()

	// The document moves on; the peer asks for what it missed.
	for _, s := range []string{"one\n", "two\n", "three\n"} {
		if _, err := base.Insert(base.Len(), s); err != nil {
			t.Fatal(err)
		}
	}
	delta, err := base.Diff(peerSV)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.ApplyUpdate(delta); err != nil {
		t.Fatal(err)
	}
	if peer.Text() != base.Text() {
		t.Fatalf("peer %q != base %q", peer.Text(), base.Text())
	}

	// An empty vector means the full state.
	full, err := base.Diff(nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadDoc(full)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Text() != base.Text() {
		t.Fatal("diff with empty vector did not carry the full state")
	}
}

func TestUpdatesStayIncremental(t *testing.T) {
	d := NewDoc()
	defer d.Close()
	sizes := map[int]int{}
	for i := 0; i < 200; i++ {
		u, err := d.Insert(d.Len(), line(i))
		if err != nil {
			t.Fatal(err)
		}
		if i < 5 || i > 195 {
			sizes[i] = len(u)
		}
	}
	for i, n := range sizes {
		if n == 0 || n > 200 {
			t.Fatalf("update %d carries %d bytes; appends must stay small incremental updates", i, n)
		}
	}
	// Applying an update the doc already holds reports no change.
	sv := d.StateVector()
	full, err := d.Diff(sv)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := d.ApplyUpdate(full); err != nil || u != nil {
		t.Fatalf("re-applying own state must be a no-op: u=%v err=%v", u, err)
	}
}

func TestReplaceTextMergesConcurrentTyping(t *testing.T) {
	a := newDocID(t, 1)
	defer a.Close()
	if _, err := a.Insert(0, "The quick brown fox\n"); err != nil {
		t.Fatal(err)
	}
	b, err := LoadDoc(a.State())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// B types at the end while A rewrites the middle.
	if _, err := b.Insert(b.Len(), "jumps\n"); err != nil {
		t.Fatal(err)
	}
	if u := a.ReplaceText("The slow brown fox\n"); u == nil {
		t.Fatal("replace returned no update")
	}

	// Exchange both ways; both converge with A's rewrite and B's typing.
	sva, svb := a.StateVector(), b.StateVector()
	ua, err := a.Diff(svb)
	if err != nil {
		t.Fatal(err)
	}
	ub, err := b.Diff(sva)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ApplyUpdate(ub); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ApplyUpdate(ua); err != nil {
		t.Fatal(err)
	}
	if a.Text() != b.Text() {
		t.Fatalf("diverged: a=%q b=%q", a.Text(), b.Text())
	}
	want := "The slow brown fox\njumps\n"
	if a.Text() != want {
		t.Fatalf("merged text = %q, want %q", a.Text(), want)
	}
	if u := a.ReplaceText(a.Text()); u != nil {
		t.Fatalf("replace with identical text returned %d bytes", len(u))
	}
}

func TestCloseSemantics(t *testing.T) {
	d := NewDoc()
	if _, err := d.Insert(0, "text"); err != nil {
		t.Fatal(err)
	}
	um := NewUndoManager(d)
	sub := d.ObserveUpdates(nopObserver{})
	d.Close()
	d.Close() // idempotent

	if got := d.Text(); got != "" {
		t.Fatalf("text after close = %q", got)
	}
	if got := d.Len(); got != 0 {
		t.Fatalf("len after close = %d", got)
	}
	if got := d.State(); got != nil {
		t.Fatal("state after close must be nil")
	}
	if got := d.StateVector(); got != nil {
		t.Fatal("state vector after close must be nil")
	}
	if _, err := d.Diff(nil); err != ErrClosed {
		t.Fatalf("diff after close: %v", err)
	}
	if _, err := d.ApplyUpdate([]byte{0, 0}); err != ErrClosed {
		t.Fatalf("apply after close: %v", err)
	}
	if _, err := d.Insert(0, "x"); err != ErrClosed {
		t.Fatalf("insert after close: %v", err)
	}
	if _, err := d.Delete(0, 1); err != ErrClosed {
		t.Fatalf("delete after close: %v", err)
	}
	if u := d.ReplaceText("x"); u != nil {
		t.Fatal("replace after close must be nil")
	}
	if _, err := um.Undo(); err != ErrClosed {
		t.Fatalf("undo after close: %v", err)
	}
	if _, err := um.Redo(); err != ErrClosed {
		t.Fatalf("redo after close: %v", err)
	}
	if got := um.UndoStackSize(); got != 0 {
		t.Fatalf("undo stack after close = %d", got)
	}
	um.Clear() // no-op, no panic
	um.Destroy()
	sub.Close()
	// Observing a closed doc gives an inert subscription.
	d.ObserveUpdates(nopObserver{}).Close()
	NewUndoManager(d).Undo() // inert
	if _, err := NewDocWithClientID(-1); err == nil {
		t.Fatal("negative client id must error")
	}
	if _, err := NewDocWithClientID(1 << 53); err == nil {
		t.Fatal("client id above 2^53-1 must error")
	}
}

// TestConvergence drives three replicas through the whole bind surface —
// typed inserts and deletes, wholesale rewrites, out-of-order delivery —
// and requires the same text everywhere at the end, the way the spike
// harness did for the raw port.
func TestConvergence(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		rng := rand.New(rand.NewSource(seed))
		replicas := []*Doc{newDocID(t, 1), newDocID(t, 2), newDocID(t, 3)}
		for _, r := range replicas {
			defer r.Close()
		}
		inboxes := make([][][]byte, len(replicas))
		steps := 5 + rng.Intn(40)
		for i := 0; i < steps; i++ {
			src := rng.Intn(len(replicas))
			var upd []byte
			var err error
			switch rng.Intn(10) {
			case 0, 1:
				upd, err = replicas[src].ReplaceText(mutateText(replicas[src].Text(), rng)), nil
			case 2, 3:
				n := replicas[src].Len()
				if n > 0 && rng.Intn(3) == 0 {
					start := int64(rng.Intn(int(n)))
					length := int64(rng.Intn(int(n-start))) + 1
					start, length = alignUTF16(replicas[src].Text(), start, length)
					upd, err = replicas[src].Delete(start, length)
				} else {
					pos := int64(0)
					if n > 0 {
						pos, _ = alignUTF16(replicas[src].Text(), int64(rng.Intn(int(n+1))), 0)
					}
					upd, err = replicas[src].Insert(pos, words[rng.Intn(len(words))])
				}
			default:
				upd, err = replicas[src].Insert(replicas[src].Len(), words[rng.Intn(len(words))])
			}
			if err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			if upd == nil {
				continue
			}
			for j := range replicas {
				if j != src {
					inboxes[j] = append(inboxes[j], upd)
				}
			}
			for j := range replicas {
				for len(inboxes[j]) > 0 && rng.Intn(2) == 0 {
					k := rng.Intn(len(inboxes[j]))
					if _, err := replicas[j].ApplyUpdate(inboxes[j][k]); err != nil {
						t.Fatalf("seed %d: %v", seed, err)
					}
					inboxes[j] = append(inboxes[j][:k], inboxes[j][k+1:]...)
				}
			}
		}
		for j := range replicas {
			for _, u := range inboxes[j] {
				if _, err := replicas[j].ApplyUpdate(u); err != nil {
					t.Fatalf("seed %d: %v", seed, err)
				}
			}
		}
		for j := 1; j < len(replicas); j++ {
			if replicas[0].Text() != replicas[j].Text() {
				t.Fatalf("seed %d: replica %d diverged", seed, j)
			}
		}
	}
}

var words = []string{"alpha", "beta", "gamma", "ε", "🙂", "\n", "# h1", " ", "words ", "claus\r\n"}

func line(i int) string {
	return "line " + itoa(i) + "\n"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

func newDocID(t *testing.T, id int64) *Doc {
	t.Helper()
	d, err := NewDocWithClientID(id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mutateText(s string, rng *rand.Rand) string {
	if s == "" {
		return words[rng.Intn(len(words))]
	}
	cut := rng.Intn(len(s) + 1)
	return s[:cut] + words[rng.Intn(len(words))] + s[cut:]
}

// alignUTF16 snaps a [start, start+length) UTF-16 range out of surrogate
// splits, the way a text selection does.
func alignUTF16(s string, start, length int64) (int64, int64) {
	u := utf16.Encode([]rune(s))
	isLow := func(i int64) bool { return i < int64(len(u)) && u[i] >= 0xDC00 && u[i] <= 0xDFFF }
	for isLow(start) && start > 0 {
		start--
		if length > 0 {
			length++
		}
	}
	for length > 0 && isLow(start+length) {
		length++
	}
	if start+length > int64(len(u)) {
		length = int64(len(u)) - start
	}
	return start, length
}

type nopObserver struct{}

func (nopObserver) OnUpdate([]byte, bool) {}
