package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/leotsgo/yana/internal/index"
	"github.com/leotsgo/yana/internal/pathsafe"
	"github.com/leotsgo/yana/internal/reconcile"
	"github.com/leotsgo/yana/internal/scanner"
)

// aliasEnv is an env whose tree carries aliases, embeds, and a live
// reconciliation loop for the rename test.
func newAliasEnv(t *testing.T, withSync bool) *linksEnv {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
		old := time.Now().Add(-time.Minute)
		os.Chtimes(p, old, old)
	}
	write("main/home.md", "# Home\n\nAsk [[Mom]] about dinner. Menu: ![[meals]].\n")
	write("main/people/margaret.md", "---\naliases: [Mom, Margaret]\n---\n# Margaret\n\nCook of the house.\n")
	write("main/meals.md", "# Meals\n\nSpaghetti. The board: ![[home]].\n")
	write("main/loop.md", "# Loop\n\n![[loop]]\n")

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
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>YANA/</title>")}}
	deps := Deps{DB: db, Root: root, Web: web, Version: "test", Scanner: sc}
	var rec *reconcile.Reconciler
	if withSync {
		rec = reconcile.New(root, db, sc, reconcile.Options{
			IdleTime: 60 * time.Millisecond, Debounce: 20 * time.Millisecond,
			SettleTime: 150 * time.Millisecond, UnloadAfter: -1,
		}, nil)
		if err := rec.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { rec.Close() })
		deps.Sync = rec
	}
	srv := New(deps)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &linksEnv{env: &env{dir: dir, srv: srv, ts: ts, db: db}, rec: rec, sc: sc}
}

func noteByPath(t *testing.T, e *linksEnv, rel string) index.Note {
	t.Helper()
	n, err := e.db.GetNoteByPath(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func linkOf(t *testing.T, e *linksEnv, noteID, raw string) index.OutboundLink {
	t.Helper()
	links, err := e.db.OutboundLinks(context.Background(), noteID)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range links {
		if l.RawTarget == raw && l.Kind != "embed" {
			return l
		}
	}
	return index.OutboundLink{RawTarget: raw}
}

// [[Mom]] resolves through the alias, and the read view inlines embeds
// once, without recursion.
func TestAliasResolutionAndEmbeds(t *testing.T) {
	e := newAliasEnv(t, false)
	home := noteByPath(t, e, "main/home.md")

	if l := linkOf(t, e, home.ID, "Mom"); !l.Resolved || l.ToID == "" {
		t.Fatalf("[[Mom]] did not resolve through the alias: %+v", l)
	}

	var note NoteResponse
	if code := e.get(t, "/api/notes/"+home.ID, &note); code != 200 {
		t.Fatalf("note %d", code)
	}
	// The embed inlines the target's body under a header.
	if !strings.Contains(note.HTML, `<section class="embed">`) ||
		!strings.Contains(note.HTML, "Spaghetti") ||
		!strings.Contains(note.HTML, `class="embed-head"`) {
		t.Fatalf("embed not inlined:\n%s", note.HTML)
	}
	// The embedded body's own embed is a link, not another embed.
	if strings.Count(note.HTML, `<section class="embed">`) != 1 {
		t.Fatalf("embed recursed:\n%s", note.HTML)
	}
	if !strings.Contains(note.HTML, `<span class="wikilink" data-target="home">`) {
		t.Fatalf("nested embed is not a link:\n%s", note.HTML)
	}

	// A note embedding itself renders a link, not an infinite embed: the
	// meals page inlines home once, and the ![[meals]] inside home's
	// embedded body is a link back.
	var meals NoteResponse
	if code := e.get(t, "/api/notes/"+noteByPath(t, e, "main/meals.md").ID, &meals); code != 200 {
		t.Fatalf("meals %d", code)
	}
	if strings.Count(meals.HTML, `<section class="embed">`) != 1 {
		t.Fatalf("embed depth wrong:\n%s", meals.HTML)
	}
	if !strings.Contains(meals.HTML, `<span class="wikilink" data-target="meals">`) {
		t.Fatalf("circular embed is not a link:\n%s", meals.HTML)
	}

	// A note embedding itself by name renders a link too.
	var loop NoteResponse
	if code := e.get(t, "/api/notes/"+noteByPath(t, e, "main/loop.md").ID, &loop); code != 200 {
		t.Fatalf("loop %d", code)
	}
	if strings.Contains(loop.HTML, `<section class="embed">`) || !strings.Contains(loop.HTML, `<span class="wikilink" data-target="loop">`) {
		t.Fatalf("self-embed is not a link:\n%s", loop.HTML)
	}

	// The tree carries aliases for completion.
	var tree struct {
		Spaces []struct {
			Children []treeNodeJSON `json:"children"`
		} `json:"spaces"`
	}
	if code := e.get(t, "/api/tree", &tree); code != 200 {
		t.Fatalf("tree %d", code)
	}
	found := false
	var walk func(nodes []treeNodeJSON)
	walk = func(nodes []treeNodeJSON) {
		for _, n := range nodes {
			if n.Title == "Margaret" && len(n.Aliases) == 2 {
				found = true
			}
			walk(n.Children)
		}
	}
	for _, sp := range tree.Spaces {
		walk(sp.Children)
	}
	if !found {
		t.Fatalf("tree does not carry Margaret's aliases: %+v", tree)
	}
}

type treeNodeJSON struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	Aliases  []string       `json:"aliases"`
	Children []treeNodeJSON `json:"children"`
}

// Two notes claiming one alias: the link is unresolved and one report
// line names both.
func TestAliasConflictReport(t *testing.T) {
	e := newAliasEnv(t, false)
	p := filepath.Join(e.dir, "main", "people", "maggie.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("---\naliases: [Mom]\n---\n# Maggie\n"), 0o644)
	old := time.Now().Add(-time.Minute)
	os.Chtimes(p, old, old)
	if err := e.sc.ScanOne(context.Background(), "main/people/maggie.md"); err != nil {
		t.Fatal(err)
	}

	home := noteByPath(t, e, "main/home.md")
	if l := linkOf(t, e, home.ID, "Mom"); l.Resolved {
		t.Fatalf("conflicted alias still resolves: %+v", l)
	}

	var res struct {
		Unresolved     []index.UnresolvedLink `json:"unresolved"`
		AliasConflicts []index.AliasConflict  `json:"alias_conflicts"`
	}
	if code := e.get(t, "/api/links/unresolved", &res); code != 200 {
		t.Fatalf("unresolved %d", code)
	}
	seen := false
	for _, u := range res.Unresolved {
		if u.RawTarget == "Mom" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("[[Mom]] missing from the unresolved report: %+v", res.Unresolved)
	}
	if len(res.AliasConflicts) != 1 || res.AliasConflicts[0].Alias != "Mom" || len(res.AliasConflicts[0].Notes) != 2 {
		t.Fatalf("alias conflicts = %+v", res.AliasConflicts)
	}
	paths := map[string]bool{}
	for _, n := range res.AliasConflicts[0].Notes {
		paths[n.RelPath] = true
	}
	if !paths["main/people/margaret.md"] || !paths["main/people/maggie.md"] {
		t.Fatalf("conflict does not name both: %+v", res.AliasConflicts[0].Notes)
	}
}

// Renaming a note keeps its aliases; a link written through the alias
// is left as written and still resolves, and nothing becomes
// unresolved.
func TestRenameKeepsAliasLinks(t *testing.T) {
	e := newAliasEnv(t, true)
	margaret := noteByPath(t, e, "main/people/margaret.md")
	home := noteByPath(t, e, "main/home.md")

	var res struct {
		Note index.Note `json:"note"`
	}
	if code := e.post(t, "/api/notes/"+margaret.ID+"/move", map[string]string{"path": "main/people/Margaret Pendleton.md"}, &res); code != 200 {
		t.Fatalf("move = %d", code)
	}
	if res.Note.RelPath != "main/people/Margaret Pendleton.md" {
		t.Fatalf("move result = %+v", res.Note)
	}

	if l := linkOf(t, e, home.ID, "Mom"); !l.Resolved || l.ToID != margaret.ID {
		t.Fatalf("[[Mom]] no longer resolves after the rename: %+v", l)
	}

	// The link was left as written.
	deadline := time.Now().Add(5 * time.Second)
	body := ""
	for time.Now().Before(deadline) {
		body = fileBodyOrNull(t, e.dir, "main/home.md")
		if strings.Contains(body, "[[Mom]]") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(body, "[[Mom]]") {
		t.Fatalf("alias link was rewritten or lost: %q", body)
	}

	var unresolved struct {
		Unresolved []index.UnresolvedLink `json:"unresolved"`
	}
	if code := e.get(t, "/api/links/unresolved?space=main", &unresolved); code != 200 || len(unresolved.Unresolved) != 0 {
		t.Fatalf("space has unresolved links after the rename: %d %+v", code, unresolved.Unresolved)
	}

	back, err := e.db.Backlinks(context.Background(), margaret.ID)
	if err != nil || len(back) != 1 || back[0].Note.ID != home.ID || !strings.Contains(back[0].Context, "[[Mom]]") {
		t.Fatalf("backlinks after the rename = %+v, %v", back, err)
	}
}

// The live preview resolves embeds against the note being edited.
func TestRenderEmbedsInPreview(t *testing.T) {
	e := newAliasEnv(t, false)
	home := noteByPath(t, e, "main/home.md")
	var res struct {
		HTML string `json:"html"`
	}
	if code := e.post(t, "/api/render", map[string]string{"markdown": "Dinner: ![[meals]]", "note": home.ID}, &res); code != 200 {
		t.Fatalf("render %d", code)
	}
	if !strings.Contains(res.HTML, `<section class="embed">`) || !strings.Contains(res.HTML, "Spaghetti") {
		t.Fatalf("preview did not inline the embed: %s", res.HTML)
	}
}
