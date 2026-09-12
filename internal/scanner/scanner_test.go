package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
)

type fixture struct {
	dir  string
	root *pathsafe.Root
	db   *index.DB
	sc   *Scanner
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{dir: dir, root: root, db: db}
	f.sc = New(root, db, Options{SettleTime: time.Millisecond}, nil)
	return f
}

func (f *fixture) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(f.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Push the mtime into the past so the settle check passes.
	old := time.Now().Add(-time.Minute)
	_ = os.Chtimes(p, old, old)
}

func (f *fixture) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f *fixture) reopenDB(t *testing.T) {
	t.Helper()
	f.db.Close()
	db, err := index.Open(filepath.Join(f.dir, ".sync", "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.db = db
	f.sc = New(f.root, db, Options{SettleTime: time.Millisecond}, nil)
	t.Cleanup(func() { db.Close() })
}

func TestScanAssignsIDsAndIndexes(t *testing.T) {
	f := setup(t)
	f.write(t, "home/hello.md", "# Hello world\n\nA note with #tag and [[link]].\n")
	f.write(t, "home/sub/deeper.md", "---\ncustom: yes\n---\nno title here\n")
	f.write(t, "home/_assets/pic.png", "PNG")
	f.write(t, "home/.hidden.md", "# hidden\n")
	f.write(t, ".trash/home/old.md", "# trashed\n")
	f.write(t, "home/notes.txt", "not a note")
	f.write(t, "loose.md", "# Loose\n")
	f.write(t, "work/dash.html", "<html><head><title>Dashboard</title></head><body><h1>Hi</h1><script>x()</script><p>numbers</p></body></html>")

	res, err := f.sc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Notes != 4 || res.Assets != 1 || res.Assigned != 4 || len(res.Deferred) != 0 {
		t.Fatalf("result: %+v", res)
	}
	hello := f.read(t, "home/hello.md")
	if !strings.HasPrefix(hello, "---\nid: ") || !strings.Contains(hello, "\ncreated: ") || !strings.HasSuffix(hello, "---\n# Hello world\n\nA note with #tag and [[link]].\n") {
		t.Fatalf("frontmatter not written cleanly:\n%s", hello)
	}
	deeper := f.read(t, "home/sub/deeper.md")
	if !strings.Contains(deeper, "custom: yes\n---\nno title here\n") {
		t.Fatalf("existing keys disturbed:\n%s", deeper)
	}
	ctx := context.Background()
	n, err := f.db.GetNoteByPath(ctx, "home/hello.md")
	if err != nil {
		t.Fatal(err)
	}
	if n.Title != "Hello world" || n.Space != "home" || n.Kind != "md" || len(n.ID) != 26 {
		t.Fatalf("note: %+v", n)
	}
	tags, _ := f.db.Tags(ctx, n.ID)
	if len(tags) != 1 || tags[0] != "tag" {
		t.Fatalf("tags: %v", tags)
	}
	d, _ := f.db.GetNoteByPath(ctx, "home/sub/deeper.md")
	if d.Title != "deeper" {
		t.Fatalf("filename fallback title: %q", d.Title)
	}
	l, _ := f.db.GetNoteByPath(ctx, "loose.md")
	if l.Space != "" {
		t.Fatalf("loose file space: %q", l.Space)
	}
	h, _ := f.db.GetNoteByPath(ctx, "work/dash.html")
	if h.Kind != "html" || h.Title != "Dashboard" {
		t.Fatalf("html note: %+v", h)
	}
	hits, _ := f.db.Search(ctx, "numbers", "", 10)
	if len(hits) != 1 || hits[0].Note.ID != h.ID {
		t.Fatalf("html body not indexed: %+v", hits)
	}
	if _, err := f.db.GetNoteByPath(ctx, "home/.hidden.md"); err == nil {
		t.Fatal("dotfile indexed")
	}
	if _, err := f.db.GetNoteByPath(ctx, ".trash/home/old.md"); err == nil {
		t.Fatal("trash indexed")
	}
}

// TestDatabaseIsDisposable is the Phase 1 acceptance test: delete the
// index, rescan, and get the same tree, ids and search results.
func TestDatabaseIsDisposable(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		f.write(t, fmt.Sprintf("s%d/n%d.md", i%3, i), fmt.Sprintf("# Note %d\n\nbody %d #t%d\n", i, i, i%5))
	}
	f.write(t, "s0/_assets/a.bin", "x")
	if _, err := f.sc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := f.db.ListNotes(ctx, "")
	hitsBefore, _ := f.db.Search(ctx, "body 7", "", 10)
	spacesBefore, _ := f.db.Spaces(ctx)

	f.db.Close()
	for _, name := range []string{"index.db", "index.db-wal", "index.db-shm"} {
		_ = os.Remove(filepath.Join(f.dir, ".sync", name))
	}
	f.reopenDB(t)
	res, err := f.sc.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Assigned != 0 {
		t.Fatalf("rescan assigned %d new ids; ids must come from the files", res.Assigned)
	}
	after, _ := f.db.ListNotes(ctx, "")
	if len(after) != len(before) {
		t.Fatalf("%d notes before, %d after", len(before), len(after))
	}
	for i := range before {
		b, a := before[i], after[i]
		if b.ID != a.ID || b.RelPath != a.RelPath || b.Title != a.Title || b.ContentHash != a.ContentHash || !b.Created.Equal(a.Created) {
			t.Fatalf("note differs after rebuild:\n%+v\n%+v", b, a)
		}
	}
	hitsAfter, _ := f.db.Search(ctx, "body 7", "", 10)
	if len(hitsAfter) != len(hitsBefore) || hitsAfter[0].Note.ID != hitsBefore[0].Note.ID {
		t.Fatalf("search differs after rebuild: %+v vs %+v", hitsBefore, hitsAfter)
	}
	spacesAfter, _ := f.db.Spaces(ctx)
	if fmt.Sprint(spacesAfter) != fmt.Sprint(spacesBefore) {
		t.Fatalf("spaces differ: %v vs %v", spacesBefore, spacesAfter)
	}
}

func TestRescanIsIdempotentAndRetires(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.write(t, "a/one.md", "# One\n")
	f.write(t, "a/two.md", "# Two\n")
	if _, err := f.sc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	one := f.read(t, "a/one.md")
	res, err := f.sc.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Assigned != 0 || f.read(t, "a/one.md") != one {
		t.Fatal("second scan must not touch files")
	}
	// Move a file by hand: id follows it, path updates, no new row.
	os.MkdirAll(filepath.Join(f.dir, "a", "moved"), 0o755)
	if err := os.Rename(filepath.Join(f.dir, "a", "two.md"), filepath.Join(f.dir, "a", "moved", "two.md")); err != nil {
		t.Fatal(err)
	}
	twoID := frontmatter.Parse([]byte(f.read(t, "a/moved/two.md"))).Meta.ID
	os.Remove(filepath.Join(f.dir, "a", "one.md"))
	res, err = f.sc.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Retired != 1 {
		t.Fatalf("retired %d, want 1", res.Retired)
	}
	notes, _ := f.db.ListNotes(ctx, "")
	if len(notes) != 1 || notes[0].ID != twoID || notes[0].RelPath != "a/moved/two.md" {
		t.Fatalf("after move: %+v", notes)
	}
}

func TestDuplicateIDFromCopy(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	f.write(t, "a/orig.md", "# Orig\n")
	if _, err := f.sc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	orig := f.read(t, "a/orig.md")
	f.write(t, "a/copy.md", orig) // cp orig.md copy.md
	if _, err := f.sc.Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if f.read(t, "a/orig.md") != orig {
		t.Fatal("original must keep its id")
	}
	copyID := frontmatter.Parse([]byte(f.read(t, "a/copy.md"))).Meta.ID
	origID := frontmatter.Parse([]byte(orig)).Meta.ID
	if copyID == "" || copyID == origID {
		t.Fatalf("copy should get a fresh id: %q vs %q", copyID, origID)
	}
	notes, _ := f.db.ListNotes(ctx, "")
	if len(notes) != 2 {
		t.Fatalf("both files indexed: %+v", notes)
	}
}

func TestYoungFileIsDeferred(t *testing.T) {
	f := setup(t)
	f.sc = New(f.root, f.db, Options{SettleTime: time.Hour}, nil)
	p := filepath.Join(f.dir, "a", "fresh.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("# Fresh\n"), 0o644) // mtime = now
	res, err := f.sc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deferred) != 1 || res.Deferred[0] != "a/fresh.md" || res.Notes != 0 {
		t.Fatalf("expected deferral: %+v", res)
	}
	if f.read(t, "a/fresh.md") != "# Fresh\n" {
		t.Fatal("young file must not be modified")
	}
	if err := f.sc.ScanOne(context.Background(), "a/fresh.md"); !IsDeferred(err) {
		t.Fatalf("ScanOne should defer: %v", err)
	}
}

func TestLimits(t *testing.T) {
	f := setup(t)
	f.sc = New(f.root, f.db, Options{SettleTime: time.Millisecond, MaxNoteSize: 10, MaxNotesPerSpace: 1}, nil)
	f.write(t, "a/big.md", strings.Repeat("x", 100))
	f.write(t, "a/one.md", "# 1\n")
	f.write(t, "a/two.md", "# 2\n")
	res, err := f.sc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Notes != 1 || res.Skipped != 2 {
		t.Fatalf("limits not enforced: %+v", res)
	}
}

func TestScanPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	f := setup(t)
	const n = 5000
	for i := 0; i < n; i++ {
		f.write(t, fmt.Sprintf("perf/d%d/note-%d.md", i%50, i), fmt.Sprintf("# Note %d\n\nSome body text for note %d with #tag%d and a [[link %d]].\n", i, i, i%10, i+1))
	}
	start := time.Now()
	res, err := f.sc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first := time.Since(start)
	start = time.Now()
	if _, err := f.sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := time.Since(start)
	t.Logf("%d notes: first scan (assigning ids) %s, second scan %s", res.Notes, first, second)
	if res.Notes != n {
		t.Fatalf("indexed %d", res.Notes)
	}
	if second > 10*time.Second {
		t.Fatalf("scan too slow: %s", second)
	}
}
