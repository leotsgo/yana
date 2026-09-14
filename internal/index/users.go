// Users and sessions. Unlike the rest of this package's content, these
// two tables are real state; everything else here is derived.
package index

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// User is one row of the users table.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	IsOwner      bool      `json:"is_owner"`
	CreatedAt    time.Time `json:"created_at"`
}

// ErrUserExists is returned when the username is taken.
var ErrUserExists = errors.New("username already exists")

// ErrUserNotFound is returned when a user id or name has no row.
var ErrUserNotFound = errors.New("no such user")

// ValidUsername reports whether s is an acceptable username.
func ValidUsername(s string) bool {
	if len(s) < 2 || len(s) > 32 {
		return false
	}
	s = strings.ToLower(s)
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// CreateUserTx inserts a user row inside tx. Usernames are unique
// case-insensitively.
func CreateUserTx(tx *sql.Tx, u User) error {
	owner := 0
	if u.IsOwner {
		owner = 1
	}
	_, err := tx.Exec(`INSERT INTO users (id, username, password_hash, is_owner, created_at) VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, owner, u.CreatedAt.UnixNano())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrUserExists
		}
		return err
	}
	return nil
}

// CreateUser writes a user row.
func (db *DB) CreateUser(ctx context.Context, u User) error {
	return db.Write(ctx, func(tx *sql.Tx) error { return CreateUserTx(tx, u) })
}

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var owner int
	var created int64
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &owner, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return u, ErrUserNotFound
		}
		return u, err
	}
	u.IsOwner = owner != 0
	u.CreatedAt = time.Unix(0, created).UTC()
	return u, nil
}

const userCols = "id, username, password_hash, is_owner, created_at"

// GetUser returns a user by id.
func (db *DB) GetUser(ctx context.Context, id string) (User, error) {
	return scanUser(db.readers.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// GetUserByName returns a user by username (case-insensitive).
func (db *DB) GetUserByName(ctx context.Context, username string) (User, error) {
	return scanUser(db.readers.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ? COLLATE NOCASE`, username))
}

// CountUsers returns how many accounts exist.
func (db *DB) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := db.readers.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// ListUsers returns every account.
func (db *DB) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := db.readers.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteUser removes a user and cascades their sessions.
func (db *DB) DeleteUser(ctx context.Context, id string) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrUserNotFound
		}
		// Sessions cascade; membership rows for the gone id are moot but
		// cheap to drop here.
		_, err = tx.Exec(`DELETE FROM space_members WHERE user_id = ?`, id)
		return err
	})
}

// SetPasswordHash replaces a user's password hash.
func (db *DB) SetPasswordHash(ctx context.Context, id, hash string) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrUserNotFound
		}
		return nil
	})
}

// --- sessions ------------------------------------------------------------

// Session is one device-labelled session.
type Session struct {
	ID         string     `json:"id"`
	UserID     string     `json:"-"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt time.Time  `json:"last_used_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// CreateSession inserts a session row carrying the hash of its refresh
// token.
func (db *DB) CreateSession(ctx context.Context, s Session, refreshHash string) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		var revoked any
		if s.RevokedAt != nil {
			revoked = s.RevokedAt.UnixNano()
		}
		_, err := tx.Exec(`INSERT INTO sessions (id, user_id, refresh_hash, label, created_at, last_used_at, expires_at, revoked_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			s.ID, s.UserID, refreshHash, s.Label, s.CreatedAt.UnixNano(), s.LastUsedAt.UnixNano(), s.ExpiresAt.UnixNano(), revoked)
		return err
	})
}

// ErrSessionNotFound is returned when a refresh token has no live session.
var ErrSessionNotFound = errors.New("no such session")

// SessionByRefreshHash looks a session up by its refresh token hash and
// touches last_used_at. Expired, revoked, and missing sessions all come
// back the same so the caller cannot distinguish them.
func (db *DB) SessionByRefreshHash(ctx context.Context, hash string, now time.Time) (Session, error) {
	var s Session
	var created, last, expires int64
	var revoked sql.NullInt64
	err := db.readers.QueryRowContext(ctx,
		`SELECT id, user_id, label, created_at, last_used_at, expires_at, revoked_at FROM sessions WHERE refresh_hash = ?`, hash).
		Scan(&s.ID, &s.UserID, &s.Label, &created, &last, &expires, &revoked)
	if err != nil {
		return Session{}, ErrSessionNotFound
	}
	s.CreatedAt = time.Unix(0, created).UTC()
	s.LastUsedAt = time.Unix(0, last).UTC()
	s.ExpiresAt = time.Unix(0, expires).UTC()
	if revoked.Valid {
		t := time.Unix(0, revoked.Int64).UTC()
		s.RevokedAt = &t
	}
	if s.RevokedAt != nil || !now.Before(s.ExpiresAt) {
		return Session{}, ErrSessionNotFound
	}
	_ = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE sessions SET last_used_at = ? WHERE id = ?`, now.UnixNano(), s.ID)
		return err
	})
	return s, nil
}

// ListSessions returns a user's sessions, newest first.
func (db *DB) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := db.readers.QueryContext(ctx,
		`SELECT id, user_id, label, created_at, last_used_at, expires_at, revoked_at FROM sessions WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		var created, last, expires int64
		var revoked sql.NullInt64
		if err := rows.Scan(&s.ID, &s.UserID, &s.Label, &created, &last, &expires, &revoked); err != nil {
			return nil, err
		}
		s.CreatedAt = time.Unix(0, created).UTC()
		s.LastUsedAt = time.Unix(0, last).UTC()
		s.ExpiresAt = time.Unix(0, expires).UTC()
		if revoked.Valid {
			t := time.Unix(0, revoked.Int64).UTC()
			s.RevokedAt = &t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RevokeSession marks one session revoked. It reports whether the
// session existed and belonged to userID (unless allowAny, for the
// global owner).
func (db *DB) RevokeSession(ctx context.Context, userID, sessionID string, allowAny bool) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		q := `UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`
		args := []any{time.Now().UnixNano(), sessionID}
		if !allowAny {
			q += ` AND user_id = ?`
			args = append(args, userID)
		}
		res, err := tx.Exec(q, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrSessionNotFound
		}
		return nil
	})
}

// RevokeUserSessions revokes every session of a user and returns their
// ids so callers can sever live connections. keep, when not empty, is
// spared (the session that triggered a password change stays live).
func (db *DB) RevokeUserSessions(ctx context.Context, userID string, keep ...string) ([]string, error) {
	spare := make(map[string]bool, len(keep))
	for _, id := range keep {
		spare[id] = true
	}
	var ids []string
	err := db.Write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id FROM sessions WHERE user_id = ? AND revoked_at IS NULL`, userID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if spare[id] {
				continue
			}
			ids = append(ids, id)
		}
		rows.Close()
		_, err = tx.Exec(`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, time.Now().UnixNano(), userID)
		if err != nil {
			return err
		}
		for id := range spare {
			if _, err := tx.Exec(`UPDATE sessions SET revoked_at = NULL WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	return ids, err
}
