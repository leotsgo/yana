package index

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestTasksIndexedAndListed(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	body := "# List\n\n## Section\n\n- [ ] open one #food\n- [x] done one\n  - [ ] nested\n"
	n1, _ := sample("01A", "home/one.md", "One", body)
	n2, _ := sample("01B", "work/two.md", "Two", "- [ ] work task\n")
	n2.Space = "work"
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if err := UpsertNote(tx, n1, body, body, []string{"food"}); err != nil {
			return err
		}
		time.Sleep(time.Millisecond)
		return UpsertNote(tx, n2, "- [ ] work task\n", "- [ ] work task\n", nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	open, err := db.Tasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 3 {
		t.Fatalf("open tasks: %+v", open)
	}
	// Newest note first, then file order within the note.
	if open[0].Note.ID != "01B" || open[0].Text == "" {
		t.Fatalf("order: %+v", open)
	}
	if open[1].Line != 4 || open[1].Heading != "Section" || open[1].Indent != 0 {
		t.Fatalf("task 1: %+v", open[1])
	}
	if open[2].Indent != 1 {
		t.Fatalf("nested task: %+v", open[2])
	}

	done, err := db.Tasks(ctx, TaskFilter{Done: true, DoneSince: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 || done[0].DoneAt == nil {
		t.Fatalf("done tasks: %+v", done)
	}
	doneAt := *done[0].DoneAt

	// Filters: space, tag, path.
	only, err := db.Tasks(ctx, TaskFilter{Space: "home"})
	if err != nil || len(only) != 2 {
		t.Fatalf("space filter: %+v %v", only, err)
	}
	byTag, err := db.Tasks(ctx, TaskFilter{Tag: "food"})
	if err != nil || len(byTag) != 2 {
		t.Fatalf("tag filter: %+v %v", byTag, err)
	}
	byPath, err := db.Tasks(ctx, TaskFilter{Path: "home"})
	if err != nil || len(byPath) != 2 {
		t.Fatalf("path filter: %+v %v", byPath, err)
	}
	if n, err := db.OpenTaskCount(ctx, nil); err != nil || n != 3 {
		t.Fatalf("count: %d %v", n, err)
	}

	// A rescan of the same content keeps the done stamp; a note that
	// drops a task drops its row.
	err = db.Write(ctx, func(tx *sql.Tx) error {
		return UpsertNote(tx, n1, body, body, []string{"food"})
	})
	if err != nil {
		t.Fatal(err)
	}
	done, err = db.Tasks(ctx, TaskFilter{Done: true, DoneSince: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 1 || !done[0].DoneAt.Equal(doneAt) {
		t.Fatalf("done_at not preserved: %+v", done)
	}
	trimmed := "- [ ] open one\n"
	err = db.Write(ctx, func(tx *sql.Tx) error {
		return UpsertNote(tx, n1, trimmed, trimmed, nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	open, err = db.Tasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 {
		t.Fatalf("after trim: %+v", open)
	}
}

func TestTasksAllowedSpaces(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	body := "- [ ] a\n"
	n1, _ := sample("01A", "home/one.md", "One", body)
	n2, _ := sample("02B", "work/two.md", "Two", body)
	n2.Space = "work"
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		if err := UpsertNote(tx, n1, body, body, nil); err != nil {
			return err
		}
		return UpsertNote(tx, n2, body, body, nil)
	}); err != nil {
		t.Fatal(err)
	}
	only, err := db.Tasks(ctx, TaskFilter{Allowed: []string{"work"}})
	if err != nil || len(only) != 1 || only[0].Note.Space != "work" {
		t.Fatalf("allowed: %+v %v", only, err)
	}
	none, err := db.Tasks(ctx, TaskFilter{Allowed: []string{}})
	if err != nil || len(none) != 0 {
		t.Fatalf("empty allowed: %+v %v", none, err)
	}
	if n, err := db.OpenTaskCount(ctx, []string{"work"}); err != nil || n != 1 {
		t.Fatalf("count allowed: %d %v", n, err)
	}
}
