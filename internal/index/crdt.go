package index

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Update is one row of note_updates.
type Update struct {
	Seq     int64
	Payload []byte
	Author  string
	TS      time.Time
}

// Snapshot is the compacted state of one note's log.
type Snapshot struct {
	Seq int64
	Doc []byte
	TS  time.Time
}

// AppendUpdate records one CRDT update for a note. author must not be
// empty: the column is NOT NULL and the value is load-bearing for
// attribution.
func AppendUpdate(tx *sql.Tx, noteID string, seq int64, payload []byte, author string, ts time.Time) error {
	if author == "" {
		return errors.New("index: update author is empty")
	}
	_, err := tx.Exec(`INSERT INTO note_updates (note_id, seq, payload, author, ts) VALUES (?, ?, ?, ?, ?)`,
		noteID, seq, payload, author, ts.UnixNano())
	return err
}

// WriteSnapshot stores the compacted document for a note and removes the
// log rows it covers.
func WriteSnapshot(tx *sql.Tx, noteID string, seq int64, doc []byte, ts time.Time) error {
	if _, err := tx.Exec(`INSERT INTO note_snapshots (note_id, seq, doc, ts) VALUES (?, ?, ?, ?)
		ON CONFLICT(note_id) DO UPDATE SET seq = excluded.seq, doc = excluded.doc, ts = excluded.ts`,
		noteID, seq, doc, ts.UnixNano()); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM note_updates WHERE note_id = ? AND seq <= ?`, noteID, seq)
	return err
}

// DeleteLog removes every log row and snapshot for a note.
func DeleteLog(tx *sql.Tx, noteID string) error {
	if _, err := tx.Exec(`DELETE FROM note_updates WHERE note_id = ?`, noteID); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM note_snapshots WHERE note_id = ?`, noteID)
	return err
}

// GetSnapshot returns a note's snapshot, or ok=false when it has none.
func (db *DB) GetSnapshot(ctx context.Context, noteID string) (Snapshot, bool, error) {
	var s Snapshot
	var ts int64
	err := db.readers.QueryRowContext(ctx, `SELECT seq, doc, ts FROM note_snapshots WHERE note_id = ?`, noteID).
		Scan(&s.Seq, &s.Doc, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	s.TS = time.Unix(0, ts).UTC()
	return s, true, nil
}

// Updates returns a note's log rows with seq greater than after, in order.
func (db *DB) Updates(ctx context.Context, noteID string, after int64) ([]Update, error) {
	rows, err := db.readers.QueryContext(ctx,
		`SELECT seq, payload, author, ts FROM note_updates WHERE note_id = ? AND seq > ? ORDER BY seq`, noteID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Update
	for rows.Next() {
		var u Update
		var ts int64
		if err := rows.Scan(&u.Seq, &u.Payload, &u.Author, &ts); err != nil {
			return nil, err
		}
		u.TS = time.Unix(0, ts).UTC()
		out = append(out, u)
	}
	return out, rows.Err()
}

// AuthorPathsSince returns, for each note with recorded ops after ts, the
// set of authors behind those ops and the note's current path. It is the
// durable half of git commit attribution; the live half is the
// reconciliation event feed.
func (db *DB) AuthorPathsSince(ctx context.Context, after time.Time) (map[string]map[string]struct{}, error) {
	rows, err := db.readers.QueryContext(ctx,
		`SELECT DISTINCT u.note_id, u.author, n.rel_path
		 FROM note_updates u LEFT JOIN notes n ON n.id = u.note_id
		 WHERE u.ts > ?`, after.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]struct{}{}
	for rows.Next() {
		var noteID, author string
		var rel sql.NullString
		if err := rows.Scan(&noteID, &author, &rel); err != nil {
			return nil, err
		}
		if !rel.Valid || rel.String == "" {
			continue
		}
		if out[rel.String] == nil {
			out[rel.String] = map[string]struct{}{}
		}
		out[rel.String][author] = struct{}{}
	}
	return out, rows.Err()
}

// LogStats returns the highest seq recorded for a note (0 when none) and
// how many log rows it currently has.
func (db *DB) LogStats(ctx context.Context, noteID string) (maxSeq int64, rows int, err error) {
	var snapSeq sql.NullInt64
	if err = db.readers.QueryRowContext(ctx, `SELECT seq FROM note_snapshots WHERE note_id = ?`, noteID).Scan(&snapSeq); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return
	}
	var logMax sql.NullInt64
	if err = db.readers.QueryRowContext(ctx, `SELECT MAX(seq), COUNT(*) FROM note_updates WHERE note_id = ?`, noteID).Scan(&logMax, &rows); err != nil {
		return
	}
	maxSeq = max(snapSeq.Int64, logMax.Int64)
	return maxSeq, rows, nil
}
