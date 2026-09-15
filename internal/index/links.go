package index

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/render"
	"github.com/madeofpendletonwool/yana/internal/wikilink"
)

// OutboundLink is one wikilink of a note, resolved or not.
type OutboundLink struct {
	RawTarget string `json:"raw_target"`
	ToID      string `json:"to_id,omitempty"`
	Resolved  bool   `json:"resolved"`
}

// Backlink is one note linking to another, with the line the link sits on.
type Backlink struct {
	Note      Note   `json:"note"`
	RawTarget string `json:"raw_target"`
	Context   string `json:"context"`
}

// UnresolvedLink is one broken wikilink in the unresolved report.
type UnresolvedLink struct {
	Note      Note   `json:"note"`
	RawTarget string `json:"raw_target"`
}

// InboundLink is one resolved link pointing at a note, for rename
// propagation.
type InboundLink struct {
	FromID    string
	FromRel   string
	FromSpace string
	RawTarget string
}

// ReplaceLinksForNote recomputes the outbound links of the note fromID from
// its indexed body. The note row must already exist in tx.
func ReplaceLinksForNote(tx *sql.Tx, fromID string) error {
	var rel string
	if err := tx.QueryRow(`SELECT rel_path FROM notes WHERE id = ?`, fromID).Scan(&rel); err != nil {
		return err
	}
	res, err := spaceResolver(tx, spaceOfPath(rel))
	if err != nil {
		return err
	}
	kind, raw, err := rawBodyOfTx(tx, fromID)
	if err != nil {
		return err
	}
	return writeLinks(tx, fromID, kind, res, rel, raw)
}

// RecomputeSpaceLinks recomputes the outbound links of every note in one
// space. A move changes how every raw target in the space resolves, so the
// whole space is redone.
func RecomputeSpaceLinks(tx *sql.Tx, space string) error {
	res, err := spaceResolver(tx, space)
	if err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT n.id, n.rel_path, n.kind, COALESCE(b.raw_body, b.body) FROM notes n
		JOIN note_bodies b ON b.note_rowid = n.rowid WHERE n.space = ?`, space)
	if err != nil {
		return err
	}
	type item struct {
		id, rel, kind, body string
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.rel, &it.kind, &it.body); err != nil {
			rows.Close()
			return err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range items {
		if err := writeLinks(tx, it.id, it.kind, res, it.rel, it.body); err != nil {
			return err
		}
	}
	return nil
}

// RecomputeAllLinks recomputes the links of every space. The full scan
// calls it once the tree is indexed.
func RecomputeAllLinks(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT DISTINCT space FROM notes`)
	if err != nil {
		return err
	}
	var spaces []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		spaces = append(spaces, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, s := range spaces {
		if err := RecomputeSpaceLinks(tx, s); err != nil {
			return err
		}
	}
	return nil
}

// spaceResolver builds the resolver for one space from the notes table as
// tx sees it.
func spaceResolver(tx *sql.Tx, space string) (*wikilink.Resolver, error) {
	rows, err := tx.Query(`SELECT id, rel_path FROM notes WHERE space = ?`, space)
	if err != nil {
		return nil, err
	}
	var refs []wikilink.NoteRef
	for rows.Next() {
		var r wikilink.NoteRef
		if err := rows.Scan(&r.ID, &r.RelPath); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, r)
	}
	rows.Close()
	return wikilink.NewResolver(space, refs), rows.Err()
}

// writeLinks replaces the outbound rows of one note. raws is exactly the
// distinct targets the body spells: [[targets]] for markdown notes,
// data-wikilink attributes for HTML ones.
func writeLinks(tx *sql.Tx, fromID, kind string, res *wikilink.Resolver, fromRel, raw string) error {
	if _, err := tx.Exec(`DELETE FROM links WHERE from_id = ?`, fromID); err != nil {
		return err
	}
	var raws []string
	if kind == "html" {
		raws = render.HTMLWikiLinks([]byte(raw))
	} else {
		raws = render.WikiLinks([]byte(raw))
	}
	for _, raw := range raws {
		r := res.Resolve(raw, fromRel)
		var toID any
		resolved := 0
		if r.OK {
			toID = r.ToID
			resolved = 1
		}
		if _, err := tx.Exec(`INSERT INTO links (from_id, to_id, raw_target, resolved) VALUES (?, ?, ?, ?)`,
			fromID, toID, raw, resolved); err != nil {
			return err
		}
	}
	return nil
}

// rawBodyOfTx returns the note's kind and untransformed body.
func rawBodyOfTx(tx *sql.Tx, noteID string) (kind, raw string, err error) {
	err = tx.QueryRow(`SELECT n.kind, COALESCE(b.raw_body, b.body) FROM note_bodies b
		JOIN notes n ON n.rowid = b.note_rowid WHERE n.id = ?`, noteID).Scan(&kind, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return kind, raw, err
}

func spaceOfPath(rel string) string {
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		return rel[:i]
	}
	return ""
}

// OutboundLinks returns a note's wikilinks for the note payload.
func (db *DB) OutboundLinks(ctx context.Context, noteID string) ([]OutboundLink, error) {
	rows, err := db.readers.QueryContext(ctx,
		`SELECT raw_target, COALESCE(to_id, ''), resolved FROM links WHERE from_id = ? ORDER BY raw_target`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboundLink
	for rows.Next() {
		var l OutboundLink
		var resolved int
		if err := rows.Scan(&l.RawTarget, &l.ToID, &resolved); err != nil {
			return nil, err
		}
		l.Resolved = resolved != 0
		if !l.Resolved {
			l.ToID = ""
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Backlinks returns the notes linking to noteID, each with the line its
// link sits on.
func (db *DB) Backlinks(ctx context.Context, noteID string) ([]Backlink, error) {
	rows, err := db.readers.QueryContext(ctx, `SELECT `+prefixed(noteColumns, "n.")+`, l.raw_target
		FROM links l JOIN notes n ON n.id = l.from_id
		WHERE l.to_id = ? AND l.resolved = 1 ORDER BY n.rel_path`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Backlink
	for rows.Next() {
		var b Backlink
		if err := scanNoteInto(rows, &b.Note, &b.RawTarget); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		body, err := db.Body(ctx, out[i].Note.ID)
		if err == nil {
			out[i].Context = contextLine(body, out[i].RawTarget)
		}
	}
	return out, nil
}

// scanNoteInto scans a note row plus optional trailing columns.
func scanNoteInto(row interface{ Scan(...any) error }, n *Note, extra ...any) error {
	var mtime, created, updated int64
	var order sql.NullInt64
	var trusted int
	dst := []any{&n.ID, &n.Space, &n.RelPath, &n.Title, &n.Preview, &n.Kind, &n.ContentHash,
		&n.Size, &mtime, &created, &updated, &order, &trusted}
	dst = append(dst, extra...)
	if err := row.Scan(dst...); err != nil {
		return err
	}
	n.MTime = time.Unix(0, mtime).UTC()
	n.Created = time.Unix(0, created).UTC()
	n.UpdatedAt = time.Unix(0, updated).UTC()
	if order.Valid {
		o := int(order.Int64)
		n.Order = &o
	}
	n.Trusted = trusted != 0
	return nil
}

// contextLine returns the first body line containing a link to raw,
// whitespace-collapsed and capped. The raw target is matched as a whole
// link body so a target that prefixes another does not match.
func contextLine(body, raw string) string {
	for _, line := range strings.Split(body, "\n") {
		if idx := linkIndex(line, raw); idx >= 0 {
			line = strings.Join(strings.Fields(line), " ")
			r := []rune(line)
			if len(r) > 200 {
				return string(r[:200]) + "…"
			}
			return line
		}
	}
	return ""
}

// linkIndex finds "[[raw" followed by the end of the link (]] or the
// display separator |) anywhere in s.
func linkIndex(s, raw string) int {
	needle := "[[" + raw
	from := 0
	for {
		i := strings.Index(s[from:], needle)
		if i < 0 {
			return -1
		}
		i += from
		rest := s[i+len(needle):]
		if rest == "" || rest[0] == ']' || rest[0] == '|' || strings.HasPrefix(rest, "]]") {
			return i
		}
		from = i + 1
	}
}

// UnresolvedLinks returns the broken wikilinks of one space, or of every
// space when space is "".
func (db *DB) UnresolvedLinks(ctx context.Context, space string) ([]UnresolvedLink, error) {
	q := `SELECT ` + prefixed(noteColumns, "n.") + `, l.raw_target
		FROM links l JOIN notes n ON n.id = l.from_id
		WHERE l.resolved = 0`
	var args []any
	if space != "" {
		q += ` AND n.space = ?`
		args = append(args, space)
	}
	q += ` ORDER BY n.space, n.rel_path, l.raw_target`
	rows, err := db.readers.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnresolvedLink
	for rows.Next() {
		var u UnresolvedLink
		if err := scanNoteInto(rows, &u.Note, &u.RawTarget); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// InboundLinks returns the resolved links pointing at noteID. Rename
// propagation rewrites exactly these.
func (db *DB) InboundLinks(ctx context.Context, noteID string) ([]InboundLink, error) {
	rows, err := db.readers.QueryContext(ctx, `SELECT l.from_id, n.rel_path, n.space, l.raw_target
		FROM links l JOIN notes n ON n.id = l.from_id
		WHERE l.to_id = ? AND l.resolved = 1 ORDER BY n.rel_path, l.raw_target`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InboundLink
	for rows.Next() {
		var l InboundLink
		if err := rows.Scan(&l.FromID, &l.FromRel, &l.FromSpace, &l.RawTarget); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
