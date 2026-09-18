package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/madeofpendletonwool/yana/internal/guide"
)

// The owner's first sign-in on an empty tree writes the starter note
// and what it references; the wikilink between the two notes resolves
// and the one to a missing note is reported.
func TestSetupSeedsGuideIntoEmptyTree(t *testing.T) {
	f := newAuthFixture(t)
	code, body := doPost(t, f.ts, "POST", "/api/auth/setup", "", map[string]string{"username": "owner", "password": "owner-password"})
	if code != http.StatusCreated {
		t.Fatalf("setup: %d %v", code, body)
	}
	for _, g := range guide.Files() {
		if _, err := os.Stat(filepath.Join(f.dir, filepath.FromSlash(g.Rel))); err != nil {
			t.Fatalf("%s not written: %v", g.Rel, err)
		}
	}
	start, err := f.db.GetNoteByPath(context.Background(), guide.NoteName)
	if err != nil {
		t.Fatalf("starter note not indexed: %v", err)
	}
	linked, err := f.db.GetNoteByPath(context.Background(), guide.LinkedName)
	if err != nil {
		t.Fatalf("linked note not indexed: %v", err)
	}
	back, err := f.db.Backlinks(context.Background(), linked.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 1 || back[0].Note.ID != start.ID {
		t.Fatalf("starter note should be the linked note's one backlink: %+v", back)
	}
	back, err = f.db.Backlinks(context.Background(), start.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != 1 || back[0].Note.ID != linked.ID {
		t.Fatalf("[[Start here]] should resolve back to the starter note: %+v", back)
	}
	// The root _assets directory is not a space, on disk or after a scan.
	if _, err := f.sc.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := f.db.ListSpaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Space == "_assets" {
			t.Fatal("_assets listed as a space")
		}
	}
	// No top-level directory was made: at the root, one would be a space.
	entries, _ := os.ReadDir(f.dir)
	for _, e := range entries {
		if e.IsDir() && e.Name() != "_assets" && e.Name()[0] != '.' {
			t.Fatalf("seeding made a top-level directory %q", e.Name())
		}
	}
	un, err := f.db.UnresolvedLinks(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(un) != 1 || un[0].RawTarget != "A note that does not exist yet" {
		t.Fatalf("exactly one unresolved link expected: %+v", un)
	}
}

// A tree that already holds a note is never seeded.
func TestSetupLeavesExistingTreeAlone(t *testing.T) {
	f := newAuthFixture(t)
	f.writeNote(t, "home/hello.md", "# Hello\n")
	if code, body := doPost(t, f.ts, "POST", "/api/auth/setup", "", map[string]string{"username": "owner", "password": "owner-password"}); code != http.StatusCreated {
		t.Fatalf("setup: %d %v", code, body)
	}
	if _, err := os.Stat(filepath.Join(f.dir, guide.NoteName)); err == nil {
		t.Fatal("guide written into a tree that had notes")
	}
}

// Help re-creates the guide inside a space; it is idempotent, and a
// viewer cannot write it.
func TestGuideEndpoint(t *testing.T) {
	f := newAuthFixture(t)
	w := f.buildWorld(t)
	code, body := doPost(t, f.ts, "POST", "/api/guide", w.samHdr, map[string]string{"space": "home"})
	if code != http.StatusCreated || body["created"] != true || body["path"] != "home/"+guide.NoteName {
		t.Fatalf("create: %d %v", code, body)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "home", "_assets", "yana.png")); err != nil {
		t.Fatalf("asset not written: %v", err)
	}
	code, body = doPost(t, f.ts, "POST", "/api/guide", w.samHdr, map[string]string{"space": "home"})
	if code != http.StatusOK || body["created"] != false {
		t.Fatalf("second call should find it: %d %v", code, body)
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/guide", w.eveHdr, map[string]string{"space": "home"}); code == http.StatusCreated || code == http.StatusOK {
		t.Fatalf("viewer wrote the guide: %d", code)
	}
	if code, _ := doPost(t, f.ts, "POST", "/api/guide", w.samHdr, map[string]string{"space": "nope/../home"}); code != http.StatusBadRequest {
		t.Fatalf("bad space accepted: %d", code)
	}
}
