package index

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func openLinksDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func upsert(t *testing.T, db *DB, id, rel, body string) {
	upsertKind(t, db, id, rel, "md", body, body)
}

func upsertKind(t *testing.T, db *DB, id, rel, kind, body, raw string) {
	t.Helper()
	now := time.Now().UTC()
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		return UpsertNote(tx, Note{
			ID: id, Space: spaceOfPath(rel), RelPath: rel, Title: id, Kind: kind,
			ContentHash: id, MTime: now, Created: now, UpdatedAt: now,
		}, body, raw, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLinkRecomputeAndQueries(t *testing.T) {
	db := openLinksDB(t)
	ctx := context.Background()

	upsert(t, db, "home", "main/home.md", "# Home\n\nSee [[target]] and [[guides/nested|the guide]].\nBroken: [[missing]].\n")
	upsert(t, db, "target", "main/target.md", "# Target\n")
	upsert(t, db, "nested", "main/guides/nested.md", "# Nested\nLinks back with [[target.md]] and root style.\n")

	replace := func(id string) {
		t.Helper()
		if err := db.Write(ctx, func(tx *sql.Tx) error { return ReplaceLinksForNote(tx, id) }); err != nil {
			t.Fatal(err)
		}
	}
	replace("home")
	replace("nested")

	// Outbound links of home: sibling, nested, unresolved.
	out, err := db.OutboundLinks(ctx, "home")
	if err != nil {
		t.Fatal(err)
	}
	byRaw := map[string]OutboundLink{}
	for _, l := range out {
		byRaw[l.RawTarget] = l
	}
	if l := byRaw["target"]; !l.Resolved || l.ToID != "target" {
		t.Fatalf("target link = %+v", l)
	}
	if l := byRaw["guides/nested"]; !l.Resolved || l.ToID != "nested" {
		t.Fatalf("nested link = %+v", l)
	}
	if l := byRaw["missing"]; l.Resolved || l.ToID != "" {
		t.Fatalf("missing link = %+v", l)
	}

	// Backlinks of target: both notes, with a context line each.
	back, err := db.Backlinks(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 2 {
		t.Fatalf("backlinks = %+v", back)
	}
	for _, b := range back {
		if b.Context == "" {
			t.Fatalf("backlink from %s has no context", b.Note.ID)
		}
	}

	// Unresolved report: the one broken link.
	un, err := db.UnresolvedLinks(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(un) != 1 || un[0].RawTarget != "missing" || un[0].Note.ID != "home" {
		t.Fatalf("unresolved = %+v", un)
	}

	// Inbound links drive rename propagation.
	in, err := db.InboundLinks(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 2 {
		t.Fatalf("inbound = %+v", in)
	}

	// Deleting the target flips its inbound links unresolved.
	if err := db.Write(ctx, func(tx *sql.Tx) error { return DeleteNoteByPath(tx, "main/target.md") }); err != nil {
		t.Fatal(err)
	}
	in, err = db.InboundLinks(ctx, "target")
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 0 {
		t.Fatalf("inbound after delete = %+v", in)
	}
	un, err = db.UnresolvedLinks(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(un) != 3 { // target, target.md, and the original missing
		t.Fatalf("unresolved after delete = %+v", un)
	}
}

func TestRecomputeSpaceLinks(t *testing.T) {
	db := openLinksDB(t)
	ctx := context.Background()

	upsert(t, db, "a", "s/a.md", "Link [[twin]].\n")
	upsert(t, db, "t1", "s/one/twin.md", "one")
	upsert(t, db, "t2", "s/two/twin.md", "two")

	if err := db.Write(ctx, func(tx *sql.Tx) error { return RecomputeSpaceLinks(tx, "s") }); err != nil {
		t.Fatal(err)
	}
	// Ambiguous filename: unresolved.
	out, err := db.OutboundLinks(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Resolved {
		t.Fatalf("ambiguous twin resolved: %+v", out)
	}

	// One twin disappears; the filename becomes unique.
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		if err := DeleteNoteByPath(tx, "s/two/twin.md"); err != nil {
			return err
		}
		return RecomputeSpaceLinks(tx, "s")
	}); err != nil {
		t.Fatal(err)
	}
	out, err = db.OutboundLinks(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || !out[0].Resolved || out[0].ToID != "t1" {
		t.Fatalf("twin should resolve uniquely now: %+v", out)
	}
}

func TestContextLine(t *testing.T) {
	body := "Some prose before.\nThis line mentions [[target|the target]] inline.\nLast line.\n"
	got := contextLine(body, "target")
	if got != "This line mentions [[target|the target]] inline." {
		t.Fatalf("contextLine = %q", got)
	}
	// A raw target that prefixes another must not match it.
	if got := contextLine("only [[targets]] here", "target"); got != "" {
		t.Fatalf("prefix match leaked: %q", got)
	}
	if got := contextLine("plain body", "target"); got != "" {
		t.Fatalf("no match expected: %q", got)
	}
}

// HTML notes link through data-wikilink attributes; the index resolves
// them exactly like [[targets]].
func TestHTMLNoteLinks(t *testing.T) {
	db := openLinksDB(t)
	ctx := context.Background()

	upsertKind(t, db, "dash", "main/dash.html", "html",
		"Dash See the metrics note. Missing target.",
		`<h1>Dash</h1><p>See <a data-wikilink="metrics">the metrics note</a>.
<a data-wikilink="metrics">repeated target</a> and
<a data-wikilink="no such note">a missing one</a>.</p>`)
	upsertKind(t, db, "metrics", "main/metrics.md", "md", "# Metrics\n", "# Metrics\n")

	if err := db.Write(ctx, func(tx *sql.Tx) error { return ReplaceLinksForNote(tx, "dash") }); err != nil {
		t.Fatal(err)
	}
	out, err := db.OutboundLinks(ctx, "dash")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("outbound = %+v", out)
	}
	byRaw := map[string]OutboundLink{}
	for _, l := range out {
		byRaw[l.RawTarget] = l
	}
	if l := byRaw["metrics"]; !l.Resolved || l.ToID != "metrics" {
		t.Fatalf("metrics link = %+v", l)
	}
	if l := byRaw["no such note"]; l.Resolved {
		t.Fatalf("missing link = %+v", l)
	}
	back, err := db.Backlinks(ctx, "metrics")
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 1 || back[0].Note.ID != "dash" {
		t.Fatalf("backlinks = %+v", back)
	}
}
