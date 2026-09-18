// Public links: a note served read-only at an unguessable URL with no
// account. Real state like sessions and agent tokens — not derived from
// the tree, not written to the file — so a link is index state that an
// export does not carry and a fresh index does not have.
package index

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// ErrPublicLinkNotFound is returned for a link that is missing, revoked
// or expired; the three are indistinguishable by design.
var ErrPublicLinkNotFound = errors.New("no such public link")

// PublicLink is one row of public_links. The token is not here: only its
// hash is stored, and the server derives the token from ID when it
// needs to show the URL again.
type PublicLink struct {
	ID        string     `json:"id"`
	NoteID    string     `json:"note_id"`
	CreatedBy string     `json:"created_by"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	RevokedAt *time.Time `json:"-"`
}

// PublicLinkRow is a live link with the note it opens, for listings.
type PublicLinkRow struct {
	PublicLink
	Note Note `json:"-"`
}

// HashPublicToken returns the stored form of a link token.
func HashPublicToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

const publicLinkCols = "id, note_id, created_by, created_at, expires_at, revoked_at"

func scanPublicLink(row interface{ Scan(...any) error }) (PublicLink, error) {
	var l PublicLink
	var created int64
	var expires, revoked sql.NullInt64
	if err := row.Scan(&l.ID, &l.NoteID, &l.CreatedBy, &created, &expires, &revoked); err != nil {
		return l, err
	}
	l.CreatedAt = time.Unix(0, created).UTC()
	if expires.Valid {
		at := time.Unix(0, expires.Int64).UTC()
		l.ExpiresAt = &at
	}
	if revoked.Valid {
		at := time.Unix(0, revoked.Int64).UTC()
		l.RevokedAt = &at
	}
	return l, nil
}

// liveLinkWhere is the predicate for a link that still opens its note.
const liveLinkWhere = "revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)"

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixNano()
}

// PublicLinkForNote returns the note's live link, or ErrPublicLinkNotFound.
func (db *DB) PublicLinkForNote(ctx context.Context, noteID string, now time.Time) (PublicLink, error) {
	l, err := scanPublicLink(db.readers.QueryRowContext(ctx,
		`SELECT `+publicLinkCols+` FROM public_links WHERE note_id = ? AND `+liveLinkWhere, noteID, now.UnixNano()))
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrPublicLinkNotFound
	}
	return l, err
}

// CreatePublicLink makes the note's link, or returns the live one it
// already has: one link per note, so sharing twice hands out the same
// URL. hashFor derives the token hash for a new row's id, which is how
// the token is never stored yet can be shown again.
func (db *DB) CreatePublicLink(ctx context.Context, l PublicLink, hashFor func(id string) string, now time.Time) (PublicLink, bool, error) {
	var out PublicLink
	created := false
	err := db.Write(ctx, func(tx *sql.Tx) error {
		existing, err := scanPublicLink(tx.QueryRow(
			`SELECT `+publicLinkCols+` FROM public_links WHERE note_id = ? AND `+liveLinkWhere, l.NoteID, now.UnixNano()))
		if err == nil {
			out = existing
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.Exec(`INSERT INTO public_links (`+publicLinkCols+`, token_hash) VALUES (?, ?, ?, ?, ?, NULL, ?)`,
			l.ID, l.NoteID, l.CreatedBy, l.CreatedAt.UnixNano(), nullTime(l.ExpiresAt), hashFor(l.ID))
		if err != nil {
			return err
		}
		out = l
		created = true
		return nil
	})
	return out, created, err
}

// SetPublicLinkExpiry changes when the note's live link stops working.
func (db *DB) SetPublicLinkExpiry(ctx context.Context, noteID string, expires *time.Time, now time.Time) (PublicLink, error) {
	var out PublicLink
	err := db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE public_links SET expires_at = ? WHERE note_id = ? AND `+liveLinkWhere,
			nullTime(expires), noteID, now.UnixNano())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrPublicLinkNotFound
		}
		out, err = scanPublicLink(tx.QueryRow(
			`SELECT `+publicLinkCols+` FROM public_links WHERE note_id = ? AND `+liveLinkWhere, noteID, now.UnixNano()))
		if errors.Is(err, sql.ErrNoRows) {
			// The new expiry is already in the past: the link is gone.
			return ErrPublicLinkNotFound
		}
		return err
	})
	return out, err
}

// RevokePublicLink retires the note's live link. A note with none is not
// an error: the outcome — no live link — holds either way.
func (db *DB) RevokePublicLink(ctx context.Context, noteID string, now time.Time) (bool, error) {
	var n int64
	err := db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE public_links SET revoked_at = ? WHERE note_id = ? AND revoked_at IS NULL`, now.UnixNano(), noteID)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n > 0, err
}

// RevokePublicLinksTx retires every link of a note inside tx. RetireNote
// calls it: a deleted note's link dies with it and a restore does not
// bring it back.
func RevokePublicLinksTx(tx *sql.Tx, noteID string, now time.Time) error {
	_, err := tx.Exec(`UPDATE public_links SET revoked_at = ? WHERE note_id = ? AND revoked_at IS NULL`, now.UnixNano(), noteID)
	return err
}

// PublicLinkByHash resolves a token's hash to its live link and the note
// it opens. Revoked, expired, missing, and a note that no longer exists
// all answer ErrPublicLinkNotFound.
func (db *DB) PublicLinkByHash(ctx context.Context, tokenHash string, now time.Time) (PublicLink, Note, error) {
	l, err := scanPublicLink(db.readers.QueryRowContext(ctx,
		`SELECT `+publicLinkCols+` FROM public_links WHERE token_hash = ? AND `+liveLinkWhere, tokenHash, now.UnixNano()))
	if errors.Is(err, sql.ErrNoRows) {
		return PublicLink{}, Note{}, ErrPublicLinkNotFound
	}
	if err != nil {
		return PublicLink{}, Note{}, err
	}
	n, err := db.GetNote(ctx, l.NoteID)
	if errors.Is(err, ErrNotFound) {
		return PublicLink{}, Note{}, ErrPublicLinkNotFound
	}
	if err != nil {
		return PublicLink{}, Note{}, err
	}
	return l, n, nil
}

// PublicNoteIDs returns the ids of every note with a live link, for the
// tree's globe marks.
func (db *DB) PublicNoteIDs(ctx context.Context, now time.Time) (map[string]bool, error) {
	rows, err := db.readers.QueryContext(ctx, `SELECT note_id FROM public_links WHERE `+liveLinkWhere, now.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ListPublicLinks returns every live link whose note is in one of the
// spaces (nil: every space), newest first. A link whose note is gone
// from the index is not listed; it does not open anything either.
func (db *DB) ListPublicLinks(ctx context.Context, spaces []string, now time.Time) ([]PublicLinkRow, error) {
	q := `SELECT ` + prefixed(noteColumns, "n.") + `, ` + prefixed(publicLinkCols, "l.") + `
		FROM public_links l JOIN notes n ON n.id = l.note_id WHERE ` + liveLinkWhere
	args := []any{now.UnixNano()}
	if spaces != nil {
		if len(spaces) == 0 {
			return []PublicLinkRow{}, nil
		}
		ph := make([]string, len(spaces))
		for i, sp := range spaces {
			ph[i] = "?"
			args = append(args, sp)
		}
		q += ` AND n.space IN (` + strings.Join(ph, ",") + `)`
	}
	q += ` ORDER BY l.created_at DESC`
	rows, err := db.readers.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PublicLinkRow{}
	for rows.Next() {
		var r PublicLinkRow
		var created int64
		var expires, revoked sql.NullInt64
		if err := scanNoteInto(rows, &r.Note, &r.ID, &r.NoteID, &r.CreatedBy, &created, &expires, &revoked); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(0, created).UTC()
		if expires.Valid {
			at := time.Unix(0, expires.Int64).UTC()
			r.ExpiresAt = &at
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RevokePublicLinksIn retires every live link whose note is in one of
// the spaces (nil: every space) and reports how many.
func (db *DB) RevokePublicLinksIn(ctx context.Context, spaces []string, now time.Time) (int64, error) {
	var n int64
	err := db.Write(ctx, func(tx *sql.Tx) error {
		q := `UPDATE public_links SET revoked_at = ? WHERE ` + liveLinkWhere +
			` AND note_id IN (SELECT id FROM notes`
		args := []any{now.UnixNano(), now.UnixNano()}
		if spaces != nil {
			if len(spaces) == 0 {
				return nil
			}
			ph := make([]string, len(spaces))
			for i, sp := range spaces {
				ph[i] = "?"
				args = append(args, sp)
			}
			q += ` WHERE space IN (` + strings.Join(ph, ",") + `)`
		}
		q += `)`
		res, err := tx.Exec(q, args...)
		if err != nil {
			return err
		}
		n, _ = res.RowsAffected()
		return nil
	})
	return n, err
}
