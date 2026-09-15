// Trash rows: notes whose files are gone, kept so the trash UI can say
// where a note came from and restore can put it back. The content itself
// lives outside the index (the .trash copy and the retired sidecar), so
// deleting the index loses the listing for externally deleted notes but
// never the notes.
package index

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DeletedNote is one row of deleted_notes.
type DeletedNote struct {
	ID        string    `json:"id"`
	Space     string    `json:"space"`
	RelPath   string    `json:"path"`
	Title     string    `json:"title"`
	Kind      string    `json:"kind"`
	Created   time.Time `json:"created"`
	DeletedAt time.Time `json:"deleted_at"`
	// TrashPath is the note's path under .trash ("" when the file was
	// removed externally and only the sidecar remains).
	TrashPath string `json:"trash_path,omitempty"`
}

// ErrDeletedNotFound is returned when a deleted note id has no row.
var ErrDeletedNotFound = errors.New("deleted note not found")

const deletedColumns = "id, space, rel_path, title, kind, created, deleted_at, trash_path"

func scanDeleted(row interface{ Scan(...any) error }) (DeletedNote, error) {
	var d DeletedNote
	var created, deletedAt int64
	if err := row.Scan(&d.ID, &d.Space, &d.RelPath, &d.Title, &d.Kind, &created, &deletedAt, &d.TrashPath); err != nil {
		return d, err
	}
	d.Created = time.Unix(0, created).UTC()
	d.DeletedAt = time.Unix(0, deletedAt).UTC()
	return d, nil
}

// RetireNote moves a note's identity into deleted_notes inside tx. An
// empty trashPath means the file vanished (external delete); a non-empty
// one always wins over the stored value so the in-app delete that knows
// the trash path is never overwritten by the reindex that follows it.
func RetireNote(tx *sql.Tx, n Note, trashPath string, at time.Time) error {
	_, err := tx.Exec(`INSERT INTO deleted_notes (`+deletedColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			space = excluded.space, rel_path = excluded.rel_path, title = excluded.title,
			kind = excluded.kind, created = excluded.created, deleted_at = excluded.deleted_at,
			trash_path = CASE WHEN excluded.trash_path != '' THEN excluded.trash_path ELSE deleted_notes.trash_path END`,
		n.ID, n.Space, n.RelPath, n.Title, n.Kind, n.Created.UnixNano(), at.UnixNano(), trashPath)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM notes WHERE id = ?`, n.ID)
	return err
}

// ClearDeleted drops the trash row of a note that is alive again. It
// runs inside UpsertNote so every revival path (restore, an external
// mv back, a fresh file with the same id) clears the entry.
func ClearDeleted(tx *sql.Tx, id string) error {
	_, err := tx.Exec(`DELETE FROM deleted_notes WHERE id = ?`, id)
	return err
}

// GetDeleted returns one trash row by note id.
func (db *DB) GetDeleted(ctx context.Context, id string) (DeletedNote, error) {
	d, err := scanDeleted(db.readers.QueryRowContext(ctx, `SELECT `+deletedColumns+` FROM deleted_notes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrDeletedNotFound
	}
	return d, err
}

// ListDeleted returns every trash row, newest first.
func (db *DB) ListDeleted(ctx context.Context) ([]DeletedNote, error) {
	rows, err := db.readers.QueryContext(ctx, `SELECT `+deletedColumns+` FROM deleted_notes ORDER BY deleted_at DESC, rel_path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeletedNote
	for rows.Next() {
		d, err := scanDeleted(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDeleted removes one trash row (the artifacts around it are the
// caller's job).
func DeleteDeleted(tx *sql.Tx, id string) error {
	_, err := tx.Exec(`DELETE FROM deleted_notes WHERE id = ?`, id)
	return err
}
