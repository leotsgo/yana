package reconcile

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestFlushLoopKeepsEveryLine drives append-plus-flush as fast as possible
// while the watcher delivers the write-backs back into the loop. The
// watcher used to read the file before taking the note lock; when a
// write-back landed in between, that stale copy was applied as an external
// edit and deleted freshly typed lines. This test keeps typing in that
// window and asserts no line is ever lost.
func TestFlushLoopKeepsEveryLine(t *testing.T) {
	if testing.Short() {
		t.Skip("shrinks in short mode")
	}
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("stress/lines.md", "")
	c := h.newClient("s", id)

	next := 1
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.appendText(fmt.Sprintf("line %d\n", next))
		if err := h.rec.Flush(h.ctx, id); err != nil {
			t.Fatal(err)
		}
		next++
	}
	h.converged(id, c)
	if got := c.text(); got != "" {
		lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
		for i, l := range lines {
			if l != fmt.Sprintf("line %d", i+1) {
				t.Fatalf("line %d of the document reads %q; a write was lost or reverted", i+1, l)
			}
		}
		if len(lines) != next-1 {
			t.Fatalf("document holds %d lines after %d appends", len(lines), next-1)
		}
	}
}
