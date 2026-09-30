package index

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/search"
)

// seedOps writes a small tree exercising every operator:
//
//	home/boiler.md      #home #done, a task (open), edited by user:alice, image + pdf refs
//	home/second.md      #home, no tasks, html? no — md, no attachments, edited by agent:claude
//	work/manual.md      untagged, html kind, pdf attachment ref
func seedOps(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	err := db.Write(ctx, func(tx *sql.Tx) error {
		// home/boiler.md — the acceptance-style note.
		n1, _ := sample("A1", "home/boiler.md", "Boiler", "fix the water heater soon #home #done")
		n1.UpdatedAt = now
		if err := UpsertNote(tx, n1, n1.Preview, n1.Preview, []string{"home", "done"}); err != nil {
			return err
		}
		// home/second.md — same tag, newer, by an agent, no attachments.
		n2, _ := sample("A2", "home/second.md", "Second", "nothing about heating here #home")
		n2.UpdatedAt = now.Add(time.Hour)
		if err := UpsertNote(tx, n2, n2.Preview, n2.Preview, []string{"home"}); err != nil {
			return err
		}
		// work/manual.md — an html note with a pdf and an image.
		n3 := Note{
			ID: "A3", Space: "work", RelPath: "work/manual.md.html", Title: "Manual",
			Preview: "the manual", Kind: "html", ContentHash: "h3", Size: 10,
			MTime: now, Created: now, UpdatedAt: now,
		}
		raw := `<p>see ![chart](_assets/chart.png) and <a href="_assets/manual.pdf">manual</a></p>`
		if err := UpsertNote(tx, n3, "the manual", raw, nil); err != nil {
			return err
		}
		// Attribution: last edit per note.
		if err := AppendUpdate(tx, "A1", 1, []byte("u1"), "user:alice", now); err != nil {
			return err
		}
		if err := AppendUpdate(tx, "A2", 1, []byte("u2"), "agent:claude", now); err != nil {
			return err
		}
		if err := AppendUpdate(tx, "A3", 1, []byte("u3"), "filesystem", now); err != nil {
			return err
		}
		// One open task on the boiler note.
		return ReplaceTasksForNote(tx, "A1", []Task{{Line: 0, Text: "fix it", Done: false}}, now)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func ids(hits []SearchHit) string {
	parts := make([]string, 0, len(hits))
	for _, h := range hits {
		parts = append(parts, h.Note.ID)
	}
	return strings.Join(parts, ",")
}

func TestSearchQueryOperators(t *testing.T) {
	db := openTest(t)
	seedOps(t, db)
	ctx := context.Background()

	run := func(t *testing.T, q string, want string) {
		t.Helper()
		hits, err := db.SearchQuery(ctx, search.Parse(q), "", nil, 50)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if got := ids(hits); got != want {
			t.Fatalf("%s: got [%s], want [%s]", q, got, want)
		}
	}

	run(t, `tag:home`, "A2,A1")        // newest edit first
	run(t, `#home`, "A2,A1")           // bare hash is tag:
	run(t, `tag:home -tag:done`, "A2") // negation
	run(t, `#home -#done`, "A2")
	run(t, `path:home/`, "A2,A1")
	run(t, `path:work`, "A3")
	run(t, `path:home/boiler`, "A1") // a note path without its extension
	run(t, `space:work`, "A3")
	run(t, `space:home tag:done`, "A1")
	run(t, `is:untagged`, "A3")
	run(t, `-is:untagged`, "A2,A1")
	run(t, `is:task`, "A1")
	run(t, `is:html`, "A3")
	run(t, `has:image`, "A3")
	run(t, `has:attachment`, "A3")
	run(t, `author:alice`, "A1")
	run(t, `author:user:alice`, "A1")
	run(t, `author:claude`, "A2")
	run(t, `author:filesystem`, "A3")
	run(t, `before:2026-06-01`, "")          // nothing updated before noon June 1
	run(t, `after:2026-06-01`, "A2,A1,A3")   // all three
	run(t, `after:2026-06-01T00:00:00Z`, "") // malformed date is text, not a date

	// The acceptance query: filters around an exact phrase.
	run(t, `tag:home -tag:done "water heater" after:2026-01-01`, "")

	// An unknown operator is searched as text: boiler mentions "heater",
	// second does not; nothing contains the literal "foo:bar".
	run(t, `heater`, "A1")
	run(t, `foo:bar`, "")
}

func TestSearchQueryAcceptancePhrase(t *testing.T) {
	db := openTest(t)
	seedOps(t, db)
	// Redo the boiler so the phrase matches and done does not exclude it.
	ctx := context.Background()
	n1, _ := sample("A1", "home/boiler.md", "Boiler", "fix the water heater soon #home")
	err := db.Write(ctx, func(tx *sql.Tx) error {
		return UpsertNote(tx, n1, n1.Preview, n1.Preview, []string{"home"})
	})
	if err != nil {
		t.Fatal(err)
	}
	hits, err := db.SearchQuery(ctx, search.Parse(`tag:home -tag:done "water heater" after:2026-01-01`), "", nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(hits); got != "A1" {
		t.Fatalf("acceptance query: got [%s], want [A1]", got)
	}
	if !strings.Contains(hits[0].Snippet, "water") {
		t.Fatalf("snippet: %q", hits[0].Snippet)
	}
}

func TestSearchQuerySpaceAndAllowed(t *testing.T) {
	db := openTest(t)
	seedOps(t, db)
	ctx := context.Background()
	hits, err := db.SearchQuery(ctx, search.Parse("tag:home"), "work", nil, 50)
	if err != nil || len(hits) != 0 {
		t.Fatalf("space param: %+v %v", hits, err)
	}
	hits, err = db.SearchQuery(ctx, search.Parse("tag:home"), "", []string{"work"}, 50)
	if err != nil || len(hits) != 0 {
		t.Fatalf("allowed work: %+v %v", hits, err)
	}
	hits, err = db.SearchQuery(ctx, search.Parse("tag:home"), "", []string{"home"}, 50)
	if err != nil || len(hits) != 2 {
		t.Fatalf("allowed home: %+v %v", hits, err)
	}
}

// TestSearchQueryFastOnThreeThousandNotes covers the acceptance bound:
// the operator-and-phrase query answers from a 3,000-note space well
// inside 150 ms.
func TestSearchQueryFastOnThreeThousandNotes(t *testing.T) {
	if testing.Short() {
		t.Skip("seeding 3,000 notes")
	}
	db := openTest(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	err := db.Write(ctx, func(tx *sql.Tx) error {
		for i := 0; i < 3000; i++ {
			var tags []string
			body := fmt.Sprintf("note number %d about rooftops and gutters", i)
			switch {
			case i%10 == 0:
				tags = []string{"home"}
				body = "the water heater needs a flush"
			case i%10 == 2:
				tags = []string{"home", "done"}
				body = "the water heater needs a flush"
			}
			n := Note{
				ID: fmt.Sprintf("N%04d", i), Space: "home", RelPath: fmt.Sprintf("home/folder%d/note%04d.md", i%50, i),
				Title: fmt.Sprintf("Note %d", i), Preview: body, Kind: "md",
				ContentHash: fmt.Sprintf("h%d", i), Size: int64(len(body)),
				MTime: base, Created: base,
				UpdatedAt: base.Add(time.Duration(i) * time.Minute),
			}
			if err := UpsertNote(tx, n, body, body, tags); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	q := search.Parse(`tag:home -tag:done "water heater" after:2026-01-01`)
	start := time.Now()
	hits, err := db.SearchQuery(ctx, q, "", nil, 50)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 50 { // 300 match; the limit is what the request asked for
		t.Fatalf("got %d hits, want 50", len(hits))
	}
	if !strings.Contains(hits[0].Snippet, "water heater") {
		t.Fatalf("top hit snippet: %q", hits[0].Snippet)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("query took %v; the acceptance bound is 150ms", elapsed)
	}
	t.Logf("3,000-note operator query: %v", elapsed)
}
