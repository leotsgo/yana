// Agent tokens: the credentials agents present at /mcp. Real state like
// users and sessions — these rows are not derived from the notes tree.
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

// ErrAgentTokenNotFound is returned when an agent token id has no row.
var ErrAgentTokenNotFound = errors.New("no such agent token")

// ErrAgentLabelTaken is returned when another live token holds the label.
var ErrAgentLabelTaken = errors.New("an agent token with that label exists")

// AgentToken is one row of agent_tokens with its space scope.
type AgentToken struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Spaces     []string   `json:"spaces"`
	CanWrite   bool       `json:"can_write"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt time.Time  `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// HashAgentToken returns the stored form of a token secret.
func HashAgentToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

const agentCols = "id, label, can_write, created_at, last_used_at, revoked_at"

func scanAgentTokens(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]AgentToken, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+agentCols+` FROM agent_tokens ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentToken
	for rows.Next() {
		var t AgentToken
		var canWrite int
		var created, last int64
		var revoked sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Label, &canWrite, &created, &last, &revoked); err != nil {
			return nil, err
		}
		t.CanWrite = canWrite != 0
		t.CreatedAt = time.Unix(0, created).UTC()
		t.LastUsedAt = time.Unix(0, last).UTC()
		if revoked.Valid {
			at := time.Unix(0, revoked.Int64).UTC()
			t.RevokedAt = &at
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Spaces join on the read pool after the row scan; the set is small.
	ids := make([]string, len(out))
	for i, t := range out {
		ids[i] = t.ID
	}
	spaces, err := agentTokenSpaces(ctx, q, ids...)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Spaces = spaces[out[i].ID]
		if out[i].Spaces == nil {
			out[i].Spaces = []string{}
		}
	}
	return out, nil
}

func agentTokenSpaces(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, ids ...string,
) (map[string][]string, error) {
	out := map[string][]string{}
	if len(ids) == 0 {
		return out, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := q.QueryContext(ctx,
		`SELECT token_id, space FROM agent_token_spaces WHERE token_id IN (`+strings.Join(ph, ",")+`) ORDER BY space`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, space string
		if err := rows.Scan(&id, &space); err != nil {
			return nil, err
		}
		out[id] = append(out[id], space)
	}
	return out, rows.Err()
}

// CreateAgentTokenTx inserts a token row with its space scope inside tx.
// A live (unrevoked) token already holding the label is a conflict: git
// attribution maps a label to one actor.
func CreateAgentTokenTx(tx *sql.Tx, t AgentToken, tokenHash string) error {
	var existing int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM agent_tokens WHERE label = ? AND revoked_at IS NULL`, t.Label).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return ErrAgentLabelTaken
	}
	var revoked any
	if t.RevokedAt != nil {
		revoked = t.RevokedAt.UnixNano()
	}
	canWrite := 0
	if t.CanWrite {
		canWrite = 1
	}
	_, err := tx.Exec(`INSERT INTO agent_tokens (id, label, token_hash, can_write, created_at, last_used_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Label, tokenHash, canWrite, t.CreatedAt.UnixNano(), t.LastUsedAt.UnixNano(), revoked)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ErrAgentLabelTaken
		}
		return err
	}
	for _, sp := range t.Spaces {
		if _, err := tx.Exec(`INSERT INTO agent_token_spaces (token_id, space) VALUES (?, ?)`, t.ID, sp); err != nil {
			return err
		}
	}
	return nil
}

// CreateAgentToken writes a token row with its space scope.
func (db *DB) CreateAgentToken(ctx context.Context, t AgentToken, tokenHash string) error {
	return db.Write(ctx, func(tx *sql.Tx) error { return CreateAgentTokenTx(tx, t, tokenHash) })
}

// ListAgentTokens returns every token, revoked ones included.
func (db *DB) ListAgentTokens(ctx context.Context) ([]AgentToken, error) {
	return scanAgentTokens(ctx, db.readers)
}

// RevokeAgentToken marks one token revoked. Its space rows stay so the
// listing keeps showing what it could see.
func (db *DB) RevokeAgentToken(ctx context.Context, id string) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE agent_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
			time.Now().UTC().UnixNano(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrAgentTokenNotFound
		}
		return nil
	})
}

// AgentTokenByHash resolves a secret's hash to its live token. Expired
// does not apply (tokens live until revoked); revoked and missing both
// answer ErrAgentTokenNotFound so a caller cannot distinguish them.
func (db *DB) AgentTokenByHash(ctx context.Context, tokenHash string) (AgentToken, error) {
	var t AgentToken
	var canWrite int
	var created, last int64
	var revoked sql.NullInt64
	err := db.readers.QueryRowContext(ctx,
		`SELECT `+agentCols+` FROM agent_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&t.ID, &t.Label, &canWrite, &created, &last, &revoked)
	if err != nil {
		return AgentToken{}, ErrAgentTokenNotFound
	}
	t.CanWrite = canWrite != 0
	t.CreatedAt = time.Unix(0, created).UTC()
	t.LastUsedAt = time.Unix(0, last).UTC()
	if revoked.Valid {
		return AgentToken{}, ErrAgentTokenNotFound
	}
	spaces, err := agentTokenSpaces(ctx, db.readers, t.ID)
	if err != nil {
		return AgentToken{}, err
	}
	t.Spaces = spaces[t.ID]
	if t.Spaces == nil {
		t.Spaces = []string{}
	}
	return t, nil
}

// TouchAgentToken records a use. Failures are not fatal; the last-used
// column is telemetry, not a gate.
func (db *DB) TouchAgentToken(ctx context.Context, id string) {
	_ = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE agent_tokens SET last_used_at = ? WHERE id = ?`, time.Now().UTC().UnixNano(), id)
		return err
	})
}
