package index

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// A database at schema version 13 keeps its rows through migration 014 and
// then accepts the drawing kind but still rejects a bogus one.
func TestUpgrade13to14AcceptsDrawings(t *testing.T) {
	p, raw := openV13(t)
	const cols = `(id,space,rel_path,title,preview,kind,content_hash,size,mtime,created,updated_at)`
	if _, err := raw.Exec(`INSERT INTO notes ` + cols + ` VALUES ('a','s','s/a.md','A','','md','h',1,1,1,1)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	db, err := Open(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO notes ` + cols + ` VALUES ('b','s','s/b.excalidraw.md','B','','drawing','h',1,1,1,1)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO deleted_notes (id,space,rel_path,title,kind,created,deleted_at) VALUES ('c','s','s/c.excalidraw.md','C','drawing',1,1)`)
		return err
	}); err != nil {
		t.Fatal("drawing insert:", err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO notes ` + cols + ` VALUES ('x','s','s/x','X','','bogus','h',1,1,1,1)`); err == nil {
			return fmt.Errorf("bogus kind accepted")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var kind, ic string
	if err := db.Reader().QueryRow(`SELECT kind FROM notes WHERE id = 'a'`).Scan(&kind); err != nil || kind != "md" {
		t.Fatalf("old row: %q, %v", kind, err)
	}
	if err := db.Reader().QueryRow("PRAGMA integrity_check").Scan(&ic); err != nil || ic != "ok" {
		t.Fatalf("integrity_check = %q, %v", ic, err)
	}
}

// A v13 database whose CHECK text 014 does not recognise must not open:
// the edit would match nothing and drawings would fail on every insert.
func TestUpgrade14FailsOnUnexpectedCheck(t *testing.T) {
	p, raw := openV13(t)
	for _, q := range []string{
		"PRAGMA writable_schema = ON",
		`UPDATE sqlite_schema SET sql = replace(sql, 'kind IN (''md'', ''html'')', 'kind IN (''md'',''html'')') WHERE type = 'table' AND name = 'deleted_notes'`,
		"PRAGMA writable_schema = RESET",
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	raw.Close()

	db, err := Open(p, nil)
	if err == nil {
		db.Close()
		t.Fatal("Open succeeded on a schema 014 could not widen")
	}
	if !strings.Contains(err.Error(), "deleted_notes") {
		t.Fatalf("error does not name the table: %v", err)
	}
	// Nothing was applied: a fixed build can run 014 again.
	raw, err = sql.Open("sqlite", "file:"+p)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var v int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != 13 {
		t.Fatalf("user_version = %d, %v", v, err)
	}
}

// openV13 builds a database at schema version 13 from the embedded
// migrations and returns its path and an open handle.
func openV13(t *testing.T) (string, *sql.DB) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "i.db")
	raw, err := sql.Open("sqlite", "file:"+p+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1) // writable_schema is per connection
	for i := 1; i <= 13; i++ {
		b, err := migrations.ReadFile(fmt.Sprintf("migrations/%03d_%s", i, migrationSuffix(t, i)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(string(b)); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := raw.Exec("PRAGMA user_version = 13"); err != nil {
		t.Fatal(err)
	}
	return p, raw
}

func migrationSuffix(t *testing.T, n int) string {
	t.Helper()
	ents, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("%03d_", n)
	for _, e := range ents {
		if len(e.Name()) > 4 && e.Name()[:4] == prefix {
			return e.Name()[4:]
		}
	}
	t.Fatalf("no migration %d", n)
	return ""
}
