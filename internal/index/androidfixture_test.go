// The Android offline-search fixtures: one deterministic corpus of 200
// notes and, for each test query, the result the real server engine
// returns for it. `go test ./internal/index -run TestAndroidReplica
// -update` regenerates the JSON under android/app/src/androidTest/assets;
// the plain run re-reads the checked-in file and fails if the engine and
// the expectations have drifted apart. The instrumented test on the
// Android side replays the same corpus through the Room replica and
// must produce the same ordered results — that is the offline/online
// parity acceptance for MAD-539.
package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madeofpendletonwool/yana/internal/frontmatter"
	"github.com/madeofpendletonwool/yana/internal/render"
	"github.com/madeofpendletonwool/yana/internal/search"
)

var fixtureUpdate = flag.Bool("update", false, "rewrite the Android search fixtures")

const fixtureCount = 200

// --- the fixture file -----------------------------------------------------

type fixtureSpace struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Notes int    `json:"notes"`
}

type fixtureNote struct {
	ID        string   `json:"id"`
	Space     string   `json:"space"`
	Path      string   `json:"path"`
	Title     string   `json:"title"`
	Preview   string   `json:"preview"`
	Kind      string   `json:"kind"`
	Created   string   `json:"created"`
	UpdatedAt string   `json:"updated_at"`
	Tags      []string `json:"tags"`
	// Source is what the note endpoint hands a client: markdown with its
	// frontmatter, or the HTML page. Each side derives the indexed body
	// from it its own way, which is exactly what the parity test wants.
	Source string `json:"source"`
}

type fixtureQuery struct {
	Q     string `json:"q"`
	Space string `json:"space,omitempty"`
}

type fixtureExpectation struct {
	IDs   []string  `json:"ids"`
	Ranks []float64 `json:"ranks"`
}

type fixtureFile struct {
	Spaces   []fixtureSpace       `json:"spaces"`
	Notes    []fixtureNote        `json:"notes"`
	Queries  []fixtureQuery       `json:"queries"`
	Expected []fixtureExpectation `json:"expected"`
}

// --- the corpus -----------------------------------------------------------

var corpusWords = []string{"lantern", "amber", "velvet", "glass", "terrace", "moss", "cider", "thyme", "ledger", "harbor", "quilt", "ember"}

func corpusID(i int) string {
	// A fixed, ULID-shaped id so the fixture file is byte-stable.
	return fmt.Sprintf("01JFIXTURE0%04d0000000000", i)
}

// corpus builds the note set deterministically: fixed dates, fixed word
// distribution, lengths that vary per note so bm25 ranks never tie.
func corpus() (spaces []fixtureSpace, notes []fixtureNote) {
	created := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := time.Date(2025, 6, 15, 9, 30, 0, 0, time.UTC)
	counts := map[string]int{}
	for i := 0; i < fixtureCount; i++ {
		id := corpusID(i)
		var space, path string
		switch {
		case i%3 == 0:
			space = "garden"
			if i%2 == 0 {
				path = "garden/recipes/r" + itoa(i) + ".md"
			} else {
				path = "garden/journal/2025/j" + itoa(i) + ".md"
			}
		case i%3 == 1:
			space = "pantry"
			path = "pantry/p" + itoa(i) + ".md"
		default:
			space = ""
			path = "loose" + itoa(i) + ".md"
		}
		counts[space]++

		kind := "md"
		if i%10 == 7 {
			kind = "html"
		}
		title := "Note " + itoa3(i) + " " + corpusWords[i%len(corpusWords)]

		// The word salad: overlapping subsets, a phrase for exact match,
		// and per-note filler of varying length.
		body := "# " + title + "\n\n"
		if i%2 == 0 {
			body += "lantern "
		}
		if i%5 == 1 {
			body += "amber "
		}
		if i%10 == 2 {
			body += "velvet lantern glow "
		}
		if i%5 == 4 {
			body += "glass "
		}
		for f := 0; f <= i%9; f++ {
			body += "filler-" + itoa(i%7) + " "
		}
		body += "\n"

		var tags []string
		if i%4 == 0 {
			tags = append(tags, "journal")
		}
		// Archive mostly rides the odd notes; one html note carries it so
		// the combined selector query has something to find.
		if i%10 == 3 || i == 77 {
			tags = append(tags, "archive")
		}

		// Timestamps distinct to the nanosecond: ordering by updated_at
		// must be total, on both engines, for the filter-mode queries.
		c := created.Add(time.Duration(i) * time.Hour)
		u := updated.Add(time.Duration(i)*time.Hour*39 + time.Duration(i)*time.Minute*7 + time.Duration(i)*time.Nanosecond*13)

		var source string
		if kind == "md" {
			source = "---\nid: " + id + "\ncreated: " + c.Format(time.RFC3339Nano) + "\n---\n" + body
		} else {
			source = "<html><head><title>" + title + "</title></head><body><h1>" + title + "</h1><p>" + body + "</p></body></html>"
		}

		preview := render.Preview([]byte(body), 120)
		notes = append(notes, fixtureNote{
			ID: id, Space: space, Path: path, Title: title, Preview: preview,
			Kind: kind, Created: c.Format(time.RFC3339Nano), UpdatedAt: u.Format(time.RFC3339Nano),
			Tags: tags, Source: source,
		})
	}
	for _, s := range []struct{ name, label string }{{"garden", "Garden"}, {"pantry", "Pantry"}} {
		spaces = append(spaces, fixtureSpace{Name: s.name, Label: s.label, Notes: counts[s.name]})
	}
	return spaces, notes
}

func corpusQueries() []fixtureQuery {
	return []fixtureQuery{
		{Q: "lantern"},
		{Q: "amber lantern"},
		{Q: "\"velvet lantern\""},
		{Q: "lantern -glass"},
		{Q: "Lantern"},
		{Q: "ve"},
		{Q: "#journal"},
		{Q: "-tag:archive lantern"},
		{Q: "is:untagged lantern"},
		{Q: "is:html"},
		{Q: "path:garden/recipes"},
		{Q: "space:pantry lantern"},
		{Q: "before:2025-08-01 lantern"},
		{Q: "after:2025-08-01 lantern"},
		{Q: "tag:journal after:2025-08-01"},
		{Q: "is:html tag:archive"},
		{Q: "lantern", Space: "pantry"},
	}
}

func itoa(i int) string  { return fmt.Sprintf("%03d", i) }
func itoa3(i int) string { return itoa(i) }
func fixtureDir() string {
	return filepath.Join("..", "..", "android", "app", "src", "androidTest", "assets", "searchfixtures")
}
func fixturePath() string { return filepath.Join(fixtureDir(), "fixture.json") }

// indexedBody derives what search reads from the fixture's source, the
// scanner's rule: frontmatter-stripped markdown, or tag-stripped HTML.
func indexedBody(n fixtureNote) (body, raw string) {
	if n.Kind == "md" {
		b := string(frontmatter.Parse([]byte(n.Source)).Body)
		return b, b
	}
	return render.StripHTML([]byte(n.Source)), n.Source
}

func indexFixture(t *testing.T, fx *fixtureFile) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "index.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Write(context.Background(), func(tx *sql.Tx) error {
		for _, n := range fx.Notes {
			body, raw := indexedBody(n)
			created, _ := time.Parse(time.RFC3339Nano, n.Created)
			updated, _ := time.Parse(time.RFC3339Nano, n.UpdatedAt)
			if err := UpsertNote(tx, Note{
				ID: n.ID, Space: n.Space, RelPath: n.Path, Title: n.Title, Preview: n.Preview,
				Kind: n.Kind, ContentHash: "fix", Size: int64(len(n.Source)),
				MTime: updated, Created: created, UpdatedAt: updated,
			}, body, raw, n.Tags); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestAndroidReplicaFixtures(t *testing.T) {
	spaces, notes := corpus()
	if len(notes) != fixtureCount {
		t.Fatalf("corpus has %d notes, want %d", len(notes), fixtureCount)
	}
	queries := corpusQueries()

	if *fixtureUpdate {
		fx := fixtureFile{Spaces: spaces, Notes: notes, Queries: queries}
		db := indexFixture(t, &fx)
		for _, q := range queries {
			hits, err := db.SearchQuery(context.Background(), search.Parse(q.Q), q.Space, nil, 50)
			if err != nil {
				t.Fatalf("query %q: %v", q.Q, err)
			}
			e := fixtureExpectation{}
			for _, h := range hits {
				e.IDs = append(e.IDs, h.Note.ID)
				e.Ranks = append(e.Ranks, h.Rank)
			}
			fx.Expected = append(fx.Expected, e)
		}
		if err := os.MkdirAll(fixtureDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.MarshalIndent(fx, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath(), append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", fixturePath())
		return
	}

	data, err := os.ReadFile(fixturePath())
	if err != nil {
		t.Fatalf("fixtures missing; run `go test ./internal/index -run TestAndroidReplicaFixtures -update`: %v", err)
	}
	var fx fixtureFile
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(fx.Notes) != fixtureCount || len(fx.Queries) != len(queries) || len(fx.Expected) != len(queries) {
		t.Fatalf("fixture shape drifted: %d notes, %d queries, %d expectations", len(fx.Notes), len(fx.Queries), len(fx.Expected))
	}
	db := indexFixture(t, &fx)
	for i, q := range fx.Queries {
		hits, err := db.SearchQuery(context.Background(), search.Parse(q.Q), q.Space, nil, 50)
		if err != nil {
			t.Fatalf("query %q: %v", q.Q, err)
		}
		want := fx.Expected[i]
		if len(hits) != len(want.IDs) {
			t.Fatalf("query %q: %d hits, fixture says %d", q.Q, len(hits), len(want.IDs))
		}
		for j, h := range hits {
			if h.Note.ID != want.IDs[j] {
				t.Fatalf("query %q: hit %d is %s, fixture says %s (order must match)", q.Q, j, h.Note.ID, want.IDs[j])
			}
			if math.Abs(h.Rank-want.Ranks[j]) > 1e-12 {
				t.Fatalf("query %q: hit %s rank %v, fixture says %v", q.Q, h.Note.ID, h.Rank, want.Ranks[j])
			}
		}
	}
}
