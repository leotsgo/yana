// Conflict copies: notes whose file names say another note survived a
// collision. Two paths park them — a restore over an occupied path
// (name.conflict-<ts>.md) and a save over a diverged HTML note
// (name.conflict-<ts>.html) — and this file is where the index learns
// to recognise them. The column itself is derived: every scan recomputes
// it from the paths on disk.
package index

import (
	"context"
	"database/sql"
	"path"
	"regexp"
	"strings"
)

// conflictNameRe matches the stem of a conflict copy's file name:
// anything, a literal .conflict-, the timestamp either writer uses
// (YYYYMMDDTHHMMSS or YYYYMMDD-HHMMSS), and an optional -2, -3… the
// HTML writer adds when even the conflict name is taken.
var conflictNameRe = regexp.MustCompile(`(?i)^(.+)\.conflict-\d{8}[-T]\d{6}(-\d+)?$`)

// ConflictSurvivorPath returns the path of the note a conflict copy
// belongs to — the same directory and extension with the conflict
// segment removed — or "" when rel is not a conflict copy.
func ConflictSurvivorPath(rel string) string {
	base := path.Base(rel)
	ext := path.Ext(base)
	if ext == "" {
		return ""
	}
	m := conflictNameRe.FindStringSubmatch(strings.TrimSuffix(base, ext))
	if m == nil {
		return ""
	}
	dir := path.Dir(rel)
	if dir == "." {
		return m[1] + ext
	}
	return dir + "/" + m[1] + ext
}

// RecomputeConflicts repoints every conflict copy at the note that
// survived its collision, clearing the pointer when the survivor is
// gone or the name was never a conflict. Runs inside the same
// transaction as the scan's upserts, the way the link recompute does.
func RecomputeConflicts(tx *sql.Tx) error {
	return recomputeConflicts(tx, "")
}

// RecomputeConflictsIn recomputes the conflict pointers of one
// directory (and everything under it); "" runs over the whole table.
// The relationship never crosses a directory — the copy is parked
// beside its survivor — so a single note's reindex only needs its own
// corner of the tree.
func RecomputeConflictsIn(tx *sql.Tx, dir string) error {
	return recomputeConflicts(tx, dir)
}

func recomputeConflicts(tx *sql.Tx, dir string) error {
	q := `SELECT id, rel_path, conflict_of FROM notes`
	var args []any
	if dir != "" {
		q = `SELECT id, rel_path, conflict_of FROM notes WHERE substr(rel_path, 1, ?) = ?`
		args = append(args, len(dir), dir+"/")
	}
	rows, err := tx.Query(q, args...)
	if err != nil {
		return err
	}
	type row struct {
		id, rel, cur string
		su           string // survivor path, "" when not a conflict
	}
	var all []row
	byPath := map[string]string{}
	for rows.Next() {
		var r row
		var cur sql.NullString
		if err := rows.Scan(&r.id, &r.rel, &cur); err != nil {
			rows.Close()
			return err
		}
		r.cur = cur.String
		byPath[r.rel] = r.id
		r.su = ConflictSurvivorPath(r.rel)
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range all {
		var target string
		if r.su != "" {
			if id, ok := byPath[r.su]; ok && id != r.id {
				target = id
			}
		}
		if target == r.cur {
			continue
		}
		var v any
		if target != "" {
			v = target
		}
		if _, err := tx.Exec(`UPDATE notes SET conflict_of = ? WHERE id = ?`, v, r.id); err != nil {
			return err
		}
	}
	return nil
}

// ConflictsOf lists the conflict copies pointing at one note, oldest
// first.
func (db *DB) ConflictsOf(ctx context.Context, id string) ([]Note, error) {
	rows, err := db.readers.QueryContext(ctx,
		`SELECT `+noteColumns+` FROM notes WHERE conflict_of = ? ORDER BY rel_path`, id)
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

// ConflictRow is one conflict copy as the Data page lists it: the copy
// itself and, while it still exists, the note it belongs to.
type ConflictRow struct {
	Note Note  `json:"note"`
	Of   *Note `json:"of,omitempty"`
}

// ListConflicts returns every conflict copy in the allowed spaces (nil
// is unrestricted, like search). A name that merely looks like a
// conflict is not one; the pattern decides.
func (db *DB) ListConflicts(ctx context.Context, allowed []string) ([]ConflictRow, error) {
	q := `SELECT ` + noteColumns + ` FROM notes WHERE rel_path LIKE ?`
	var args []any
	args = append(args, `%.conflict-%`)
	if allowed != nil {
		if len(allowed) == 0 {
			return []ConflictRow{}, nil
		}
		ph := make([]string, len(allowed))
		for i, sp := range allowed {
			ph[i] = "?"
			args = append(args, sp)
		}
		q += ` AND space IN (` + strings.Join(ph, ",") + `)`
	}
	q += ` ORDER BY rel_path`
	rows, err := db.readers.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	var hits []Note
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []ConflictRow{}
	for _, n := range hits {
		if ConflictSurvivorPath(n.RelPath) == "" {
			continue
		}
		out = append(out, ConflictRow{Note: n})
	}
	if len(out) == 0 {
		return out, nil
	}
	for i := range out {
		su, err := db.GetNoteByPath(ctx, ConflictSurvivorPath(out[i].Note.RelPath))
		if err != nil {
			continue
		}
		if su.ID == out[i].Note.ID {
			continue
		}
		of := su
		out[i].Of = &of
	}
	return out, nil
}
