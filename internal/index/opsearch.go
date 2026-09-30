// Operator search: the SQL half of the search grammar. The parser in
// internal/search decides what each term means; this file maps the terms
// onto the tables (notes, tags, tasks, note_updates, note_bodies) as
// filters around the full-text match.
package index

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/search"
)

// imageExts are the file name endings has:image recognises in a note's
// references, mirroring the asset types the app serves inline.
var imageExts = []string{"png", "jpg", "jpeg", "gif", "webp", "avif", "svg", "bmp", "ico"}

// SearchQuery runs a parsed query. Filters from operator terms narrow
// the full-text match; a query with no usable text runs as filters over
// the notes alone, newest edit first. space and allowed carry the same
// meaning they do in Search.
func (db *DB) SearchQuery(ctx context.Context, q search.Query, space string, allowed []string, limit int) ([]SearchHit, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var filters []string
	var args []any
	for _, t := range q.Terms {
		if t.Op == "" {
			continue
		}
		// Each filter is emitted as a bare SQL boolean; negation wraps
		// it in NOT, so `-tag:x` reads directly as "not carrying x".
		clause, cargs := opFilter(t)
		if clause == "" {
			continue
		}
		if t.Negated {
			clause = "NOT (" + clause + ")"
		}
		filters = append(filters, clause)
		args = append(args, cargs...)
	}
	positive, negative := q.TextTerms()

	mode := "filter"
	var usable []search.Term
	for _, t := range positive {
		if len([]rune(t.Text)) >= 3 {
			usable = append(usable, t)
			mode = "fts"
		}
	}
	if mode != "fts" && len(positive) > 0 {
		mode = "title"
	}

	var (
		query string
		short bool
	)
	switch mode {
	case "fts":
		expr := ftsExpr(usable, negative)
		query = `SELECT ` + prefixed(noteColumns, "n.") + `, snippet(notes_fts, 1, '<mark>', '</mark>', '…', 24), bm25(notes_fts, 4.0, 1.0)
			FROM notes_fts f JOIN notes n ON n.rowid = f.rowid
			WHERE notes_fts MATCH ?`
		args = append([]any{expr}, args...)
	case "title":
		short = true
		query = `SELECT ` + prefixed(noteColumns, "n.") + `, '' FROM notes n WHERE 1=1`
		for _, t := range positive {
			query += ` AND n.title LIKE ? ESCAPE '\'`
			args = append(args, "%"+escapeLike(t.Text)+"%")
		}
		for _, t := range negative {
			// A negated word needs something to subtract from; with no
			// full-text match, the title is what is left to test.
			query += ` AND n.title NOT LIKE ? ESCAPE '\'`
			args = append(args, "%"+escapeLike(t.Text)+"%")
		}
	default:
		short = true
		query = `SELECT ` + prefixed(noteColumns, "n.") + `, '' FROM notes n WHERE 1=1`
	}
	for _, f := range filters {
		query += " AND " + f
	}
	if space != "" {
		query += ` AND n.space = ?`
		args = append(args, space)
	}
	if allowed != nil {
		// IN with placeholders; an empty allowed list matches nothing.
		if len(allowed) == 0 {
			allowed = []string{""}
		}
		ph := make([]string, len(allowed))
		for i, sp := range allowed {
			ph[i] = "?"
			args = append(args, sp)
		}
		query += " AND n.space IN (" + strings.Join(ph, ",") + ")"
	}
	switch mode {
	case "fts":
		query += ` ORDER BY bm25(notes_fts, 4.0, 1.0) LIMIT ?`
	case "title":
		query += ` ORDER BY n.title LIMIT ?`
	default:
		query += ` ORDER BY n.updated_at DESC LIMIT ?`
	}
	args = append(args, limit)

	rows, err := db.readers.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		var mtime, created, updated int64
		var order sql.NullInt64
		var trusted int
		var conflict sql.NullString
		dst := []any{&h.Note.ID, &h.Note.Space, &h.Note.RelPath, &h.Note.Title, &h.Note.Preview, &h.Note.Kind,
			&h.Note.ContentHash, &h.Note.Size, &mtime, &created, &updated, &order, &trusted, &conflict, &h.Snippet}
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
		h.Note.ConflictOf = conflict.String
		if h.Snippet == "" {
			h.Snippet = h.Note.Preview
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// opFilter maps one operator term onto SQL over the notes row n, or ""
// when the term narrows nothing.
func opFilter(t search.Term) (string, []any) {
	switch t.Op {
	case search.OpTag:
		return `EXISTS (SELECT 1 FROM tags t WHERE t.note_id = n.id AND t.tag = ?)`, []any{t.Value}
	case search.OpPath:
		v := strings.Trim(strings.TrimPrefix(t.Value, "/"), "/")
		if v == "" {
			return "", nil
		}
		e := escapeLike(v)
		return `(n.rel_path LIKE ? ESCAPE '\' OR n.rel_path LIKE ? ESCAPE '\')`, []any{e + "/%", e + ".%"}
	case search.OpSpace:
		return `n.space = ?`, []any{t.Value}
	case search.OpIs:
		switch t.Value {
		case "untagged":
			return `NOT EXISTS (SELECT 1 FROM tags t WHERE t.note_id = n.id)`, nil
		case "task":
			return `EXISTS (SELECT 1 FROM tasks k WHERE k.note_id = n.id AND k.done = 0)`, nil
		case "html":
			return `n.kind = 'html'`, nil
		}
	case search.OpHas:
		switch t.Value {
		case "image":
			var parts []string
			var args []any
			for _, ext := range imageExts {
				parts = append(parts, `b.raw_body LIKE ? ESCAPE '\'`)
				args = append(args, `%\_assets/%.`+ext+`%`)
			}
			return `EXISTS (SELECT 1 FROM note_bodies b WHERE b.note_rowid = n.rowid AND (` + strings.Join(parts, " OR ") + `))`, args
		case "attachment":
			return `EXISTS (SELECT 1 FROM note_bodies b WHERE b.note_rowid = n.rowid AND b.raw_body LIKE ? ESCAPE '\')`, []any{`%\_assets/%`}
		}
	case search.OpAuthor:
		v := strings.ToLower(t.Value)
		if name, ok := strings.CutPrefix(v, "user:"); ok {
			v = name
		} else if name, ok := strings.CutPrefix(v, "agent:"); ok {
			v = name
		}
		return `? = COALESCE((SELECT CASE
				WHEN u.author LIKE 'user:%' THEN lower(substr(u.author, 6))
				WHEN u.author LIKE 'agent:%' THEN lower(substr(u.author, 7))
				ELSE lower(u.author) END
				FROM note_updates u WHERE u.note_id = n.id ORDER BY u.seq DESC LIMIT 1), '')`, []any{v}
	case search.OpBefore:
		return `n.updated_at < ?`, []any{t.Date.UnixNano()}
	case search.OpAfter:
		return `n.updated_at >= ?`, []any{t.Date.UnixNano()}
	}
	return "", nil
}

// ftsExpr builds the FTS5 expression for the usable positive terms and
// the negated ones: phrases quoted so user punctuation cannot break the
// syntax, negations appended as NOT.
func ftsExpr(positive, negative []search.Term) string {
	var parts []string
	for _, t := range positive {
		if len([]rune(t.Text)) < 3 {
			continue // the trigram tokenizer cannot match shorter terms
		}
		parts = append(parts, `"`+strings.ReplaceAll(t.Text, `"`, `""`)+`"`)
	}
	for _, t := range negative {
		if len([]rune(t.Text)) < 3 {
			continue
		}
		parts = append(parts, `NOT "`+strings.ReplaceAll(t.Text, `"`, `""`)+`"`)
	}
	// The caller guarantees at least one positive usable term; the
	// fallback keeps an empty expression from ever reaching SQLite.
	if len(parts) == 0 {
		return `""`
	}
	return strings.Join(parts, " ")
}

// FreeText returns the plain positive text of a query joined by spaces,
// for the attachment side of search.
func FreeText(q search.Query) string {
	positive, _ := q.TextTerms()
	parts := make([]string, 0, len(positive))
	for _, t := range positive {
		parts = append(parts, t.Text)
	}
	return strings.Join(parts, " ")
}
