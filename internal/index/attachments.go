package index

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Attachment is one file under _assets/ whose text the index holds. PDFs
// carry extracted text and a page count; every file carries its name, so
// a scanned manual with no text layer is still findable.
type Attachment struct {
	RelPath     string     `json:"path"`
	Space       string     `json:"space"`
	Name        string     `json:"name"`
	Size        int64      `json:"size"`
	MTime       time.Time  `json:"mtime"`
	ContentHash string     `json:"content_hash"`
	Pages       *int       `json:"pages,omitempty"`
	ExtractedAt *time.Time `json:"extracted_at,omitempty"`
}

// ErrAttachmentNotFound is returned when an attachment path has no row.
var ErrAttachmentNotFound = errors.New("attachment not found")

const attachmentColumns = "rel_path, space, name, size, mtime, content_hash, pages, extracted_at"

func scanAttachment(row interface{ Scan(...any) error }) (Attachment, error) {
	var a Attachment
	var mtime, extractedAt sql.NullInt64
	var pages sql.NullInt64
	if err := row.Scan(&a.RelPath, &a.Space, &a.Name, &a.Size, &mtime, &a.ContentHash, &pages, &extractedAt); err != nil {
		return a, err
	}
	a.MTime = time.Unix(0, mtime.Int64).UTC()
	if pages.Valid {
		p := int(pages.Int64)
		a.Pages = &p
	}
	if extractedAt.Valid {
		t := time.Unix(0, extractedAt.Int64).UTC()
		a.ExtractedAt = &t
	}
	return a, nil
}

// UpsertAttachment writes an attachment row and the text search reads.
// indexed says the body row reflects this content hash — the extracted
// text, or "" when there is none to extract (a non-PDF, a file over the
// extraction limit, a scan with no text layer), which still leaves the
// name searchable. When indexed is false the file is unchanged and the
// previous text and page count stay as they are.
func UpsertAttachment(tx *sql.Tx, a Attachment, text string, indexed bool) error {
	var pages any
	if a.Pages != nil {
		pages = *a.Pages
	}
	var extractedAt any
	if indexed && a.ExtractedAt != nil {
		extractedAt = a.ExtractedAt.UnixNano()
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO attachments (`+attachmentColumns+`, extracted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.RelPath, a.Space, a.Name, a.Size, a.MTime.UnixNano(), a.ContentHash, pages, extractedAt, boolAny(indexed)); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE attachments SET
			space = ?, name = ?, size = ?, mtime = ?, content_hash = ?,
			pages = COALESCE(?, pages), extracted_at = COALESCE(?, extracted_at), extracted = ?
			WHERE rel_path = ?`,
		a.Space, a.Name, a.Size, a.MTime.UnixNano(), a.ContentHash, pages, extractedAt, boolAny(indexed), a.RelPath); err != nil {
		return err
	}
	if !indexed {
		return nil
	}
	var rowid int64
	if err := tx.QueryRow(`SELECT rowid FROM attachments WHERE rel_path = ?`, a.RelPath).Scan(&rowid); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO attachment_bodies (att_rowid, name, body) VALUES (?, ?, ?)
		ON CONFLICT(att_rowid) DO UPDATE SET name = excluded.name, body = excluded.body`,
		rowid, a.Name, text)
	return err
}

// DeleteAttachmentByPath removes one attachment and its text.
func DeleteAttachmentByPath(tx *sql.Tx, relPath string) error {
	_, err := tx.Exec(`DELETE FROM attachments WHERE rel_path = ?`, relPath)
	return err
}

// DeleteAttachmentsExcept retires attachment rows for files that no
// longer exist. The triggers carry the bodies away.
func DeleteAttachmentsExcept(tx *sql.Tx, keep map[string]struct{}) error {
	rows, err := tx.Query(`SELECT rel_path FROM attachments`)
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
		if err := DeleteAttachmentByPath(tx, p); err != nil {
			return err
		}
	}
	return nil
}

// AttachmentState is what a scan needs to know about an already-indexed
// attachment: its hash, whether the body row reflects that hash, and the
// page count extraction found.
type AttachmentState struct {
	ContentHash string
	Pages       *int
	Indexed     bool
}

// AttachmentStates returns the state of every indexed attachment, so a
// scan can skip re-extracting files that did not change.
func (db *DB) AttachmentStates(ctx context.Context) (map[string]AttachmentState, error) {
	rows, err := db.readers.QueryContext(ctx, `SELECT rel_path, content_hash, pages, extracted FROM attachments`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]AttachmentState{}
	for rows.Next() {
		var p, hash string
		var pages sql.NullInt64
		var extracted int
		if err := rows.Scan(&p, &hash, &pages, &extracted); err != nil {
			return nil, err
		}
		st := AttachmentState{ContentHash: hash, Indexed: extracted != 0}
		if pages.Valid {
			v := int(pages.Int64)
			st.Pages = &v
		}
		out[p] = st
	}
	return out, rows.Err()
}

// GetAttachment returns one attachment by path.
func (db *DB) GetAttachment(ctx context.Context, relPath string) (Attachment, error) {
	a, err := scanAttachment(db.readers.QueryRowContext(ctx,
		`SELECT `+attachmentColumns+` FROM attachments WHERE rel_path = ?`, relPath))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrAttachmentNotFound
	}
	return a, err
}

// AttachmentHit is one full-text result from an attachment.
type AttachmentHit struct {
	Attachment
	Snippet string  `json:"snippet"`
	Rank    float64 `json:"rank"`
}

// SearchAttachments runs a full-text query over attachment text and
// names, mirroring Search: the trigram tokenizer needs three characters,
// and shorter queries fall back to a name substring match. allowed, when
// not nil, restricts results to those spaces.
func (db *DB) SearchAttachments(ctx context.Context, query string, allowed []string, limit int) ([]AttachmentHit, error) {
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
		if len(allowed) == 0 {
			allowed = []string{""}
		}
		ph := make([]string, len(allowed))
		for i, sp := range allowed {
			ph[i] = "?"
			args = append(args, sp)
		}
		return q + " AND a.space IN (" + strings.Join(ph, ",") + ")", args
	}
	var (
		rows *sql.Rows
		err  error
	)
	short := len([]rune(query)) < 3
	if short {
		q := `SELECT ` + prefixed(attachmentColumns, "a.") + `, '', 0 FROM attachments a WHERE a.name LIKE ? ESCAPE '\'`
		args := []any{"%" + escapeLike(query) + "%"}
		q, args = allowClause(q, args)
		q += ` ORDER BY a.name LIMIT ?`
		args = append(args, limit)
		rows, err = db.readers.QueryContext(ctx, q, args...)
	} else {
		q := `SELECT ` + prefixed(attachmentColumns, "a.") + `, snippet(attachments_fts, 1, '<mark>', '</mark>', '…', 24), bm25(attachments_fts, 4.0, 1.0)
			FROM attachments_fts f JOIN attachments a ON a.rowid = f.rowid
			WHERE attachments_fts MATCH ?`
		args := []any{ftsQuery(query)}
		q, args = allowClause(q, args)
		q += ` ORDER BY bm25(attachments_fts, 4.0, 1.0) LIMIT ?`
		args = append(args, limit)
		rows, err = db.readers.QueryContext(ctx, q, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttachmentHit
	for rows.Next() {
		var h AttachmentHit
		var mtime, extractedAt sql.NullInt64
		var pages sql.NullInt64
		dst := []any{&h.RelPath, &h.Space, &h.Name, &h.Size, &mtime, &h.ContentHash, &pages, &extractedAt, &h.Snippet, &h.Rank}
		if err := rows.Scan(dst...); err != nil {
			return nil, err
		}
		h.MTime = time.Unix(0, mtime.Int64).UTC()
		if pages.Valid {
			p := int(pages.Int64)
			h.Pages = &p
		}
		if extractedAt.Valid {
			t := time.Unix(0, extractedAt.Int64).UTC()
			h.ExtractedAt = &t
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// NotesReferencing lists notes whose raw text mentions ref (a tail like
// `_assets/manual.pdf`), newest path first, at most limit. It is how an
// attachment search result points back at the notes that use the file.
func (db *DB) NotesReferencing(ctx context.Context, ref string, allowed []string, limit int) ([]Note, error) {
	if limit <= 0 {
		limit = 5
	}
	q := `SELECT ` + prefixed(noteColumns, "n.") + `
		FROM notes n JOIN note_bodies b ON n.rowid = b.note_rowid
		WHERE b.raw_body LIKE ? ESCAPE '\'`
	args := []any{"%" + escapeLike(ref) + "%"}
	if allowed != nil {
		if len(allowed) == 0 {
			return nil, nil
		}
		ph := make([]string, len(allowed))
		for i, sp := range allowed {
			ph[i] = "?"
			args = append(args, sp)
		}
		q += ` AND n.space IN (` + strings.Join(ph, ",") + `)`
	}
	q += ` ORDER BY n.rel_path LIMIT ?`
	args = append(args, limit)
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

// OrphanAssets lists assets no note in their space references. The
// reference is the tail of the path from its _assets segment, which is
// how a note beside the file writes it.
func (db *DB) OrphanAssets(ctx context.Context) ([]Asset, error) {
	rows, err := db.readers.QueryContext(ctx, `
		SELECT a.space, a.rel_path, a.size FROM assets a
		WHERE NOT EXISTS (
			SELECT 1 FROM notes n JOIN note_bodies b ON n.rowid = b.note_rowid
			WHERE n.space = a.space
			  AND b.raw_body LIKE '%' || replace(replace(replace(
				      substr(a.rel_path, instr(a.rel_path, '_assets/')),
				      '\', '\\'), '%', '\%'), '_', '\_') || '%'
			      ESCAPE '\'
		)
		ORDER BY a.rel_path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		var a Asset
		if err := rows.Scan(&a.Space, &a.RelPath, &a.Size); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func boolAny(b bool) any {
	if b {
		return 1
	}
	return 0
}
