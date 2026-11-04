package index

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestGraph(t *testing.T) {
	db := openLinksDB(t)
	ctx := context.Background()

	upsertTagged := func(id, rel, body string, tags ...string) {
		t.Helper()
		now := time.Now().UTC()
		err := db.Write(ctx, func(tx *sql.Tx) error {
			return UpsertNote(tx, Note{
				ID: id, Space: spaceOfPath(rel), RelPath: rel, Title: id, Kind: "md",
				ContentHash: id, MTime: now, Created: now, UpdatedAt: now,
			}, body, body, tags)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	upsertTagged("a", "main/a.md", "[[b]] [[b]] ![[b]] [[a]] [[Missing]] ![[pic.png]]", "x")
	upsertTagged("b", "main/b.md", "[[a]]")
	upsertTagged("c", "main/c.md", "", "x")
	upsertTagged("d", "main/d.md", "[[missing]]")
	upsertTagged("e", "other/e.md", "[[a]]")
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		for _, id := range []string{"a", "b", "c", "d", "e"} {
			if err := ReplaceLinksForNote(tx, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	paths := func(g Graph) []string {
		out := []string{}
		for _, n := range g.Notes {
			out = append(out, n.Path)
		}
		return out
	}

	g, err := db.Graph(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"main/a.md", "main/b.md", "main/c.md", "main/d.md", "other/e.md"}; !reflect.DeepEqual(paths(g), want) {
		t.Fatalf("notes = %v", paths(g))
	}
	if want := [][2]int{{0, 1}}; !reflect.DeepEqual(g.Links, want) {
		t.Fatalf("links = %v", g.Links)
	}
	if want := []GraphGhost{{"main", "Missing", []int{0, 3}}, {"other", "a", []int{4}}}; !reflect.DeepEqual(g.Ghosts, want) {
		t.Fatalf("ghosts = %+v", g.Ghosts)
	}
	if want := []GraphTag{{"main", "x", []int{0, 2}}}; !reflect.DeepEqual(g.Tags, want) {
		t.Fatalf("tags = %+v", g.Tags)
	}

	g, err = db.Graph(ctx, []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Notes) != 4 || len(g.Ghosts) != 1 || g.Ghosts[0].Space != "main" || !reflect.DeepEqual(g.Ghosts[0].From, []int{0, 3}) {
		t.Fatalf("main only = %+v", g)
	}

	g, err = db.Graph(ctx, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if g.Notes == nil || g.Links == nil || g.Ghosts == nil || g.Tags == nil ||
		len(g.Notes)+len(g.Links)+len(g.Ghosts)+len(g.Tags) != 0 {
		t.Fatalf("empty allowed = %+v", g)
	}
}
