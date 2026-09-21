package index

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// Task is one row of the tasks table. The note it belongs to rides
// along, the way backlinks carry theirs.
type Task struct {
	Note    Note       `json:"note"`
	Line    int        `json:"line"`
	Indent  int        `json:"indent"`
	Text    string     `json:"text"`
	Done    bool       `json:"done"`
	DoneAt  *time.Time `json:"done_at,omitempty"`
	Heading string     `json:"heading"`
}

// ReplaceTasksForNote swaps the task rows of one note inside tx. A task
// whose line was already done keeps its done_at; a box newly seen as
// done is stamped now. Callers hand in tasks in file order.
func ReplaceTasksForNote(tx *sql.Tx, noteID string, tasks []Task, now time.Time) error {
	prev := map[int]int64{}
	rows, err := tx.Query(`SELECT line, done_at FROM tasks WHERE note_id = ? AND done = 1 AND done_at IS NOT NULL`, noteID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var line int
		var at int64
		if err := rows.Scan(&line, &at); err != nil {
			rows.Close()
			return err
		}
		prev[line] = at
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM tasks WHERE note_id = ?`, noteID); err != nil {
		return err
	}
	for _, t := range tasks {
		var doneAt any
		done := 0
		if t.Done {
			done = 1
			if at, ok := prev[t.Line]; ok {
				doneAt = at
			} else {
				doneAt = now.UnixNano()
			}
		}
		if _, err := tx.Exec(`INSERT INTO tasks (note_id, line, indent, text, done, done_at, heading) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			noteID, t.Line, t.Indent, t.Text, done, doneAt, t.Heading); err != nil {
			return err
		}
	}
	return nil
}

// TaskFilter narrows a task listing.
type TaskFilter struct {
	// Space restricts to one space (already authorization-checked).
	Space string
	// Allowed restricts to these spaces; nil is unrestricted, an empty
	// list matches nothing.
	Allowed []string
	// Done selects completed tasks instead of open ones.
	Done bool
	// DoneSince bounds how far back a completed task may lie.
	DoneSince time.Time
	// Tag keeps tasks in notes carrying one tag.
	Tag string
	// Path keeps tasks in notes under one folder path (a dir inside a
	// space, no leading slash).
	Path string
}

// Tasks lists tasks for the spaces the caller may see, newest note
// first, then file order within a note.
func (db *DB) Tasks(ctx context.Context, f TaskFilter) ([]Task, error) {
	q := `SELECT ` + prefixed(noteColumns, "n.") + `, t.line, t.indent, t.text, t.done, t.done_at, t.heading
		FROM tasks t JOIN notes n ON n.id = t.note_id`
	var args []any
	if f.Tag != "" {
		q += ` JOIN tags g ON g.note_id = n.id AND g.tag = ?`
		args = append(args, f.Tag)
	}
	q += ` WHERE t.done = ?`
	done := 0
	if f.Done {
		done = 1
	}
	args = append(args, done)
	if f.Done && !f.DoneSince.IsZero() {
		q += ` AND t.done_at >= ?`
		args = append(args, f.DoneSince.UnixNano())
	}
	switch {
	case f.Space != "":
		q += ` AND n.space = ?`
		args = append(args, f.Space)
	case f.Allowed != nil:
		if len(f.Allowed) == 0 {
			return []Task{}, nil
		}
		ph := make([]string, len(f.Allowed))
		for i, sp := range f.Allowed {
			ph[i] = "?"
			args = append(args, sp)
		}
		q += ` AND n.space IN (` + strings.Join(ph, ",") + `)`
	}
	if f.Path != "" {
		q += ` AND (n.rel_path = ? OR n.rel_path LIKE ? ESCAPE '\')`
		args = append(args, f.Path, escapeLike(f.Path)+"/%")
	}
	q += ` ORDER BY n.updated_at DESC, n.rel_path, t.line`
	rows, err := db.readers.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var t Task
		var done int
		var doneAt sql.NullInt64
		if err := scanNoteInto(rows, &t.Note, &t.Line, &t.Indent, &t.Text, &done, &doneAt, &t.Heading); err != nil {
			return nil, err
		}
		t.Done = done != 0
		if doneAt.Valid {
			at := time.Unix(0, doneAt.Int64).UTC()
			t.DoneAt = &at
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// OpenTaskCount counts open tasks in the spaces the caller may see.
func (db *DB) OpenTaskCount(ctx context.Context, allowed []string) (int, error) {
	q := `SELECT COUNT(*) FROM tasks t JOIN notes n ON n.id = t.note_id WHERE t.done = 0`
	var args []any
	if allowed != nil {
		if len(allowed) == 0 {
			return 0, nil
		}
		ph := make([]string, len(allowed))
		for i, sp := range allowed {
			ph[i] = "?"
			args = append(args, sp)
		}
		q += ` AND n.space IN (` + strings.Join(ph, ",") + `)`
	}
	var n int
	if err := db.readers.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
