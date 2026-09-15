package index

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestRetireNoteKeepsTrashPath(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	now := time.Now()

	n1, b1 := sample("01A", "home/one.md", "One", "body one")
	if err := db.Write(ctx, func(tx *sql.Tx) error { return UpsertNote(tx, n1, b1, b1, nil) }); err != nil {
		t.Fatal(err)
	}

	// An in-app delete records the trash path.
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		return RetireNote(tx, n1, ".trash/home/one.md", now)
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetDeleted(ctx, "01A")
	if err != nil {
		t.Fatal(err)
	}
	if got.TrashPath != ".trash/home/one.md" || got.RelPath != "home/one.md" || got.Title != "One" {
		t.Fatalf("row: %+v", got)
	}
	if _, err := db.GetNote(ctx, "01A"); err != ErrNotFound {
		t.Fatal("note row survived retirement")
	}

	// The reindex that follows runs with an empty trash path; the known
	// one must survive it.
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		n2, _ := sample("01A", "home/one.md", "One", "body one")
		return RetireNote(tx, n2, "", now.Add(time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetDeleted(ctx, "01A")
	if err != nil {
		t.Fatal(err)
	}
	if got.TrashPath != ".trash/home/one.md" {
		t.Fatalf("trash path clobbered: %+v", got)
	}

	// Reviving the note clears the row.
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		n3, b3 := sample("01A", "home/one.md", "One", "body one")
		if err := UpsertNote(tx, n3, b3, b3, nil); err != nil {
			return err
		}
		return ClearDeleted(tx, "01A")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetDeleted(ctx, "01A"); err != ErrDeletedNotFound {
		t.Fatalf("row survived revival: %v", err)
	}
}

func TestUpsertNoteClearsDeletedRow(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	n, b := sample("01B", "home/two.md", "Two", "body")
	if err := db.Write(ctx, func(tx *sql.Tx) error { return UpsertNote(tx, n, b, b, nil) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		return RetireNote(tx, n, ".trash/home/two.md", time.Now())
	}); err != nil {
		t.Fatal(err)
	}
	// The note returns through the scanner: the plain upsert must clear
	// the trash row without a separate call.
	if err := db.Write(ctx, func(tx *sql.Tx) error { return UpsertNote(tx, n, b, b, nil) }); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetDeleted(ctx, "01B"); err != ErrDeletedNotFound {
		t.Fatalf("row survived upsert: %v", err)
	}
}

func TestDeleteNoteByPathRetiresIntoTrash(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	n, b := sample("01C", "home/three.md", "Three", "body")
	if err := db.Write(ctx, func(tx *sql.Tx) error { return UpsertNote(tx, n, b, b, nil) }); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	if err := db.Write(ctx, func(tx *sql.Tx) error { return DeleteNoteByPath(tx, "home/three.md", at) }); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetDeleted(ctx, "01C")
	if err != nil {
		t.Fatal(err)
	}
	if got.TrashPath != "" || got.DeletedAt.UnixNano() != at.UnixNano() {
		t.Fatalf("row: %+v want deleted_at %v", got, at)
	}
	// A path with no row is a no-op, not an error.
	if err := db.Write(ctx, func(tx *sql.Tx) error { return DeleteNoteByPath(tx, "home/none.md", at) }); err != nil {
		t.Fatal(err)
	}
}
