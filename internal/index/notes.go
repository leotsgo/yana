package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Note is one row of the notes table.
type Note struct {
	ID          string    `json:"id"`
	Space       string    `json:"space"`
	RelPath     string    `json:"path"`
	Title       string    `json:"title"`
	Preview     string    `json:"preview"`
	Kind        string    `json:"kind"`
	ContentHash string    `json:"content_hash"`
	Size        int64     `json:"size"`
	MTime       time.Time `json:"mtime"`
	Created     time.Time `json:"created"`
	UpdatedAt   time.Time `json:"updated_at"`
	Order       *int      `json:"order,omitempty"`
	Trusted     bool      `json:"trusted"`
}

// Asset is one file under an _assets directory.
type Asset struct {
	Space   string `json:"space"`
	RelPath string `json:"path"`
	NoteID  string `json:"note_id,omitempty"`
	Size    int64  `json:"size"`
}

// ErrNotFound is returned when a note id has no row.
var ErrNotFound = errors.New("note not found")

const noteColumns = "id, space, rel_path, title, preview, kind, content_hash, size, mtime, created, updated_at, sort_order, trusted"

func scanNote(row interface{ Scan(...any) error }) (Note, error) {
	var n Note
	if err := scanNoteInto(row, &n); err != nil {
		return n, err
	}
	return n, nil
}

// UpsertNote writes a note row, its body (for search) and its tags inside
// tx. A note is identified by id; a changed rel_path is a move. raw is the
// untransformed body (the markdown itself, or the HTML source) links are
// extracted from; body is what search reads.
func UpsertNote(tx *sql.Tx, n Note, body, raw string, tags []string) error {
	var order any
	if n.Order != nil {
		order = *n.Order
	}
	trusted := 0
	if n.Trusted {
		trusted = 1
	}
	// A different note already at this path (e.g. a file replaced by one
	// with a fresh id) must go first or the UNIQUE(rel_path) fails.
	if _, err := tx.Exec(`DELETE FROM notes WHERE rel_path = ? AND id <> ?`, n.RelPath, n.ID); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO notes (`+noteColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			space = excluded.space, rel_path = excluded.rel_path, title = excluded.title,
			preview = excluded.preview, kind = excluded.kind, content_hash = excluded.content_hash,
			size = excluded.size, mtime = excluded.mtime, created = excluded.created,
			updated_at = excluded.updated_at, sort_order = excluded.sort_order, trusted = excluded.trusted`,
		n.ID, n.Space, n.RelPath, n.Title, n.Preview, n.Kind, n.ContentHash, n.Size,
		n.MTime.UnixNano(), n.Created.UnixNano(), n.UpdatedAt.UnixNano(), order, trusted)
	if err != nil {
		return fmt.Errorf("upsert note %s: %w", n.RelPath, err)
	}
	// A note row for this id means it is alive again; its trash entry,
	// if any, is stale.
	if err := ClearDeleted(tx, n.ID); err != nil {
		return err
	}
	var rowid int64
	if err := tx.QueryRow(`SELECT rowid FROM notes WHERE id = ?`, n.ID).Scan(&rowid); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO note_bodies (note_rowid, title, body, raw_body) VALUES (?, ?, ?, ?)
		ON CONFLICT(note_rowid) DO UPDATE SET title = excluded.title, body = excluded.body, raw_body = excluded.raw_body`,
		rowid, n.Title, body, raw); err != nil {
		return fmt.Errorf("upsert body %s: %w", n.RelPath, err)
	}
	if _, err := tx.Exec(`DELETE FROM tags WHERE note_id = ?`, n.ID); err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tags (note_id, tag) VALUES (?, ?)`, n.ID, tag); err != nil {
			return err
		}
	}
	return nil
}

// DeleteNotesExcept removes every note whose rel_path is not in keep. It is
// how a full scan retires files that vanished while the server was down.
// Each removed row moves to deleted_notes, so the trash can still say
// where the note lived.
func DeleteNotesExcept(tx *sql.Tx, keep map[string]struct{}, now time.Time) (int64, error) {
	rows, err := tx.Query(`SELECT id, rel_path FROM notes`)
	if err != nil {
		return 0, err
	}
	var gone []string
	for rows.Next() {
		var id, p string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return 0, err
		}
		if _, ok := keep[p]; !ok {
			gone = append(gone, id)
		}
	}
	rows.Close()
	for _, id := range gone {
		n, err := GetNoteTx(tx, id)
		if err != nil {
			return 0, err
		}
		if err := RetireNote(tx, n, "", now); err != nil {
			return 0, err
		}
	}
	return int64(len(gone)), nil
}

// DeleteNoteByPath removes one note row and its dependents, keeping a
// trash record of where it lived. now stamps the deletion.
func DeleteNoteByPath(tx *sql.Tx, relPath string, now time.Time) error {
	var id string
	err := tx.QueryRow(`SELECT id FROM notes WHERE rel_path = ?`, relPath).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	n, err := GetNoteTx(tx, id)
	if err != nil {
		return err
	}
	return RetireNote(tx, n, "", now)
}

// UpsertAsset records a file under _assets.
func UpsertAsset(tx *sql.Tx, a Asset) error {
	_, err := tx.Exec(`INSERT INTO assets (space, rel_path, note_id, size) VALUES (?, ?, ?, ?)
		ON CONFLICT(rel_path) DO UPDATE SET space = excluded.space, note_id = excluded.note_id, size = excluded.size`,
		a.Space, a.RelPath, nullIfEmpty(a.NoteID), a.Size)
	return err
}

// DeleteAssetsExcept retires asset rows for files that no longer exist.
func DeleteAssetsExcept(tx *sql.Tx, keep map[string]struct{}) error {
	rows, err := tx.Query(`SELECT rel_path FROM assets`)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		if _, ok := keep[p]; !ok {
			gone = append(gone, p)
		}
	}
	rows.Close()
	for _, p := range gone {
		if _, err := tx.Exec(`DELETE FROM assets WHERE rel_path = ?`, p); err != nil {
			return err
		}
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// SetScanState records a bookkeeping value.
func SetScanState(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(`INSERT INTO scan_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// GetNote returns one note by id.
func (db *DB) GetNote(ctx context.Context, id string) (Note, error) {
	n, err := scanNote(db.readers.QueryRowContext(ctx, `SELECT `+noteColumns+` FROM notes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	return n, err
}

// GetNoteTx returns one note by id inside a transaction, for callers that
// need the pre-write state of a row they are about to change.
func GetNoteTx(tx *sql.Tx, id string) (Note, error) {
	n, err := scanNote(tx.QueryRow(`SELECT `+noteColumns+` FROM notes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	return n, err
}

// GetNoteByPath returns one note by relative path.
func (db *DB) GetNoteByPath(ctx context.Context, relPath string) (Note, error) {
	n, err := scanNote(db.readers.QueryRowContext(ctx, `SELECT `+noteColumns+` FROM notes WHERE rel_path = ?`, relPath))
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	return n, err
}

// ListNotes returns every note, optionally restricted to a space, ordered
// by path.
func (db *DB) ListNotes(ctx context.Context, space string) ([]Note, error) {
	q := `SELECT ` + noteColumns + ` FROM notes`
	var args []any
	if space != "" {
		q += ` WHERE space = ?`
		args = append(args, space)
	}
	q += ` ORDER BY rel_path`
	rows, err := db.readers.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Tags returns the tags of one note.
func (db *DB) Tags(ctx context.Context, noteID string) ([]string, error) {
	rows, err := db.readers.QueryContext(ctx, `SELECT tag FROM tags WHERE note_id = ? ORDER BY tag`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Counts returns totals for readiness and metrics.
func (db *DB) Counts(ctx context.Context) (notes, assets int, err error) {
	if err = db.readers.QueryRowContext(ctx, `SELECT COUNT(*) FROM notes`).Scan(&notes); err != nil {
		return
	}
	err = db.readers.QueryRowContext(ctx, `SELECT COUNT(*) FROM assets`).Scan(&assets)
	return
}

// ScanState reads a bookkeeping value; missing keys return "".
func (db *DB) ScanState(ctx context.Context, key string) (string, error) {
	var v string
	err := db.readers.QueryRowContext(ctx, `SELECT value FROM scan_state WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SearchHit is one full-text result.
type SearchHit struct {
	Note    Note    `json:"note"`
	Snippet string  `json:"snippet"`
	Rank    float64 `json:"rank"`
}

// Search runs a full-text query. The trigram tokenizer needs at least three
// characters; shorter queries fall back to a title substring match.
// allowed, when not nil, restricts results to those spaces (an empty
// list matches nothing); nil is unrestricted.
func (db *DB) Search(ctx context.Context, query, space string, allowed []string, limit int) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	allowClause := func(q string, args []any) (string, []any) {
		if allowed == nil {
			return q, args
		}
		// IN with placeholders; an empty allowed list matches nothing.
		if len(allowed) == 0 {
			allowed = []string{""}
		}
		ph := make([]string, len(allowed))
		for i, sp := range allowed {
			ph[i] = "?"
			args = append(args, sp)
		}
		return q + " AND n.space IN (" + strings.Join(ph, ",") + ")", args
	}
	var (
		rows *sql.Rows
		err  error
	)
	if len([]rune(query)) < 3 {
		q := `SELECT ` + prefixed(noteColumns, "n.") + `, '' FROM notes n WHERE n.title LIKE ? ESCAPE '\'`
		args := []any{"%" + escapeLike(query) + "%"}
		if space != "" {
			q += ` AND n.space = ?`
			args = append(args, space)
		}
		q, args = allowClause(q, args)
		q += ` ORDER BY n.title LIMIT ?`
		args = append(args, limit)
		rows, err = db.readers.QueryContext(ctx, q, args...)
	} else {
		q := `SELECT ` + prefixed(noteColumns, "n.") + `, snippet(notes_fts, 1, '<mark>', '</mark>', '…', 24), bm25(notes_fts, 4.0, 1.0)
			FROM notes_fts f JOIN notes n ON n.rowid = f.rowid
			WHERE notes_fts MATCH ?`
		args := []any{ftsQuery(query)}
		if space != "" {
			q += ` AND n.space = ?`
			args = append(args, space)
		}
		q, args = allowClause(q, args)
		q += ` ORDER BY bm25(notes_fts, 4.0, 1.0) LIMIT ?`
		args = append(args, limit)
		rows, err = db.readers.QueryContext(ctx, q, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	short := len([]rune(query)) < 3
	for rows.Next() {
		var h SearchHit
		var mtime, created, updated int64
		var order sql.NullInt64
		var trusted int
		dst := []any{&h.Note.ID, &h.Note.Space, &h.Note.RelPath, &h.Note.Title, &h.Note.Preview, &h.Note.Kind,
			&h.Note.ContentHash, &h.Note.Size, &mtime, &created, &updated, &order, &trusted, &h.Snippet}
		if !short {
			dst = append(dst, &h.Rank)
		}
		if err := rows.Scan(dst...); err != nil {
			return nil, err
		}
		h.Note.MTime = time.Unix(0, mtime).UTC()
		h.Note.Created = time.Unix(0, created).UTC()
		h.Note.UpdatedAt = time.Unix(0, updated).UTC()
		if order.Valid {
			o := int(order.Int64)
			h.Note.Order = &o
		}
		h.Note.Trusted = trusted != 0
		if h.Snippet == "" {
			h.Snippet = h.Note.Preview
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ftsQuery turns free text into an FTS5 expression: each whitespace-separated
// term becomes a quoted phrase so user punctuation cannot break the query
// syntax.
func ftsQuery(q string) string {
	var parts []string
	for _, term := range strings.Fields(q) {
		term = strings.ReplaceAll(term, `"`, `""`)
		if len([]rune(term)) < 3 {
			// Trigram cannot match shorter terms; keep the query useful by
			// dropping them rather than failing the whole search.
			continue
		}
		parts = append(parts, `"`+term+`"`)
	}
	if len(parts) == 0 {
		q = strings.ReplaceAll(q, `"`, `""`)
		return `"` + q + `"`
	}
	return strings.Join(parts, " ")
}

func prefixed(cols, prefix string) string {
	parts := strings.Split(cols, ", ")
	for i := range parts {
		parts[i] = prefix + parts[i]
	}
	return strings.Join(parts, ", ")
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// Body returns the indexed body text of one note (what search sees).
func (db *DB) Body(ctx context.Context, id string) (string, error) {
	var body string
	err := db.readers.QueryRowContext(ctx,
		`SELECT b.body FROM note_bodies b JOIN notes n ON n.rowid = b.note_rowid WHERE n.id = ?`, id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return body, err
}

// TagCount is one tag and how many notes carry it.
type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// ListTags returns every tag with its note count, most used first, then
// by name. allowed, when not nil, restricts the count to those spaces.
func (db *DB) ListTags(ctx context.Context, allowed []string) ([]TagCount, error) {
	q := `SELECT t.tag, COUNT(*) FROM tags t JOIN notes n ON n.id = t.note_id`
	var args []any
	if allowed != nil {
		if len(allowed) == 0 {
			return []TagCount{}, nil
		}
		ph := make([]string, len(allowed))
		for i, sp := range allowed {
			ph[i] = "?"
			args = append(args, sp)
		}
		q += ` WHERE n.space IN (` + strings.Join(ph, ",") + `)`
	}
	q += ` GROUP BY t.tag ORDER BY COUNT(*) DESC, t.tag`
	rows, err := db.readers.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagCount{}
	for rows.Next() {
		var t TagCount
		if err := rows.Scan(&t.Tag, &t.Count); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// NotesByTag returns every note carrying one tag, ordered by path.
func (db *DB) NotesByTag(ctx context.Context, tag string) ([]Note, error) {
	rows, err := db.readers.QueryContext(ctx,
		`SELECT `+prefixed(noteColumns, "n.")+` FROM notes n JOIN tags t ON t.note_id = n.id WHERE t.tag = ? ORDER BY n.rel_path`, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AllTags returns the tags of every note, keyed by note id, in one
// query; the tree endpoint stamps them onto its rows.
func (db *DB) AllTags(ctx context.Context) (map[string][]string, error) {
	rows, err := db.readers.QueryContext(ctx, `SELECT note_id, tag FROM tags ORDER BY note_id, tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id, t string
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		out[id] = append(out[id], t)
	}
	return out, rows.Err()
}
