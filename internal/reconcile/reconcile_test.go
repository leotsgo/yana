package reconcile

import (
	"context"
	"database/sql"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/index"
)

// Acceptance 1: two simulated clients type concurrently into one note.
func TestTwoClientsTypeConcurrently(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("work/note.md", "# Title\n\n")
	a := h.newClient("a", id)
	b := h.newClient("b", id)
	defer a.close()
	defer b.close()

	var wg sync.WaitGroup
	for _, c := range []*client{a, b} {
		wg.Add(1)
		go func(c *client) {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				c.appendText(c.name)
				if i%7 == 0 {
					c.insertAt(9, "<"+c.name+">")
				}
			}
		}(c)
	}
	wg.Wait()
	body := h.converged(id, a, b)
	if strings.Count(body, "a") < 40 || strings.Count(body, "b") < 40 {
		t.Fatalf("text lost: %q", body)
	}
	if st := h.rec.Stats(); st.Readins != 0 {
		t.Fatalf("client-only edits produced %d read-ins (echo suppression failed)", st.Readins)
	}
}

// Acceptance 2: external `echo >> note.md` while two clients have it open.
func TestExternalAppendWhileOpen(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("work/note.md", "line one\n")
	a := h.newClient("a", id)
	b := h.newClient("b", id)
	defer a.close()
	defer b.close()

	a.appendText("from a\n")
	b.insertAt(0, "b first\n")
	// The clients' text has not reached the file yet (idle timer); the
	// append lands on the previous file content.
	h.appendFile("work/note.md", "appended by shell\n")
	a.appendText("a again\n")

	body := h.converged(id, a, b)
	for _, want := range []string{"line one\n", "from a\n", "b first\n", "appended by shell\n", "a again\n"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %q", want, body)
		}
	}
	if st := h.rec.Stats(); st.Readins == 0 {
		t.Fatal("the external append never produced a read-in")
	}
}

// Acceptance 3: external full-file replacement mid-edit.
func TestExternalReplaceMidEdit(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("work/note.md", "alpha\nbeta\ngamma\n")
	a := h.newClient("a", id)
	defer a.close()

	a.insertAt(utf16Len("alpha\n"), "a-was-here\n")
	// A truncate+write of the whole file with different content.
	h.replaceFile("work/note.md", "alpha\nbeta replaced\ngamma\ndelta\n")

	body := h.converged(id, a)
	for _, want := range []string{"beta replaced\n", "delta\n", "a-was-here\n"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %q", want, body)
		}
	}
	if strings.Contains(body, "beta\n") {
		t.Fatalf("replaced text survived: %q", body)
	}
}

// Acceptance 4: rapid alternating client and filesystem writes. The write
// count must stay bounded and the loop must settle.
func TestOscillation(t *testing.T) {
	duration := 60 * time.Second
	if testing.Short() {
		duration = 5 * time.Second
	}
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("work/note.md", "start\n")
	a := h.newClient("a", id)
	defer a.close()

	start := time.Now()
	clientOps, fsOps := 0, 0
	for time.Since(start) < duration {
		a.appendText("c\n")
		clientOps++
		time.Sleep(5 * time.Millisecond)
		h.appendFile("work/note.md", "f\n")
		fsOps++
		time.Sleep(5 * time.Millisecond)
	}
	body := h.converged(id, a)
	if got := strings.Count(body, "c\n"); got != clientOps {
		t.Fatalf("client lines: %d of %d survived", got, clientOps)
	}
	if got := strings.Count(body, "f\n"); got != fsOps {
		t.Fatalf("filesystem lines: %d of %d survived", got, fsOps)
	}
	st := h.rec.Stats()
	// One write-back per idle window at most, plus a few for the tail.
	limit := int64(duration/tIdle) + 10
	if st.Writebacks > limit {
		t.Fatalf("%d write-backs for %v (limit %d): the loop is oscillating", st.Writebacks, duration, limit)
	}
	// Settled: nothing moves once the writers stop.
	time.Sleep(5 * tIdle)
	after := h.rec.Stats()
	if after.Writebacks != st.Writebacks || after.Readins != st.Readins {
		t.Fatalf("loop did not settle: %+v then %+v", st, after)
	}
	t.Logf("%d client ops, %d fs ops, %d write-backs, %d read-ins, %d echoes", clientOps, fsOps, st.Writebacks, st.Readins, st.Echoes)
}

// Acceptance 6: mv a note to a new directory while a client has it open.
func TestMoveWhileOpen(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("work/inbox/note.md", "moving\n")
	a := h.newClient("a", id)
	defer a.close()
	a.appendText("before move\n")
	h.converged(id, a)

	if err := os.MkdirAll(h.abs("work/archive/2026"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(h.abs("work/inbox/note.md"), h.abs("work/archive/2026/note.md")); err != nil {
		t.Fatal(err)
	}
	if !h.eventually(20*tSettle, func() bool {
		p, err := h.rec.Path(h.ctx, id)
		return err == nil && p == "work/archive/2026/note.md"
	}) {
		p, _ := h.rec.Path(h.ctx, id)
		t.Fatalf("move not noticed; path is %q", p)
	}
	a.appendText("after move\n")
	body := h.converged(id, a)
	if !strings.Contains(body, "after move\n") || !strings.Contains(body, "before move\n") {
		t.Fatalf("text after move: %q", body)
	}
	if _, err := os.Stat(h.abs("work/inbox/note.md")); err == nil {
		t.Fatal("old path came back")
	}
	n, err := h.db.GetNote(h.ctx, id)
	if err != nil || n.RelPath != "work/archive/2026/note.md" {
		t.Fatalf("index path %q err %v", n.RelPath, err)
	}
	if _, err := h.db.GetNoteByPath(h.ctx, "work/inbox/note.md"); err == nil {
		t.Fatal("index still has the old path")
	}
}

// A note edited only by clients never reads itself back in.
func TestEchoSuppression(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("n.md", "x\n")
	a := h.newClient("a", id)
	defer a.close()
	for i := 0; i < 5; i++ {
		a.appendText("y\n")
		time.Sleep(2 * tIdle)
	}
	h.converged(id, a)
	time.Sleep(3 * tSettle)
	st := h.rec.Stats()
	if st.Readins != 0 {
		t.Fatalf("%d read-ins from own write-backs", st.Readins)
	}
	if st.Echoes == 0 {
		t.Fatal("no echoes were seen; is the watcher running?")
	}
}

// Restarting keeps the document and the file in step, and an edit made
// while the server was down is absorbed on open.
func TestRestartAndEditWhileDown(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, testOptions())
	id := h.newNote("n.md", "one\n")
	a := h.newClient("a", id)
	a.appendText("two\n")
	h.converged(id, a)
	a.close()
	h.close()

	if _, err := os.Stat(filepath.Join(dir, ".sync", "crdt", id+".bin")); err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	// Edit while down.
	content, _ := os.ReadFile(filepath.Join(dir, "n.md"))
	if err := os.WriteFile(filepath.Join(dir, "n.md"), append(content, "three\n"...), 0o644); err != nil {
		t.Fatal(err)
	}

	h = newHarness(t, dir, testOptions())
	defer h.close()
	body := h.converged(id)
	if body != "one\ntwo\nthree\n" {
		t.Fatalf("after restart: %q", body)
	}
	// And unflushed edits at shutdown are written by Close.
	b := h.newClient("b", id)
	defer b.close()
	b.appendText("four\n")
	h.rec.FlushAll(h.ctx)
	if got := h.fileBody("n.md"); got != "one\ntwo\nthree\nfour\n" {
		t.Fatalf("flush: %q", got)
	}
}

// Deleting the file retires the sidecar; a file that comes back with the
// same id gets it back.
func TestDeleteRetainsSidecar(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("n.md", "keep me\n")
	a := h.newClient("a", id)
	a.appendText("edited\n")
	h.converged(id, a)
	a.close()
	var deleted bool
	var mu sync.Mutex
	stop := h.rec.Subscribe(func(ev Event) {
		if ev.NoteID == id && ev.Kind == EventDeleted {
			mu.Lock()
			deleted = true
			mu.Unlock()
		}
	})
	defer stop()
	saved := h.read("n.md")
	if err := os.Remove(h.abs("n.md")); err != nil {
		t.Fatal(err)
	}
	if !h.eventually(20*tSettle, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return deleted
	}) {
		t.Fatal("deletion not reported")
	}
	if _, err := os.Stat(h.rec.retiredPath(id)); err != nil {
		t.Fatalf("retired sidecar: %v", err)
	}
	if _, err := os.Stat(h.rec.sidecarPath(id)); err == nil {
		t.Fatal("active sidecar still present")
	}
	if !h.eventually(10*tSettle, func() bool {
		_, err := h.db.GetNote(h.ctx, id)
		return err != nil
	}) {
		t.Fatal("index row not retired")
	}
	if _, err := h.rec.Text(h.ctx, id); err == nil {
		t.Fatal("deleted note still opens")
	}

	// Bring it back (rsync put it back, say).
	if err := os.WriteFile(h.abs("n.md"), []byte(saved), 0o644); err != nil {
		t.Fatal(err)
	}
	if !h.eventually(20*tSettle, func() bool {
		_, err := h.db.GetNote(h.ctx, id)
		return err == nil
	}) {
		t.Fatal("restored file not indexed")
	}
	if got := h.converged(id); got != "keep me\nedited\n" {
		t.Fatalf("restored: %q", got)
	}
	if _, err := os.Stat(h.rec.sidecarPath(id)); err != nil {
		t.Fatalf("sidecar not restored: %v", err)
	}
	if _, err := os.Stat(h.rec.retiredPath(id)); err == nil {
		t.Fatal("retired copy still present")
	}
}

// A hand-written file without an id gets one from the loop, then behaves
// like any other note.
func TestNewFileGetsID(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	if err := os.WriteFile(h.abs("fresh.md"), []byte("# Fresh\n\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var id string
	if !h.eventually(20*tSettle, func() bool {
		n, err := h.db.GetNoteByPath(h.ctx, "fresh.md")
		if err != nil {
			return false
		}
		id = n.ID
		return true
	}) {
		t.Fatal("new file never indexed")
	}
	if fm := frontmatter.Parse([]byte(h.read("fresh.md"))); fm.Meta.ID != id {
		t.Fatalf("file id %q, index id %q", fm.Meta.ID, id)
	}
	a := h.newClient("a", id)
	defer a.close()
	a.appendText("typed\n")
	if got := h.converged(id, a); got != "# Fresh\n\nhello\ntyped\n" {
		t.Fatalf("body: %q", got)
	}
}

// A truncate+write editor shows an empty file for a moment. That must not
// erase the note.
func TestTransientEmptyFile(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("n.md", "important\n")
	a := h.newClient("a", id)
	defer a.close()
	h.converged(id, a)
	head := frontmatter.Parse([]byte(h.read("n.md"))).Head

	// Truncate, wait long enough for the watcher to look, then write.
	if err := os.WriteFile(h.abs("n.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * tDebounce)
	if err := os.WriteFile(h.abs("n.md"), append(append([]byte{}, head...), "important\nsaved\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := h.converged(id, a); got != "important\nsaved\n" {
		t.Fatalf("body: %q", got)
	}
	if st := h.rec.Stats(); st.Loaded != 1 {
		t.Fatalf("note was retired: %+v", st)
	}
}

// Frontmatter edits by hand are kept and do not touch the body.
func TestFrontmatterOnlyEdit(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("n.md", "body\n")
	a := h.newClient("a", id)
	defer a.close()
	h.converged(id, a)
	content := h.read("n.md")
	content = strings.Replace(content, "---\n", "---\norder: 3\n", 1)
	if err := os.WriteFile(h.abs("n.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !h.eventually(20*tSettle, func() bool {
		n, err := h.db.GetNote(h.ctx, id)
		return err == nil && n.Order != nil && *n.Order == 3
	}) {
		t.Fatal("order not indexed")
	}
	a.appendText("more\n")
	h.converged(id, a)
	got := h.read("n.md")
	if !strings.HasPrefix(got, "---\norder: 3\nid: ") || !strings.HasSuffix(got, "body\nmore\n") {
		t.Fatalf("write-back lost the frontmatter edit: %q", got)
	}
}

// The log is compacted into a snapshot and the snapshot loads.
func TestCompaction(t *testing.T) {
	opts := testOptions()
	opts.CompactAfter = 5
	dir := t.TempDir()
	h := newHarness(t, dir, opts)
	id := h.newNote("n.md", "")
	a := h.newClient("a", id)
	for i := 0; i < 12; i++ {
		a.appendText("l\n")
	}
	h.converged(id, a)
	a.close()
	snap, ok, err := h.db.GetSnapshot(h.ctx, id)
	if err != nil || !ok {
		t.Fatalf("snapshot: ok=%v err=%v", ok, err)
	}
	rows, err := h.db.Updates(h.ctx, id, snap.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) >= 5 {
		t.Fatalf("%d rows after compaction", len(rows))
	}
	all, _ := h.db.Updates(h.ctx, id, 0)
	if len(all) != len(rows) {
		t.Fatalf("compacted rows still present: %d", len(all)-len(rows))
	}
	h.close()

	// Delete the sidecar: the snapshot and log alone must rebuild the text.
	if err := os.Remove(filepath.Join(dir, ".sync", "crdt", id+".bin")); err != nil {
		t.Fatal(err)
	}
	h = newHarness(t, dir, opts)
	defer h.close()
	if got := h.converged(id); got != strings.Repeat("l\n", 12) {
		t.Fatalf("after reload: %q", got)
	}
}

// Every logged update carries an author.
func TestAuthorsRecorded(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("n.md", "seed\n")
	a := h.newClient("a", id)
	defer a.close()
	a.appendText("typed\n")
	h.converged(id, a)
	h.appendFile("n.md", "shell\n")
	h.converged(id, a)
	rows, err := h.db.Updates(h.ctx, id, 0)
	if err != nil {
		t.Fatal(err)
	}
	authors := map[string]int{}
	for _, u := range rows {
		authors[u.Author]++
	}
	if authors["user:a"] == 0 || authors[AuthorFilesystem] < 2 {
		t.Fatalf("authors: %v", authors)
	}
	if err := h.rec.ApplyUpdate(h.ctx, id, []byte{0, 0}, "", nil); err == nil {
		t.Fatal("empty author accepted")
	}
	err = h.db.Write(h.ctx, func(tx *sql.Tx) error {
		return index.AppendUpdate(tx, id, 9999, []byte{0, 0}, "", time.Now())
	})
	if err == nil {
		t.Fatal("index accepted an update without an author")
	}
}

// Deleting index.db loses nothing: ids, text, and the CRDT sidecar all
// survive a rebuild.
func TestDeleteIndexAndRestart(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, dir, testOptions())
	id := h.newNote("n.md", "one\n")
	a := h.newClient("a", id)
	a.appendText("two\n")
	h.converged(id, a)
	a.close()
	h.close()
	for _, f := range []string{"index.db", "index.db-wal", "index.db-shm"} {
		os.Remove(filepath.Join(dir, ".sync", f))
	}
	h = newHarness(t, dir, testOptions())
	defer h.close()
	if got := h.converged(id); got != "one\ntwo\n" {
		t.Fatalf("after index rebuild: %q", got)
	}
	b := h.newClient("b", id)
	defer b.close()
	b.appendText("three\n")
	if got := h.converged(id, b); got != "one\ntwo\nthree\n" {
		t.Fatalf("edit after rebuild: %q", got)
	}
}

// Property test: random interleavings of client edits, file edits, moves
// and flushes always end in three-way convergence with no text lost.
func TestPropertyRandomInterleavings(t *testing.T) {
	seeds := 12
	if testing.Short() {
		seeds = 4
	}
	for seed := 0; seed < seeds; seed++ {
		t.Run("", func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seed)))
			h := newHarness(t, "", testOptions())
			defer h.close()
			rel := "s/n.md"
			id := h.newNote(rel, "seed\n")
			clients := []*client{h.newClient("a", id), h.newClient("b", id)}
			defer func() {
				for _, c := range clients {
					c.close()
				}
			}()
			var wantLines []string
			token := 0
			next := func(kind string) string {
				token++
				s := kind + "-" + itoa(token) + "\n"
				wantLines = append(wantLines, s)
				return s
			}
			steps := 15 + rng.Intn(25)
			for i := 0; i < steps; i++ {
				switch rng.Intn(10) {
				case 0, 1, 2, 3:
					c := clients[rng.Intn(len(clients))]
					c.insertLine(rng, next("c"+c.name))
				case 4:
					c := clients[rng.Intn(len(clients))]
					// Delete inside the seed word only, so every token line
					// is still expected to survive.
					if i := strings.Index(c.text(), "seed"); i >= 0 {
						c.deleteAt(utf16Len(c.text()[:i]), 1)
					}
				case 5, 6:
					// Wait for the file to hold the current text before the
					// shell touches it; a shell writer sees the file, not
					// the document, and that is exactly the race under test
					// in case 7.
					h.appendFile(rel, next("f"))
				case 7:
					h.appendFile(rel, next("f"))
					clients[0].appendText(next("ca"))
				case 8:
					time.Sleep(time.Duration(rng.Intn(int(2 * tIdle))))
				case 9:
					_ = h.rec.Flush(h.ctx, id)
				}
			}
			body := h.converged(id, clients...)
			for _, line := range wantLines {
				if !strings.Contains(body, line) {
					t.Fatalf("seed %d: lost %q in %q", seed, line, body)
				}
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Copying a note (same id at two paths) gives the copy a fresh id.
func TestCopyGetsFreshID(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("a.md", "orig\n")
	a := h.newClient("a", id)
	defer a.close()
	h.converged(id, a)
	content := h.read("a.md")
	if err := os.WriteFile(h.abs("b.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if !h.eventually(20*tSettle, func() bool {
		n, err := h.db.GetNoteByPath(h.ctx, "b.md")
		return err == nil && n.ID != id
	}) {
		t.Fatal("copy did not get a fresh id")
	}
	if p, _ := h.rec.Path(h.ctx, id); p != "a.md" {
		t.Fatalf("original moved to %q", p)
	}
}

// A shell append that lands between the write-back's read of the file and
// its rename would go to an inode nothing points at any more. It is
// recovered from the handle the write-back kept open.
func TestAppendRacingWriteBackIsRecovered(t *testing.T) {
	h := newHarness(t, "", testOptions())
	defer h.close()
	id := h.newNote("n.md", "base\n")
	a := h.newClient("a", id)
	defer a.close()
	h.converged(id, a)

	h.rec.testBeforeWrite = func() { h.appendFile("n.md", "late\n") }
	a.appendText("typed\n")
	if err := h.rec.Flush(h.ctx, id); err != nil {
		t.Fatal(err)
	}
	h.rec.testBeforeWrite = nil
	body := h.converged(id, a)
	if !strings.Contains(body, "late\n") || !strings.Contains(body, "typed\n") {
		t.Fatalf("late write lost: %q", body)
	}
}

// Pinned notes stay loaded; unpinned idle ones are dropped and reload
// cleanly.
func TestUnloadIdle(t *testing.T) {
	opts := testOptions()
	opts.UnloadAfter = time.Millisecond
	h := newHarness(t, "", opts)
	defer h.close()
	id := h.newNote("n.md", "x\n")
	other := h.newNote("m.md", "y\n")
	release, err := h.rec.Pin(h.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.rec.Text(h.ctx, other); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	h.rec.unloadIdle()
	if st := h.rec.Stats(); st.Loaded != 1 {
		t.Fatalf("loaded %d, want the pinned one only", st.Loaded)
	}
	release()
	if txt, err := h.rec.Text(h.ctx, other); err != nil || txt != "y\n" {
		t.Fatalf("reload: %q %v", txt, err)
	}
}

var _ = context.Background
var _ index.Note
