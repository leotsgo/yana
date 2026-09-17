// Git remotes: where the history layer pushes backups. Real state, like
// agent tokens — the owner configures these in settings.
package index

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrGitRemoteNotFound is returned when a remote id has no row.
var ErrGitRemoteNotFound = errors.New("no such remote")

// ErrGitRemoteNameTaken is returned when another remote holds the name.
var ErrGitRemoteNameTaken = errors.New("a remote with that name exists")

// GitRemote is one row of git_remotes. Secret is the sealed credential
// and never leaves the server; HasSecret is what the API reports.
type GitRemote struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Schedule  string    `json:"schedule"`
	PushHour  int       `json:"push_hour"`
	Username  string    `json:"username"`
	Secret    []byte    `json:"-"`
	HasSecret bool      `json:"has_secret"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	Pushes    int64     `json:"pushes"`
	LastPush  time.Time `json:"last_push"`
	LastError string    `json:"last_error"`
	// LastErrorAt is zero when the newest push succeeded.
	LastErrorAt time.Time `json:"last_error_at"`
}

const gitRemoteCols = "id, name, url, schedule, push_hour, username, secret, enabled, created_at, pushes, last_push, last_error, last_error_at"

func scanGitRemote(row interface{ Scan(...any) error }) (GitRemote, error) {
	var r GitRemote
	var enabled int
	var created, lastPush, lastErrAt int64
	var secret []byte
	if err := row.Scan(&r.ID, &r.Name, &r.URL, &r.Schedule, &r.PushHour, &r.Username, &secret, &enabled,
		&created, &r.Pushes, &lastPush, &r.LastError, &lastErrAt); err != nil {
		return GitRemote{}, err
	}
	r.Secret = secret
	r.HasSecret = len(secret) > 0
	r.Enabled = enabled != 0
	r.CreatedAt = time.Unix(0, created).UTC()
	if lastPush > 0 {
		r.LastPush = time.Unix(0, lastPush).UTC()
	}
	if lastErrAt > 0 {
		r.LastErrorAt = time.Unix(0, lastErrAt).UTC()
	}
	return r, nil
}

// ListGitRemotes returns every remote, ordered by creation.
func (db *DB) ListGitRemotes(ctx context.Context) ([]GitRemote, error) {
	rows, err := db.readers.QueryContext(ctx, `SELECT `+gitRemoteCols+` FROM git_remotes ORDER BY created_at, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GitRemote
	for rows.Next() {
		r, err := scanGitRemote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetGitRemote returns one remote by id.
func (db *DB) GetGitRemote(ctx context.Context, id string) (GitRemote, error) {
	r, err := scanGitRemote(db.readers.QueryRowContext(ctx, `SELECT `+gitRemoteCols+` FROM git_remotes WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return GitRemote{}, ErrGitRemoteNotFound
	}
	return r, err
}

// CreateGitRemote inserts a remote. The name must be unique.
func (db *DB) CreateGitRemote(ctx context.Context, r GitRemote) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO git_remotes (id, name, url, schedule, push_hour, username, secret, enabled, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, r.Name, r.URL, r.Schedule, r.PushHour, r.Username, nullBytes(r.Secret), boolInt(r.Enabled), r.CreatedAt.UnixNano())
		if err != nil && strings.Contains(err.Error(), "UNIQUE") {
			return ErrGitRemoteNameTaken
		}
		return err
	})
}

// UpdateGitRemote rewrites a remote's settings. Secret replaces the stored
// credential when keepSecret is false; otherwise the stored one stays.
func (db *DB) UpdateGitRemote(ctx context.Context, r GitRemote, keepSecret bool) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		var res sql.Result
		var err error
		if keepSecret {
			res, err = tx.Exec(`UPDATE git_remotes SET name = ?, url = ?, schedule = ?, push_hour = ?, username = ?, enabled = ? WHERE id = ?`,
				r.Name, r.URL, r.Schedule, r.PushHour, r.Username, boolInt(r.Enabled), r.ID)
		} else {
			res, err = tx.Exec(`UPDATE git_remotes SET name = ?, url = ?, schedule = ?, push_hour = ?, username = ?, secret = ?, enabled = ? WHERE id = ?`,
				r.Name, r.URL, r.Schedule, r.PushHour, r.Username, nullBytes(r.Secret), boolInt(r.Enabled), r.ID)
		}
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") {
				return ErrGitRemoteNameTaken
			}
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrGitRemoteNotFound
		}
		return nil
	})
}

// DeleteGitRemote removes a remote.
func (db *DB) DeleteGitRemote(ctx context.Context, id string) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`DELETE FROM git_remotes WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrGitRemoteNotFound
		}
		return nil
	})
}

// RecordGitPush stores the outcome of a push: a success bumps the counter
// and clears the error; a failure keeps the last success and records why.
func (db *DB) RecordGitPush(ctx context.Context, id string, at time.Time, pushErr error) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if pushErr == nil {
			_, err = tx.Exec(`UPDATE git_remotes SET pushes = pushes + 1, last_push = ?, last_error = '', last_error_at = 0 WHERE id = ?`,
				at.UnixNano(), id)
		} else {
			msg := pushErr.Error()
			if len(msg) > 2000 {
				msg = msg[:2000]
			}
			_, err = tx.Exec(`UPDATE git_remotes SET last_error = ?, last_error_at = ? WHERE id = ?`, msg, at.UnixNano(), id)
		}
		return err
	})
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
