package index

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/drawing"
	"github.com/madeofpendletonwool/yana/internal/render"
	"github.com/madeofpendletonwool/yana/internal/wikilink"
)

// OutboundLink is one wikilink of a note, resolved or not. Kind is
// "link" for [[targets]] and "embed" for ![[targets]].
type OutboundLink struct {
	RawTarget string `json:"raw_target"`
	ToID      string `json:"to_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Resolved  bool   `json:"resolved"`
}

// Backlink is one note linking to another, with the line the link sits on.
type Backlink struct {
	Note      Note   `json:"note"`
	RawTarget string `json:"raw_target"`
	Kind      string `json:"kind,omitempty"`
	Context   string `json:"context"`
}

// UnresolvedLink is one broken wikilink in the unresolved report.
type UnresolvedLink struct {
	Note      Note   `json:"note"`
	RawTarget string `json:"raw_target"`
	Kind      string `json:"kind,omitempty"`
}

// AliasConflict is one alias two or more notes of a space claim. Every
// link through it stays unresolved until the claim is sorted out.
type AliasConflict struct {
	Space string `json:"space"`
	Alias string `json:"alias"`
	Notes []Note `json:"notes"`
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

// RecomputeLinksForNew recomputes the links a newly indexed note can
// change: its own, and those RecomputeLinksNaming finds. The note row and
// its aliases must already exist in tx.
func RecomputeLinksForNew(tx *sql.Tx, noteID string) error {
	var rel, space string
	if err := tx.QueryRow(`SELECT rel_path, space FROM notes WHERE id = ?`, noteID).Scan(&rel, &space); err != nil {
		return err
	}
	aliases, err := AliasesOfTx(tx, noteID)
	if err != nil {
		return err
	}
	return RecomputeLinksNaming(tx, space, rel, aliases, noteID)
}

// RecomputeLinksNaming recomputes the links of the notes in space whose
// raw targets could name the note at rel, plus those of the notes in
// also. A target reaches a note through its path, its filename or an
// alias, and every path or filename spelling contains the note's stem,
// so a target holding neither the stem nor an alias resolves the same
// whether that note is there or not. Adding or removing a note then costs
// its would-be linkers, not every body in the space.
func RecomputeLinksNaming(tx *sql.Tx, space, rel string, aliases []string, also ...string) error {
	stem, _ := drawing.SplitExt(path.Base(rel))
	ids := map[string]struct{}{}
	for _, id := range also {
		ids[id] = struct{}{}
	}
	needles := []string{stem}
	for _, a := range aliases {
		if a = strings.TrimSpace(a); a != "" {
			needles = append(needles, a)
		}
	}
	for _, needle := range needles {
		rows, err := tx.Query(`SELECT DISTINCT l.from_id FROM links l JOIN notes n ON n.id = l.from_id
			WHERE n.space = ? AND instr(l.raw_target, ?) > 0`, space, needle)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids[id] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	res, err := spaceResolver(tx, space)
	if err != nil {
		return err
	}
	for id := range ids {
		var from string
		if err := tx.QueryRow(`SELECT rel_path FROM notes WHERE id = ?`, id).Scan(&from); err != nil {
			return err
		}
		kind, raw, err := rawBodyOfTx(tx, id)
		if err != nil {
			return err
		}
		if err := writeLinks(tx, id, kind, res, from, raw); err != nil {
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

const resolverQuery = `SELECT n.id, n.rel_path, COALESCE(a.aliases, '') FROM notes n
	LEFT JOIN (SELECT note_id, GROUP_CONCAT(alias, char(30)) AS aliases FROM aliases GROUP BY note_id) a
		ON a.note_id = n.id
	WHERE n.space = ?`

// SpaceResolver builds the resolver for one space outside a write
// transaction, for read paths that need to resolve targets (the read
// view inlining embeds, the live preview).
func (db *DB) SpaceResolver(ctx context.Context, space string) (*wikilink.Resolver, error) {
	rows, err := db.readers.QueryContext(ctx, resolverQuery, space)
	if err != nil {
		return nil, err
	}
	return resolverFromRows(space, rows)
}

// spaceResolver builds the resolver for one space from the notes and
// aliases tables as tx sees it. Aliases ride along through a group
// concat with a separator no alias may contain.
func spaceResolver(tx *sql.Tx, space string) (*wikilink.Resolver, error) {
	rows, err := tx.Query(resolverQuery, space)
	if err != nil {
		return nil, err
	}
	return resolverFromRows(space, rows)
}

func resolverFromRows(space string, rows *sql.Rows) (*wikilink.Resolver, error) {
	sep := string(rune(30))
	var refs []wikilink.NoteRef
	for rows.Next() {
		var r wikilink.NoteRef
		var joined string
		if err := rows.Scan(&r.ID, &r.RelPath, &joined); err != nil {
			rows.Close()
			return nil, err
		}
		if joined != "" {
			r.Aliases = strings.Split(joined, sep)
		}
		refs = append(refs, r)
	}
	rows.Close()
	return wikilink.NewResolver(space, refs), rows.Err()
}

// writeLinks replaces the outbound rows of one note. Links are exactly
// the distinct targets the body spells: [[targets]] for markdown notes,
// data-wikilink attributes for HTML ones, element links of the scene for
// drawings; markdown embeds (![[targets]])
// are recorded as their own kind.
func writeLinks(tx *sql.Tx, fromID, kind string, res *wikilink.Resolver, fromRel, raw string) error {
	if _, err := tx.Exec(`DELETE FROM links WHERE from_id = ?`, fromID); err != nil {
		return err
	}
	write := func(raws []string, linkKind string) error {
		for _, raw := range raws {
			r := res.Resolve(raw, fromRel)
			var toID any
			resolved := 0
			if r.OK {
				toID = r.ToID
				resolved = 1
			}
			if _, err := tx.Exec(`INSERT INTO links (from_id, to_id, raw_target, kind, resolved) VALUES (?, ?, ?, ?, ?)`,
				fromID, toID, raw, linkKind, resolved); err != nil {
				return err
			}
		}
		return nil
	}
	if kind == drawing.Kind {
		// An unparseable drawing has no links.
		sc, _, err := drawing.Parse([]byte(raw))
		if err != nil {
			return nil
		}
		return write(sc.Links(), "link")
	}
	if kind == "html" {
		return write(render.HTMLWikiLinks([]byte(raw)), "link")
	}
	if err := write(render.WikiLinks([]byte(raw)), "link"); err != nil {
		return err
	}
	return write(render.WikiEmbeds([]byte(raw)), "embed")
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
		`SELECT raw_target, COALESCE(to_id, ''), COALESCE(kind, 'link'), resolved FROM links WHERE from_id = ? ORDER BY raw_target`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboundLink
	for rows.Next() {
		var l OutboundLink
		var resolved int
		if err := rows.Scan(&l.RawTarget, &l.ToID, &l.Kind, &resolved); err != nil {
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
	rows, err := db.readers.QueryContext(ctx, `SELECT `+prefixed(noteColumns, "n.")+`, l.raw_target, COALESCE(l.kind, 'link')
		FROM links l JOIN notes n ON n.id = l.from_id
		WHERE l.to_id = ? AND l.resolved = 1 ORDER BY n.rel_path`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Backlink
	for rows.Next() {
		var b Backlink
		if err := scanNoteInto(rows, &b.Note, &b.RawTarget, &b.Kind); err != nil {
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
	var conflict sql.NullString
	dst := []any{&n.ID, &n.Space, &n.RelPath, &n.Title, &n.Preview, &n.Kind, &n.ContentHash,
		&n.Size, &mtime, &created, &updated, &order, &trusted, &conflict}
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
	n.ConflictOf = conflict.String
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
	q := `SELECT ` + prefixed(noteColumns, "n.") + `, l.raw_target, COALESCE(l.kind, 'link')
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
		if err := scanNoteInto(rows, &u.Note, &u.RawTarget, &u.Kind); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AliasConflicts returns the aliases two or more notes claim, with the
// claiming notes, for one space or for every space when space is "".
func (db *DB) AliasConflicts(ctx context.Context, space string) ([]AliasConflict, error) {
	q := `SELECT a.space, a.alias FROM aliases a
		GROUP BY a.space, a.alias HAVING COUNT(DISTINCT a.note_id) > 1`
	var args []any
	if space != "" {
		q = `SELECT a.space, a.alias FROM aliases a WHERE a.space = ?
		GROUP BY a.space, a.alias HAVING COUNT(DISTINCT a.note_id) > 1`
		args = append(args, space)
	}
	q += ` ORDER BY a.space, a.alias`
	rows, err := db.readers.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	type key struct{ space, alias string }
	var keys []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.space, &k.alias); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []AliasConflict
	for _, k := range keys {
		nrows, err := db.readers.QueryContext(ctx, `SELECT `+noteColumns+`
			FROM notes WHERE id IN (SELECT note_id FROM aliases WHERE space = ? AND alias = ?) ORDER BY rel_path`,
			k.space, k.alias)
		if err != nil {
			return nil, err
		}
		c := AliasConflict{Space: k.space, Alias: k.alias}
		for nrows.Next() {
			n, err := scanNote(nrows)
			if err != nil {
				nrows.Close()
				return nil, err
			}
			c.Notes = append(c.Notes, n)
		}
		nrows.Close()
		if err := nrows.Err(); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
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
