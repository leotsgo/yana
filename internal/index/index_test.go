package index

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), ".sync", "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func sample(id, path, title, body string) (Note, string) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return Note{
		ID: id, Space: "home", RelPath: path, Title: title, Preview: body, Kind: "md",
		ContentHash: "h-" + id, Size: int64(len(body)), MTime: now, Created: now, UpdatedAt: now,
	}, body
}

func TestUpsertGetSearch(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	n1, b1 := sample("01A", "home/one.md", "Grocery list", "milk eggs #food bread")
	n2, b2 := sample("01B", "home/two.md", "Server notes", "the raspberry pi runs the dns #homelab")
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if err := UpsertNote(tx, n1, b1, b1, []string{"food"}); err != nil {
			return err
		}
		return UpsertNote(tx, n2, b2, b2, []string{"homelab"})
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetNote(ctx, "01A")
	if err != nil || got.Title != "Grocery list" || !got.MTime.Equal(n1.MTime) {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := db.GetNote(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	hits, err := db.Search(ctx, "raspberry", "", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Note.ID != "01B" || hits[0].Snippet == "" {
		t.Fatalf("search: %+v", hits)
	}
	// Trigram search is case-insensitive and substring-capable.
	hits, _ = db.Search(ctx, "RASPBERR", "", nil, 10)
	if len(hits) != 1 {
		t.Fatalf("case-insensitive search: %+v", hits)
	}
	// Space filter.
	hits, _ = db.Search(ctx, "raspberry", "other", nil, 10)
	if len(hits) != 0 {
		t.Fatalf("space filter leaked: %+v", hits)
	}
	// Short query falls back to title match.
	hits, _ = db.Search(ctx, "Gr", "", nil, 10)
	if len(hits) != 1 || hits[0].Note.ID != "01A" {
		t.Fatalf("short query: %+v", hits)
	}
	// Punctuation must not break the FTS expression.
	if _, err := db.Search(ctx, `milk" OR (`, "", nil, 10); err != nil {
		t.Fatalf("punctuation query errored: %v", err)
	}
	tags, _ := db.Tags(ctx, "01B")
	if len(tags) != 1 || tags[0] != "homelab" {
		t.Fatalf("tags: %v", tags)
	}
	spaces, _ := db.ListSpaces(ctx)
	if len(spaces) != 1 || spaces[0].Notes != 2 {
		t.Fatalf("spaces: %+v", spaces)
	}
}

func TestMoveAndRetire(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	n1, b1 := sample("01A", "home/one.md", "One", "one body")
	n2, b2 := sample("01B", "home/two.md", "Two", "two body")
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		if err := UpsertNote(tx, n1, b1, b1, nil); err != nil {
			return err
		}
		return UpsertNote(tx, n2, b2, b2, nil)
	}); err != nil {
		t.Fatal(err)
	}
	// Move by id: same id, new path.
	n1.RelPath = "home/sub/one.md"
	if err := db.Write(ctx, func(tx *sql.Tx) error { return UpsertNote(tx, n1, b1, b1, nil) }); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetNote(ctx, "01A"); got.RelPath != "home/sub/one.md" {
		t.Fatalf("move not applied: %+v", got)
	}
	// A different id landing on an existing path replaces that row.
	n3, b3 := sample("01C", "home/two.md", "Two again", "replacement")
	if err := db.Write(ctx, func(tx *sql.Tx) error { return UpsertNote(tx, n3, b3, b3, nil) }); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetNote(ctx, "01B"); err != ErrNotFound {
		t.Fatal("old note at replaced path should be gone")
	}
	// Retire everything not seen by a scan.
	var gone int64
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		gone, err = DeleteNotesExcept(tx, map[string]struct{}{"home/sub/one.md": {}}, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if gone != 1 {
		t.Fatalf("retired %d, want 1", gone)
	}
	hits, _ := db.Search(ctx, "replacement", "", nil, 10)
	if len(hits) != 0 {
		t.Fatal("FTS row survived note deletion")
	}
	notes, _ := db.ListNotes(ctx, "")
	if len(notes) != 1 {
		t.Fatalf("list: %+v", notes)
	}
}

func TestWriterSerialises(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	done := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(i int) {
			n, b := sample(string(rune('A'+i)), "home/"+string(rune('a'+i))+".md", "T", "body")
			done <- db.Write(ctx, func(tx *sql.Tx) error { return UpsertNote(tx, n, b, b, nil) })
		}(i)
	}
	for i := 0; i < 20; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	notes, _ := db.ListNotes(ctx, "home")
	if len(notes) != 20 {
		t.Fatalf("got %d notes", len(notes))
	}
}
