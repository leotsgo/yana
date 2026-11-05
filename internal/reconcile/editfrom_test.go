package reconcile

import (
	"errors"
	"testing"
)

// A remote editor saves twice from what it fetched while the note gets
// an append and a deletion in between: every edit survives, and the
// second save does not undo the append it never saw.
func TestEditFromKeepsConcurrentEdits(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("work/note.md", "one\ntwo\nthree\n")
	text, base, err := h.rec.Snapshot(h.ctx, id)
	if err != nil || text != "one\ntwo\nthree\n" {
		t.Fatalf("snapshot %q %v", text, err)
	}
	if err := h.rec.SetText(h.ctx, id, "one\nthree\nfour\n", "user:web"); err != nil {
		t.Fatal(err)
	}
	merged, next, err := h.rec.EditFrom(h.ctx, id, base, "ONE\ntwo\nthree\n", "user:cli")
	if err != nil {
		t.Fatal(err)
	}
	if merged != "ONE\nthree\nfour\n" {
		t.Fatalf("first save merged to %q", merged)
	}
	merged, _, err = h.rec.EditFrom(h.ctx, id, next, "ONE\ntwo\nthree\nfive\n", "user:cli")
	if err != nil {
		t.Fatal(err)
	}
	// four (the web's) and five (the editor's) went in at one place at
	// once; the document may order them either way, and both stay.
	if merged != "ONE\nthree\nfour\nfive\n" && merged != "ONE\nthree\nfive\nfour\n" {
		t.Fatalf("second save merged to %q", merged)
	}

	// Unchanged text is no update at all.
	now, cur, _ := h.rec.Snapshot(h.ctx, id)
	logRows := func() int {
		h.rec.mu.Lock()
		n := h.rec.notes[id]
		h.rec.mu.Unlock()
		n.mu.Lock()
		defer n.mu.Unlock()
		return n.logRows
	}
	rows := logRows()
	if _, back, err := h.rec.EditFrom(h.ctx, id, cur, now, "user:cli"); err != nil || string(back) != string(cur) {
		t.Fatalf("no-op save: %v", err)
	}
	if logRows() != rows {
		t.Fatal("a no-op save recorded an update")
	}

	// A base the note never held is refused rather than merged as new text.
	other := h.newNote("work/other.md", "elsewhere\n")
	_, foreign, _ := h.rec.Snapshot(h.ctx, other)
	if _, _, err := h.rec.EditFrom(h.ctx, id, foreign, "x", "user:cli"); !errors.Is(err, ErrBadBase) {
		t.Fatalf("foreign base: %v", err)
	}
	if _, _, err := h.rec.EditFrom(h.ctx, id, []byte("garbage"), "x", "user:cli"); !errors.Is(err, ErrBadBase) {
		t.Fatalf("garbage base: %v", err)
	}
}
