package crdt

import (
	"sync"
	"testing"
	"time"
)

// TestScopedUndo runs two writers in one document, A through the bind
// package's mutators and B as a peer whose updates arrive as bytes. A's
// undo steps back through A's edits only, B's text survives every one of
// them, and every undo reaches B as a converging update.
func TestScopedUndo(t *testing.T) {
	a := newDocID(t, 1)
	defer a.Close()
	b := newDocID(t, 2)
	defer b.Close()

	apply := func(d *Doc, u []byte, what string) {
		t.Helper()
		if _, err := d.ApplyUpdate(u); err != nil {
			t.Fatalf("apply %s: %v", what, err)
		}
	}

	// The document starts with A's "hello ", synced to B before A has
	// an undo manager, the way a loaded note looks.
	if _, err := a.Insert(0, "hello "); err != nil {
		t.Fatal(err)
	}
	apply(b, a.State(), "initial state")

	um := NewUndoManager(a)
	defer um.Destroy()

	// A types; B types; both see everything.
	ua1, err := a.Insert(6, "big ")
	if err != nil {
		t.Fatal(err)
	}
	apply(b, ua1, "A's first edit")
	bu, err := b.Insert(0, "say: ")
	if err != nil {
		t.Fatal(err)
	}
	apply(a, bu, "B's edit")
	ua2, err := a.Insert(15, "world")
	if err != nil {
		t.Fatal(err)
	}
	apply(b, ua2, "A's second edit")
	want := "say: hello big world"
	if a.Text() != want || b.Text() != want {
		t.Fatalf("before undo: a=%q b=%q", a.Text(), b.Text())
	}

	// Undo steps back through A's edits only, newest first, and B's
	// text survives each one.
	u1, err := um.Undo()
	if err != nil || u1 == nil {
		t.Fatalf("undo 1: %v %v", u1, err)
	}
	apply(b, u1, "undo 1")
	if want := "say: hello big "; a.Text() != want || b.Text() != want {
		t.Fatalf("after undo 1: a=%q b=%q", a.Text(), b.Text())
	}
	u2, err := um.Undo()
	if err != nil || u2 == nil {
		t.Fatalf("undo 2: %v %v", u2, err)
	}
	apply(b, u2, "undo 2")
	if want := "say: hello "; a.Text() != want || b.Text() != want {
		t.Fatalf("after undo 2: a=%q b=%q", a.Text(), b.Text())
	}
	if got := um.UndoStackSize(); got != 0 {
		t.Fatalf("B's edit and the pre-manager edit must be off the stack: %d", got)
	}
	// B's "say: " and the initial "hello " are both outside A's scope.
	if u, err := um.Undo(); err != nil || u != nil {
		t.Fatalf("undo must refuse what it never tracked: %v %v", u, err)
	}

	// Redo brings A's edit back, on both sides.
	if _, err := um.Redo(); err != nil {
		t.Fatal(err)
	}
	sv := b.StateVector()
	rd, err := a.Diff(sv)
	if err != nil {
		t.Fatal(err)
	}
	apply(b, rd, "redo delta")
	if want := "say: hello big "; a.Text() != want || b.Text() != want {
		t.Fatalf("after redo: a=%q b=%q", a.Text(), b.Text())
	}
}

// TestUndoFirstEditOnFreshDoc guards the acceptance property on the
// hardest case for the port: the very first edit on an empty document.
func TestUndoFirstEditOnFreshDoc(t *testing.T) {
	d := NewDoc()
	defer d.Close()
	um := NewUndoManager(d)
	defer um.Destroy()
	for _, s := range []string{"a", "b", "c"} {
		if _, err := d.Insert(d.Len(), s); err != nil {
			t.Fatal(err)
		}
	}
	if got := um.UndoStackSize(); got != 3 {
		t.Fatalf("one step per edit: %d", got)
	}
	for i, want := range []string{"ab", "a", ""} {
		if _, err := um.Undo(); err != nil {
			t.Fatal(err)
		}
		if got := d.Text(); got != want {
			t.Fatalf("undo %d left %q, want %q", i, got, want)
		}
	}
	if u, err := um.Undo(); err != nil || u != nil {
		t.Fatalf("undo on empty stack: %v %v", u, err)
	}
	for i, want := range []string{"a", "ab", "abc"} {
		if _, err := um.Redo(); err != nil {
			t.Fatal(err)
		}
		if got := d.Text(); got != want {
			t.Fatalf("redo %d left %q, want %q", i, got, want)
		}
	}
	um.Clear()
	if got := um.UndoStackSize() + um.RedoStackSize(); got != 0 {
		t.Fatalf("clear must empty both stacks: %d", got)
	}
}

// recorder collects observer deliveries; tests wait on it because
// delivery is asynchronous.
type recorder struct {
	mu       sync.Mutex
	doc      *Doc // read from inside OnUpdate, for re-entrancy
	updates  [][]byte
	locality []bool
	texts    []string
	signal   chan struct{}
}

func newRecorder(d *Doc) *recorder {
	return &recorder{doc: d, signal: make(chan struct{}, 64)}
}

func (r *recorder) OnUpdate(update []byte, local bool) {
	// Reading the doc from the callback must not deadlock.
	text := r.doc.Text()
	r.mu.Lock()
	r.updates = append(r.updates, update)
	r.locality = append(r.locality, local)
	r.texts = append(r.texts, text)
	r.mu.Unlock()
	r.signal <- struct{}{}
}

func (r *recorder) wait(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		r.mu.Lock()
		got := len(r.updates)
		r.mu.Unlock()
		if got >= n {
			return
		}
		select {
		case <-r.signal:
		case <-deadline:
			t.Fatalf("observer received %d of %d updates", got, n)
		}
	}
}

func (r *recorder) snapshot() (updates [][]byte, locality []bool, texts []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updates, r.locality, r.texts
}

func TestObserverDelivery(t *testing.T) {
	d := newDocID(t, 1)
	defer d.Close()
	rec := newRecorder(d)
	sub := d.ObserveUpdates(rec)
	defer sub.Close()

	peer := newDocID(t, 2)
	defer peer.Close()

	um := NewUndoManager(d)
	defer um.Destroy()

	// Two local edits.
	if _, err := d.Insert(0, "one "); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Insert(d.Len(), "two "); err != nil {
		t.Fatal(err)
	}
	// One remote edit, delivered as an update.
	pu, err := peer.Insert(0, "peer ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ApplyUpdate(pu); err != nil {
		t.Fatal(err)
	}
	// One undo.
	if _, err := um.Undo(); err != nil {
		t.Fatal(err)
	}
	rec.wait(t, 4)

	updates, locality, _ := rec.snapshot()
	if len(updates) != 4 {
		t.Fatalf("got %d updates", len(updates))
	}
	wantLocal := []bool{true, true, false, true}
	for i, l := range wantLocal {
		if locality[i] != l {
			t.Fatalf("update %d local=%v, want %v", i, locality[i], l)
		}
	}

	// The updates apply, in delivery order, onto a fresh replica and
	// reach the same text: delivery order is commit order.
	fresh, err := LoadDoc(peer.State())
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	for i, u := range updates {
		if _, err := fresh.ApplyUpdate(u); err != nil {
			t.Fatalf("replay update %d: %v", i, err)
		}
	}
	if fresh.Text() != d.Text() {
		t.Fatalf("replayed updates reached %q, doc is %q", fresh.Text(), d.Text())
	}

	// Closing the subscription stops delivery.
	sub.Close()
	if _, err := d.Insert(0, "after close "); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, _, n := func() ([][]byte, []bool, int) { u, l, _ := rec.snapshot(); return u, l, len(u) }(); n != 4 {
		t.Fatalf("subscription delivered after close: %d updates", n)
	}
}
