package index

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/madeofpendletonwool/yana/internal/spaces"
)

// Space membership queries: the spaces and space_members tables are the
// cached form of every space's .space.yml.

// --- space membership ----------------------------------------------------

// SpaceRow is one cached space: its directory name and .space.yml name.
type SpaceRow struct {
	Space string `json:"name"`
	Label string `json:"label"` // display name from .space.yml, or the directory name
	Notes int    `json:"notes"`
}

// Member is one space_members row joined with the user's username.
type SpaceMember struct {
	UserID   string `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// SyncSpace replaces a space's cached rows: its spaces row and its
// member list. members entries whose user cannot be resolved (the
// username or id matches no account) are dropped; the .space.yml stays
// authoritative and a later edit or scan retries them.
func SyncSpace(tx *sql.Tx, space, label string, members []SpaceMember, now time.Time) error {
	if _, err := tx.Exec(`INSERT INTO spaces (space, name, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(space) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`,
		space, label, now.UnixNano()); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM space_members WHERE space = ?`, space); err != nil {
		return err
	}
	for _, m := range members {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO space_members (space, user_id, role) VALUES (?, ?, ?)`, space, m.UserID, m.Role); err != nil {
			return err
		}
	}
	return nil
}

// RetireSpace removes a space's cached rows when its directory is gone.
func RetireSpace(tx *sql.Tx, space string) error {
	if _, err := tx.Exec(`DELETE FROM spaces WHERE space = ?`, space); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM space_members WHERE space = ?`, space)
	return err
}

// ListSpaces returns every known space with a note count, ordered by
// name.
func (db *DB) ListSpaces(ctx context.Context) ([]SpaceRow, error) {
	rows, err := db.readers.QueryContext(ctx, `
		SELECT s.space, s.name,
		       (SELECT COUNT(*) FROM notes n WHERE n.space = s.space)
		FROM spaces s
		UNION
		SELECT n.space, COALESCE((SELECT name FROM spaces s WHERE s.space = n.space), n.space), COUNT(*)
		FROM notes n WHERE n.space NOT IN (SELECT space FROM spaces)
		GROUP BY n.space
		ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SpaceRow
	for rows.Next() {
		var r SpaceRow
		if err := rows.Scan(&r.Space, &r.Label, &r.Notes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SpaceLabel returns a space's display name, or "" when unknown.
func (db *DB) SpaceLabel(ctx context.Context, space string) (string, error) {
	var label string
	err := db.readers.QueryRowContext(ctx, `SELECT name FROM spaces WHERE space = ?`, space).Scan(&label)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return label, err
}

// SpaceSpec returns the parsed membership of a space, usernames
// resolved.
func (db *DB) SpaceSpec(ctx context.Context, space string) (label string, members []SpaceMember, err error) {
	if err = db.readers.QueryRowContext(ctx, `SELECT name FROM spaces WHERE space = ?`, space).Scan(&label); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, nil
		}
		return "", nil, err
	}
	rows, err := db.readers.QueryContext(ctx, `
		SELECT m.user_id, COALESCE(u.username, ''), m.role
		FROM space_members m LEFT JOIN users u ON u.id = m.user_id
		WHERE m.space = ? ORDER BY u.username, m.user_id`, space)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m SpaceMember
		if err := rows.Scan(&m.UserID, &m.Username, &m.Role); err != nil {
			return "", nil, err
		}
		members = append(members, m)
	}
	return label, members, rows.Err()
}

// MemberRole returns the user's cached role in one space. ok is false
// when they are not a member.
func (db *DB) MemberRole(ctx context.Context, userID, space string) (role string, ok bool, err error) {
	err = db.readers.QueryRowContext(ctx,
		`SELECT role FROM space_members WHERE space = ? AND user_id = ?`, space, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return role, true, nil
}

// NoteAuthz answers, in one query, which space a note lives in and
// whether the user is a member of it. The global owner flag is applied
// by the caller (the row does not know it).
func (db *DB) NoteAuthz(ctx context.Context, userID, noteID string) (space, role string, member bool, err error) {
	var memberRole sql.NullString
	err = db.readers.QueryRowContext(ctx, `
		SELECT n.space, m.role FROM notes n
		LEFT JOIN space_members m ON m.space = n.space AND m.user_id = ?
		WHERE n.id = ?`, userID, noteID).Scan(&space, &memberRole)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, ErrNotFound
	}
	if err != nil {
		return "", "", false, err
	}
	if memberRole.Valid {
		return space, memberRole.String, true, nil
	}
	return space, "", false, nil
}

// MemberSpaces returns the spaces the user is a member of.
func (db *DB) MemberSpaces(ctx context.Context, userID string) ([]string, error) {
	rows, err := db.readers.QueryContext(ctx, `SELECT space FROM space_members WHERE user_id = ? ORDER BY space`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// NoteSpace returns the space one note lives in.
func (db *DB) NoteSpace(ctx context.Context, noteID string) (string, error) {
	var space string
	err := db.readers.QueryRowContext(ctx, `SELECT space FROM notes WHERE id = ?`, noteID).Scan(&space)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return space, err
}

// UserForAuthor resolves an author string ("user:<name>") to the
// account, in the same single lookup as the note's space and role. It is
// the membership half of the WebSocket subscribe check.
func UserForAuthor(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, author, noteID string) (user User, space, role string, err error) {
	name, ok := strings.CutPrefix(author, "user:")
	if !ok || name == "" {
		return User{}, "", "", ErrUserNotFound
	}
	var owner, created int64
	var member sql.NullString
	err = q.QueryRowContext(ctx, `
		SELECT u.id, u.username, u.password_hash, u.is_owner, u.created_at,
		       n.space, m.role
		FROM users u, notes n
		LEFT JOIN space_members m ON m.space = n.space AND m.user_id = u.id
		WHERE u.username = ? COLLATE NOCASE AND n.id = ?`, name, noteID).
		Scan(&user.ID, &user.Username, &user.PasswordHash, &owner, &created, &space, &member)
	if errors.Is(err, sql.ErrNoRows) {
		// Either the user or the note is missing; look again to say which.
		if _, uerr := scanUser(q.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ? COLLATE NOCASE`, name)); uerr != nil {
			return User{}, "", "", ErrUserNotFound
		}
		return User{}, "", "", ErrNotFound
	}
	if err != nil {
		return User{}, "", "", err
	}
	user.IsOwner = owner != 0
	user.CreatedAt = time.Unix(0, created).UTC()
	if member.Valid {
		role = member.String
	}
	return user, space, role, nil
}

// SyncSpaceSpec resolves a parsed .space.yml against the accounts
// table and replaces the space's cached rows. Unresolvable member
// references are skipped and logged; the file stays authoritative, so a
// member whose account is created later needs only a rescan or a file
// touch. It returns how many members were cached.
func (db *DB) SyncSpaceSpec(ctx context.Context, space string, spec spaces.Spec, log *slog.Logger) (int, error) {
	label := spec.Name
	if label == "" {
		label = space
	}
	var members []SpaceMember
	for _, m := range spec.Members {
		u, err := db.GetUser(ctx, m.User)
		if err != nil {
			u, err = db.GetUserByName(ctx, m.User)
		}
		if err != nil {
			if log != nil {
				log.Warn("space member does not match an account; left out of the cache",
					"space", space, "user", m.User)
			}
			continue
		}
		members = append(members, SpaceMember{UserID: u.ID, Username: u.Username, Role: m.Role})
	}
	err := db.Write(ctx, func(tx *sql.Tx) error {
		return SyncSpace(tx, space, label, members, time.Now().UTC())
	})
	return len(members), err
}

// RetireSpacesExcept drops the cached rows of spaces whose directories
// no longer exist.
func (db *DB) RetireSpacesExcept(ctx context.Context, keep map[string]struct{}, log *slog.Logger) error {
	rows, err := db.readers.QueryContext(ctx, `SELECT space FROM spaces`)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		if _, ok := keep[s]; !ok {
			gone = append(gone, s)
		}
	}
	rows.Close()
	if len(gone) == 0 {
		return nil
	}
	return db.Write(ctx, func(tx *sql.Tx) error {
		for _, s := range gone {
			if err := RetireSpace(tx, s); err != nil {
				return err
			}
			if log != nil {
				log.Info("space directory gone; membership cleared", "space", s)
			}
		}
		return nil
	})
}
