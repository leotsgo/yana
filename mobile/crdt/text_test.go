package crdt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	ycrdt "github.com/reearth/ygo/crdt"
)

// collector gathers text events from an observer; the drain goroutine
// calls back on its own thread.
type collector struct {
	mu     sync.Mutex
	events []textEvent
	texts  []string
}

func (c *collector) OnText(text string, delta string, local bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, textEvent{delta: delta, local: local})
	c.texts = append(c.texts, text)
}

func (c *collector) snapshot() ([]textEvent, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ev := append([]textEvent(nil), c.events...)
	tx := append([]string(nil), c.texts...)
	return ev, tx
}

func (c *collector) wait(t *testing.T, n int) ([]textEvent, []string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ev, tx := c.snapshot()
		if len(ev) >= n {
			return ev, tx
		}
		time.Sleep(2 * time.Millisecond)
	}
	ev, _ := c.snapshot()
	t.Fatalf("timed out waiting for %d text events, got %d (%v)", n, len(ev), ev)
	return nil, nil
}

func TestEditReplacesRangeInOneStep(t *testing.T) {
	d := NewDoc()
	defer d.Close()
	um := NewUndoManager(d)
	defer um.Destroy()
	if _, err := d.Insert(0, "hello world"); err != nil {
		t.Fatal(err)
	}

	// Type "XY" over "world": one Edit, one update, one undo step.
	u, err := d.Edit(6, 5, "XY")
	if err != nil || u == nil {
		t.Fatalf("edit: u=%v err=%v", u, err)
	}
	if got := d.Text(); got != "hello XY" {
		t.Fatalf("text = %q", got)
	}
	if n := um.UndoStackSize(); n != 2 { // insert hello world + the edit
		t.Fatalf("undo stack = %d, want 2", n)
	}
	if u, err := um.Undo(); err != nil || u == nil {
		t.Fatalf("undo: u=%v err=%v", u, err)
	}
	if got := d.Text(); got != "hello world" {
		t.Fatalf("after undo text = %q, want hello (one step)", got)
	}
	if u, err := um.Redo(); err != nil || u == nil {
		t.Fatalf("redo: u=%v err=%v", u, err)
	}
	if got := d.Text(); got != "hello XY" {
		t.Fatalf("after redo text = %q", got)
	}

	// Edge shapes: pure insert, pure delete, and a no-op.
	if u, err := d.Edit(5, 0, "!"); err != nil || u == nil {
		t.Fatalf("pure insert: u=%v err=%v", u, err)
	}
	if u, err := d.Edit(0, 2, ""); err != nil || u == nil {
		t.Fatalf("pure delete: u=%v err=%v", u, err)
	}
	if u, err := d.Edit(0, 0, ""); err != nil || u != nil {
		t.Fatalf("no-op: u=%v err=%v", u, err)
	}
	if got := d.Text(); got != "llo! XY" {
		t.Fatalf("text = %q, want llo! XY", got)
	}
	// Out-of-range replacements are refused, not clamped.
	if _, err := d.Edit(7, 2, "z"); err == nil {
		t.Fatal("edit past the end should fail")
	}
	if _, err := d.Edit(-1, 0, "z"); err == nil {
		t.Fatal("negative position accepted")
	}
	if _, err := d.Edit(0, -1, "z"); err == nil {
		t.Fatal("negative delete accepted")
	}
}

func TestEditManyAppliesHunksInOneStep(t *testing.T) {
	d := NewDoc()
	defer d.Close()
	um := NewUndoManager(d)
	defer um.Destroy()
	if _, err := d.Insert(0, "one two three"); err != nil {
		t.Fatal(err)
	}

	// Wrap "one" and "three" in marks with one call, hunks given out of
	// order: one update, one undo step.
	ops := `[{"p":8,"d":0,"i":"**"},{"p":0,"d":0,"i":"**"},{"p":13,"d":0,"i":"**"},{"p":3,"d":0,"i":"**"}]`
	u, err := d.EditMany(ops)
	if err != nil || u == nil {
		t.Fatalf("editMany: u=%v err=%v", u, err)
	}
	if got := d.Text(); got != "**one** two **three**" {
		t.Fatalf("text = %q", got)
	}
	if n := um.UndoStackSize(); n != 2 { // the insert + the wrap
		t.Fatalf("undo stack = %d, want 2", n)
	}
	if _, err := um.Undo(); err != nil {
		t.Fatal(err)
	}
	if got := d.Text(); got != "one two three" {
		t.Fatalf("after undo text = %q, want one two three (one step)", got)
	}

	// Mixed shapes: turn two list lines into task lines.
	d.ReplaceText("- a\n- b")
	um2 := NewUndoManager(d)
	defer um2.Destroy()
	ops = `[{"p":2,"d":0,"i":"[ ] "},{"p":6,"d":0,"i":"[ ] "}]`
	if _, err := d.EditMany(ops); err != nil {
		t.Fatal(err)
	}
	if got := d.Text(); got != "- [ ] a\n- [ ] b" {
		t.Fatalf("text = %q", got)
	}

	// Malformed and bad inputs are refused, not clamped.
	if _, err := d.EditMany(`not json`); err == nil {
		t.Fatal("bad json accepted")
	}
	if _, err := d.EditMany(`[{"p":0,"d":99,"i":"x"}]`); err == nil {
		t.Fatal("out-of-range hunk accepted")
	}
	if _, err := d.EditMany(`[{"p":0,"d":4,"i":"x"},{"p":2,"d":0,"i":"y"}]`); err == nil {
		t.Fatal("overlapping hunks accepted")
	}
	if u, err := d.EditMany(`[{"p":0,"d":0,"i":""}]`); err != nil || u != nil {
		t.Fatalf("no-op: u=%v err=%v", u, err)
	}
}

func TestObserveTextDeliversHunks(t *testing.T) {
	d := NewDoc()
	defer d.Close()
	c := &collector{}
	sub := d.ObserveText(c)
	defer sub.Close()

	// Emoji (2 units) and a combining character pin the unit system.
	if _, err := d.Insert(0, "a😀e\u0301b"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Edit(3, 1, "😀!"); err != nil { // replace e with emoji+!
		t.Fatal(err)
	}
	if _, err := d.Delete(0, 1); err != nil { // drop the leading a
		t.Fatal(err)
	}

	// A remote peer's update reports local=false.
	peer := newDocID(t, 77)
	defer peer.Close()
	if _, err := peer.Insert(0, "a😀e\u0301b"); err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Edit(0, 1, "ZZ"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ApplyUpdate(peer.State()); err != nil {
		t.Fatal(err)
	}

	ev, tx := c.wait(t, 4)
	wantDelta := []string{
		`[{"p":0,"d":0,"i":6}]`,
		`[{"p":3,"d":1,"i":3}]`,
		`[{"p":0,"d":1,"i":0}]`,
		// The peer's whole state integrates as one transaction; where its
		// items land relative to ours is the CRDT's to say, so the exact
		// hunk is what the port computed, pinned here.
		`[{"p":0,"d":0,"i":7}]`,
	}
	wantLocal := []bool{true, true, true, false}
	for i, want := range wantDelta {
		if ev[i].delta != want {
			t.Fatalf("event %d delta = %s, want %s", i, ev[i].delta, want)
		}
		if ev[i].local != wantLocal[i] {
			t.Fatalf("event %d local = %v, want %v", i, ev[i].local, wantLocal[i])
		}
	}
	if got := tx[3]; got != d.Text() {
		t.Fatalf("event text = %q, want %q", got, d.Text())
	}
}

func TestObserveTextCoversUndoAndRedo(t *testing.T) {
	d := NewDoc()
	defer d.Close()
	um := NewUndoManager(d)
	defer um.Destroy()
	c := &collector{}
	sub := d.ObserveText(c)
	defer sub.Close()

	if _, err := d.Edit(0, 0, "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Edit(3, 0, " two"); err != nil {
		t.Fatal(err)
	}
	if _, err := um.Undo(); err != nil {
		t.Fatal(err)
	}
	if _, err := um.Redo(); err != nil {
		t.Fatal(err)
	}

	ev, _ := c.wait(t, 4)
	// Undo and redo are this device's work: their updates go to the
	// outbox like any local edit, so they must report local=true.
	if !ev[2].local || !ev[3].local {
		t.Fatalf("undo/redo events local = %v/%v, want true/true", ev[2].local, ev[3].local)
	}
	if got := d.Text(); got != "one two" {
		t.Fatalf("text = %q", got)
	}
	if ev[2].delta != `[{"p":3,"d":4,"i":0}]` {
		t.Fatalf("undo delta = %s", ev[2].delta)
	}
	if ev[3].delta != `[{"p":3,"d":0,"i":4}]` {
		t.Fatalf("redo delta = %s", ev[3].delta)
	}
}

func TestObserveTextAfterClose(t *testing.T) {
	d := NewDoc()
	c := &collector{}
	sub := d.ObserveText(c)
	d.Close()
	sub.Close() // idempotent against Close's own stop

	done := make(chan struct{})
	go func() {
		defer close(done)
		d.ObserveText(c).Close()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ObserveText on a closed doc did not return")
	}
}

// TestHunks pins the coalescing rules directly, over the port's own
// delta values.
func TestHunks(t *testing.T) {
	s := func(v string) any { return v }
	cases := []struct {
		name  string
		delta []ycrdt.Delta
		want  []textHunk
	}{
		{
			"insert then delete at same position is one replacement",
			[]ycrdt.Delta{{Op: ycrdt.DeltaOpInsert, Insert: s("XY")}, {Op: ycrdt.DeltaOpDelete, Delete: 3}},
			[]textHunk{{Pos: 0, Del: 3, Ins: 2}},
		},
		{
			"delete then insert at same position is one replacement",
			[]ycrdt.Delta{{Op: ycrdt.DeltaOpDelete, Delete: 3}, {Op: ycrdt.DeltaOpInsert, Insert: s("XY")}},
			[]textHunk{{Pos: 0, Del: 3, Ins: 2}},
		},
		{
			"separate regions stay separate hunks",
			[]ycrdt.Delta{{Op: ycrdt.DeltaOpRetain, Retain: 2}, {Op: ycrdt.DeltaOpDelete, Delete: 1}, {Op: ycrdt.DeltaOpRetain, Retain: 3}, {Op: ycrdt.DeltaOpInsert, Insert: s("z")}},
			[]textHunk{{Pos: 2, Del: 1}, {Pos: 6, Ins: 1}},
		},
		{
			"a plain insert is a zero-delete hunk",
			[]ycrdt.Delta{{Op: ycrdt.DeltaOpRetain, Retain: 4}, {Op: ycrdt.DeltaOpInsert, Insert: s("a")}},
			[]textHunk{{Pos: 4, Ins: 1}},
		},
		{
			"an emoji insert counts two units",
			[]ycrdt.Delta{{Op: ycrdt.DeltaOpInsert, Insert: s("😀")}},
			[]textHunk{{Pos: 0, Ins: 2}},
		},
		{
			"a retain-only delta carries no hunks",
			[]ycrdt.Delta{{Op: ycrdt.DeltaOpRetain, Retain: 5}},
			nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := hunks(c.delta)
			if len(got) != len(c.want) {
				t.Fatalf("hunks = %+v, want %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("hunk %d = %+v, want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestObserveTextReplacementCoalesces(t *testing.T) {
	d := NewDoc()
	defer d.Close()
	c := &collector{}
	sub := d.ObserveText(c)
	defer sub.Close()

	if _, err := d.Insert(0, "hello world"); err != nil {
		t.Fatal(err)
	}
	// ReplaceText rewrites "hello" as "bonjour": the diff is a delete and
	// an insert at the same position, which must arrive as one hunk.
	if u := d.ReplaceText("bonjour world"); u == nil {
		t.Fatal("replace produced no update")
	}

	ev, _ := c.wait(t, 2)
	var hs []textHunk
	if err := json.Unmarshal([]byte(ev[1].delta), &hs); err != nil {
		t.Fatal(err)
	}
	if len(hs) != 1 || hs[0].Pos != 0 || hs[0].Del != 5 || hs[0].Ins != 7 {
		t.Fatalf("hunks = %+v, want one p=0 d=5 i=7", hs)
	}
}

func TestRelativePositionJSONRoundTrip(t *testing.T) {
	d := NewDoc()
	defer d.Close()
	if _, err := d.Insert(0, "a😀e\u0301b"); err != nil {
		t.Fatal(err)
	}
	// Len is 6 UTF-16 units; every boundary maps back to itself.
	for _, idx := range []int64{0, 1, 2, 3, 4, 5, 6, 99} {
		b, err := d.RelativePositionJSON(idx, 0)
		if err != nil {
			t.Fatalf("position %d: %v", idx, err)
		}
		if got := d.ResolveRelativePositionJSON(b); got != int64(min(idx, 6)) {
			t.Fatalf("position %d resolved to %d (%s)", idx, got, b)
		}
	}
	// The end stays at the end as the text grows.
	end, err := d.RelativePositionJSON(d.Len(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Insert(d.Len(), " and more"); err != nil {
		t.Fatal(err)
	}
	if got := d.ResolveRelativePositionJSON(end); got != d.Len() {
		t.Fatalf("end position resolved to %d, want %d", got, d.Len())
	}
	// A negative index is refused; garbage resolves to -1.
	if _, err := d.RelativePositionJSON(-1, 0); err == nil {
		t.Fatal("negative index accepted")
	}
	if got := d.ResolveRelativePositionJSON([]byte("not json")); got != -1 {
		t.Fatalf("garbage resolved to %d", got)
	}
	// A position anchored to a nested type (type non-null) is refused.
	nested := strings.Replace(`{"type":{"client":1,"clock":0},"tname":null,"item":null,"assoc":0}`, " ", "", -1)
	if got := d.ResolveRelativePositionJSON([]byte(nested)); got != -1 {
		t.Fatalf("nested-type position resolved to %d, want -1", got)
	}
	// The web's omission form (fields absent instead of null) decodes
	// too: createRelativePositionFromJSON accepts it.
	omitted := `{"tname":"body","assoc":0}`
	if got := d.ResolveRelativePositionJSON([]byte(omitted)); got != d.Len() {
		t.Fatalf("omitted-field form resolved to %d, want %d (end)", got, d.Len())
	}
}

// TestPositionCrossLanguage pins the JSON positions against the
// reference: a position the reference builds resolves here to the index
// it reports, and one built here resolves there the same way — through
// a concurrent edit, the reason positions exist.
func TestPositionCrossLanguage(t *testing.T) {
	dir := t.TempDir()
	abs := func(name string) string {
		p, _ := filepath.Abs(filepath.Join(dir, name))
		return p
	}

	// The reference's document, with emoji and a combining character.
	jsText := node(t, "encode", abs("js.bin"))
	raw, err := os.ReadFile(abs("js.bin"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := LoadDoc(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Text() != jsText {
		t.Fatalf("JS->Go text mismatch: %q vs %q", d.Text(), jsText)
	}

	// JS-built positions resolve here to the same index JS reports.
	runes := utf16.Encode([]rune(jsText))
	for _, idx := range []int{0, 1, 5, 9, len(runes) / 2, len(runes)} {
		p := abs("pos.json")
		jsIdx := node(t, "position", abs("js.bin"), strconv.Itoa(idx))
		if err := os.WriteFile(p, []byte(jsIdx), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := d.ResolveRelativePositionJSON([]byte(jsIdx)); got != int64(idx) {
			t.Fatalf("JS position %d resolved here to %d (%s)", idx, got, jsIdx)
		}
		_ = p
	}

	// A position built here resolves in the reference to the same
	// index, including after the reference document moves on.
	for _, idx := range []int64{0, 3, 7, int64(len(runes))} {
		b, err := d.RelativePositionJSON(idx, 0)
		if err != nil {
			t.Fatal(err)
		}
		p := abs("go.json")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := node(t, "resolve", abs("js.bin"), p); got != strconv.FormatInt(idx, 10) {
			t.Fatalf("Go position %d resolved in JS to %s", idx, got)
		}
	}
}
