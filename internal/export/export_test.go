package export

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/index"
	"github.com/madeofpendletonwool/yana/internal/pathsafe"
	"github.com/madeofpendletonwool/yana/internal/scanner"
)

// exportEnv is a scanned tree with an export Deps over it.
type exportEnv struct {
	dir  string
	root *pathsafe.Root
	db   *index.DB
	deps *Deps
}

func newExportEnv(t *testing.T, files map[string]string) *exportEnv {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Minute)
		os.Chtimes(p, old, old)
	}
	root, err := pathsafe.NewRoot(dir, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	db, err := index.Open(filepath.Join(dir, ".sync", "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sc := scanner.New(root, db, scanner.Options{SettleTime: time.Millisecond}, nil)
	if _, err := sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	deps := &Deps{DB: db, Root: root, SearchJS: []byte("// search runtime stub\n"), Now: func() time.Time { return fixed }}
	return &exportEnv{dir: dir, root: root, db: db, deps: deps}
}

// unzip extracts a zip into dir and returns the file set.
func unzip(t *testing.T, data []byte, dir string) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, filepath.FromSlash(f.Name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(names)
	return names
}

func readSite(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// checkHrefs walks every page of an extracted site and asserts each
// internal href and src resolves to a real file — the offline acceptance
// check for navigation, links, and images.
func checkHrefs(t *testing.T, dir string) {
	t.Helper()
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		page, _ := filepath.Rel(dir, p)
		html := readSite(t, dir, page)
		for _, m := range refRe.FindAllStringSubmatch(html, -1) {
			ref := m[2]
			if ref == "" || isExternalRef(ref) {
				continue
			}
			ref = stripRef(ref)
			target := filepath.Join(filepath.Dir(p), filepath.FromSlash(ref))
			if _, err := os.Stat(target); err != nil {
				t.Errorf("%s references %s which does not exist", filepath.ToSlash(page), ref)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

var refRe = regexp.MustCompile(`(?s)\b(href|src)="([^"]*)"`)

// --- single file --------------------------------------------------------

func TestSingleNoteInlinesImagesAndCSS(t *testing.T) {
	e := newExportEnv(t, map[string]string{
		"home/hello.md":        "---\nid: 01JQ8X4K2M9P7R3T5V6W8Y0Z1A\n---\n# Hello\n\nSee ![pic](_assets/pic.png).\n",
		"home/_assets/pic.png": "\x89PNG\r\n\x1a\n" + strings.Repeat("image-bytes-", 8),
	})
	out, err := e.deps.SingleNote(context.Background(), "01JQ8X4K2M9P7R3T5V6W8Y0Z1A")
	if err != nil {
		t.Fatal(err)
	}
	page := string(out)
	if !strings.Contains(page, "data:image/png;base64,") {
		t.Error("image is not inlined as a png data URI")
	}
	if strings.Contains(page, `src="_assets/pic.png"`) {
		t.Error("image src was left external")
	}
	if !strings.Contains(page, "<style>") {
		t.Error("stylesheet is not inlined")
	}
	if !strings.Contains(page, "<title>Hello — YANA/</title>") {
		t.Error("title is wrong")
	}
	if strings.Contains(page, "id: 01JQ") {
		t.Error("frontmatter leaked into the export")
	}
}

func TestSingleNoteSanitizesUntrustedHTML(t *testing.T) {
	e := newExportEnv(t, map[string]string{
		"work/dash.html": "---\nid: 01JQ8X4K2M9P7R3T5V6W8Y0Z1B\n---\n<h1>Dash</h1><script>fetch('/api/notes')</script><p>stats</p>",
	})
	out, err := e.deps.SingleNote(context.Background(), "01JQ8X4K2M9P7R3T5V6W8Y0Z1B")
	if err != nil {
		t.Fatal(err)
	}
	page := string(out)
	if strings.Contains(page, "<script>") {
		t.Error("an untrusted HTML note kept its script")
	}
	if !strings.Contains(page, "stats") {
		t.Error("the note's text was dropped")
	}
}

// --- static site --------------------------------------------------------

func siteFixture() map[string]string {
	return map[string]string{
		"home/hello.md":        "---\nid: 01JQ8X4K2M9P7R3T5V6W8Y0Z1A\ncreated: 2026-09-01T00:00:00Z\n---\n# Hello\n\nLink to [[second]] and [[Nowhere|a missing note]].\n\n![pic](_assets/pic.png)\n",
		"home/sub/second.md":   "---\nid: 01JQ8X4K2M9P7R3T5V6W8Y0Z1B\ncreated: 2026-09-01T00:00:00Z\n---\n# Second\n\nBack to [[hello]] and to sibling [[sub/second]].\n",
		"home/_assets/pic.png": "\x89PNG\r\n\x1a\npic",
		"home/sub/notes.txt":   "a stray non-note file",
		"home/dash.html":       "---\nid: 01JQ8X4K2M9P7R3T5V6W8Y0Z1C\n---\n<h1>Dash</h1><p>see <a data-wikilink=\"hello\">hello</a></p>",
		"other/secret.md":      "---\nid: 01JQ8X4K2M9P7R3T5V6W8Y0Z1D\n---\n# Secret\n\nanother space, must not leak\n",
		"home/.space.yml":      "name: home\nmembers: []\n",
	}
}

func TestSiteZip(t *testing.T) {
	e := newExportEnv(t, siteFixture())
	var buf bytes.Buffer
	stats, err := e.deps.SiteZip(context.Background(), "home", "", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Notes != 3 {
		t.Fatalf("notes: %+v", stats)
	}
	dir := t.TempDir()
	names := unzip(t, buf.Bytes(), dir)

	want := map[string]bool{
		"index.html": false, "site.css": false, "search.html": false,
		"search.js": false, "search-index.js": false,
		"hello.html": false, "sub/second.html": false, "dash.html": false,
		"_assets/pic.png": false,
	}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, seen := range want {
		if !seen {
			t.Errorf("%s missing from the site", n)
		}
	}
	for _, n := range names {
		if strings.HasPrefix(n, "other/") || strings.Contains(n, "secret") {
			t.Errorf("another space leaked into the site: %s", n)
		}
		if strings.HasSuffix(n, ".md") {
			t.Errorf("markdown source shipped in the site: %s", n)
		}
	}
	// notes.txt is a non-note file; it travels so relative links hold.
	if !contains(names, "sub/notes.txt") {
		t.Error("non-note file notes.txt was not copied")
	}
	checkHrefs(t, dir)

	hello := readSite(t, dir, "hello.html")
	if !strings.Contains(hello, `href="sub/second.html"`) {
		t.Error("wikilink to second did not become a relative href")
	}
	if !strings.Contains(hello, "wikilink unresolved") {
		t.Error("unresolved wikilink is not marked")
	}
	if !strings.Contains(hello, `src="_assets/pic.png"`) {
		t.Error("relative image src was rewritten unnecessarily")
	}
	if !strings.Contains(hello, `<section class="x-backlinks">`) || !strings.Contains(hello, "Second") {
		t.Error("backlinks section missing from hello")
	}
	if strings.Contains(hello, "frontmatter") || strings.Contains(hello, "created: 2026") {
		t.Error("frontmatter leaked into a page")
	}
	second := readSite(t, dir, "sub/second.html")
	if !strings.Contains(second, `href="../hello.html"`) {
		t.Error("link from a nested page is not relative to it")
	}
	dash := readSite(t, dir, "dash.html")
	if !strings.Contains(dash, `<a href="hello.html"`) {
		t.Error("HTML note wikilink did not become an href")
	}
	// The search index carries every note of the space, hrefs from the
	// site root.
	idx := readSite(t, dir, "search-index.js")
	if !strings.Contains(idx, `"space":"home"`) {
		t.Error("search index has no space")
	}
	if c := strings.Count(idx, `"t":`); c != 3 {
		t.Errorf("search index holds %d notes, want 3", c)
	}
	if strings.Contains(idx, "</script") {
		t.Error("search index can break out of its script tag")
	}
	var decoded struct {
		Notes []searchDoc `json:"notes"`
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(idx, "window.YANA_EXPORT_INDEX="), ";\n")
	raw = strings.ReplaceAll(raw, `<\/`, "</")
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("search index is not JSON: %v", err)
	}
	for _, n := range decoded.Notes {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(n.Href))); err != nil {
			t.Errorf("search href %s does not resolve", n.Href)
		}
	}
}

func TestSiteZipSubtree(t *testing.T) {
	e := newExportEnv(t, siteFixture())
	var buf bytes.Buffer
	stats, err := e.deps.SiteZip(context.Background(), "home", "sub", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Notes != 1 {
		t.Fatalf("subtree exported %d notes, want 1", stats.Notes)
	}
	dir := t.TempDir()
	names := unzip(t, buf.Bytes(), dir)
	if !contains(names, "second.html") || contains(names, "hello.html") {
		t.Errorf("subtree site files: %v", names)
	}
	// A file beside the subtree's notes sits beside its pages.
	if !contains(names, "notes.txt") {
		t.Errorf("subtree site files: %v", names)
	}
	checkHrefs(t, dir)
}

func TestSiteZipPageCollision(t *testing.T) {
	e := newExportEnv(t, map[string]string{
		"home/twin.md":   "---\nid: 01JQ8X4K2M9P7R3T5V6W8Y0Z1A\n---\n# Twin md\n",
		"home/twin.html": "---\nid: 01JQ8X4K2M9P7R3T5V6W8Y0Z1B\n---\n<h1>Twin html</h1>",
	})
	var buf bytes.Buffer
	if _, err := e.deps.SiteZip(context.Background(), "home", "", &buf); err == nil {
		t.Fatal("a.md and a.html must collide, but the export succeeded")
	}
}

func TestSiteZipWithoutSearchRuntime(t *testing.T) {
	e := newExportEnv(t, siteFixture())
	e.deps.SearchJS = nil
	var buf bytes.Buffer
	if _, err := e.deps.SiteZip(context.Background(), "home", "", &buf); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	names := unzip(t, buf.Bytes(), dir)
	for _, n := range names {
		if strings.HasPrefix(n, "search") {
			t.Errorf("search file %s emitted without a runtime", n)
		}
	}
	hello := readSite(t, dir, "hello.html")
	if strings.Contains(hello, "search.html") {
		t.Error("nav links a search page that does not exist")
	}
	checkHrefs(t, dir)
}

// TestSite500Notes is the acceptance check: a 500-note space exports to
// a site whose navigation, internal links, and images all resolve, fully
// offline.
func TestSite500Notes(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 500; i++ {
		dir := "big"
		switch {
		case i%7 == 0:
			dir = "big/nested/deep"
		case i%3 == 0:
			dir = "big/nested"
		}
		files[filepath.ToSlash(filepath.Join(dir, "note-"+itoa(i)+".md"))] =
			"---\nid: " + idN(i) + "\n---\n# Note " + itoa(i) + "\n\nSee [[note-" + itoa((i+1)%500) + "]] and [[note-" + itoa((i+7)%500) + "]].\n\nneedle-" + itoa(i) + " body text.\n"
	}
	files["big/_assets/dot.png"] = "\x89PNG\r\n\x1a\ndot"
	e := newExportEnv(t, files)
	var buf bytes.Buffer
	stats, err := e.deps.SiteZip(context.Background(), "big", "", &buf)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Notes != 500 {
		t.Fatalf("exported %d notes, want 500", stats.Notes)
	}
	dir := t.TempDir()
	names := unzip(t, buf.Bytes(), dir)
	pages := 0
	for _, n := range names {
		if strings.HasSuffix(n, ".html") && n != "index.html" && n != "search.html" {
			pages++
		}
	}
	if pages != 500 {
		t.Fatalf("%d note pages, want 500", pages)
	}
	checkHrefs(t, dir)
	idx := readSite(t, dir, "search-index.js")
	if c := strings.Count(idx, `"t":`); c != 500 {
		t.Errorf("search index holds %d notes, want 500", c)
	}
}

// --- tree zip -----------------------------------------------------------

func TestTreeZipRoundTrip(t *testing.T) {
	e := newExportEnv(t, siteFixture())
	var buf bytes.Buffer
	stats, err := e.deps.TreeZip(context.Background(), "home", "", &buf)
	if err != nil {
		t.Fatal(err)
	}
	// Notes, assets, and the space file travel; nothing else is in this
	// fixture beyond those.
	if stats.Files != 6 {
		t.Fatalf("files: %+v", stats)
	}

	// Every entry is byte-identical to the file on disk.
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(f.Name, "home/") {
			t.Errorf("entry %s is not inside the space", f.Name)
		}
		want, err := os.ReadFile(filepath.Join(e.dir, filepath.FromSlash(f.Name)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("entry %s is not byte-identical to disk", f.Name)
		}
	}

	// Round trip: unzip into an empty root, scan, and ids, paths, and
	// links are preserved exactly.
	fresh := t.TempDir()
	unzip(t, buf.Bytes(), fresh)
	root2, err := pathsafe.NewRoot(fresh, pathsafe.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	db2, err := index.Open(filepath.Join(fresh, ".sync", "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	sc2 := scanner.New(root2, db2, scanner.Options{SettleTime: time.Millisecond}, nil)
	if _, err := sc2.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	notes2, err := db2.ListNotes(context.Background(), "home")
	if err != nil {
		t.Fatal(err)
	}
	notes1, err := e.db.ListNotes(context.Background(), "home")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes1) != len(notes2) || len(notes2) != 3 {
		t.Fatalf("note counts: %d vs %d", len(notes1), len(notes2))
	}
	for i := range notes1 {
		a, b := notes1[i], notes2[i]
		if a.ID != b.ID || a.RelPath != b.RelPath || a.ContentHash != b.ContentHash || a.Kind != b.Kind {
			t.Errorf("round trip changed %s: %+v vs %+v", a.RelPath, a, b)
		}
		// Link rows resolve identically after the round trip.
		la, err := e.db.OutboundLinks(context.Background(), a.ID)
		if err != nil {
			t.Fatal(err)
		}
		lb, err := db2.OutboundLinks(context.Background(), b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(la) != len(lb) {
			t.Errorf("%s: %d links before, %d after", a.RelPath, len(la), len(lb))
			continue
		}
		for j := range la {
			if la[j].RawTarget != lb[j].RawTarget || la[j].Resolved != lb[j].Resolved || la[j].ToID != lb[j].ToID {
				t.Errorf("%s: link %+v became %+v", a.RelPath, la[j], lb[j])
			}
		}
	}
}

func TestTreeZipEmptySpace(t *testing.T) {
	e := newExportEnv(t, siteFixture())
	var buf bytes.Buffer
	if _, err := e.deps.TreeZip(context.Background(), "home", "missing-dir", &buf); err != ErrNoNotes {
		t.Fatalf("want ErrNoNotes, got %v", err)
	}
	if _, err := e.deps.SiteZip(context.Background(), "home", "missing-dir", &buf); err != ErrNoNotes {
		t.Fatalf("site: want ErrNoNotes, got %v", err)
	}
}

// --- helpers ------------------------------------------------------------

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// idN builds a distinct valid 26-char id for test notes: a fixed prefix
// with the number replacing the trailing zeros.
func idN(i int) string {
	s := "01JQ" + strings.Repeat("0", 22)
	n := strings.ToUpper(itoa(i))
	return s[:26-len(n)] + n
}
