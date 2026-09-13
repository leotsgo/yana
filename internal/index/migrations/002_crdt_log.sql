-- CRDT log. Losing it costs edit history, not content: the sidecar under
-- .sync/crdt/ and the note file itself carry the current text.

CREATE TABLE note_updates (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    note_id TEXT NOT NULL,
    seq     INTEGER NOT NULL,
    payload BLOB NOT NULL,
    -- user:<uuid>, agent:<label>, or filesystem. Every op carries one; git
    -- attribution and the history UI depend on it.
    author  TEXT NOT NULL,
    ts      INTEGER NOT NULL
);
CREATE UNIQUE INDEX note_updates_note_seq ON note_updates(note_id, seq);

CREATE TABLE note_snapshots (
    note_id TEXT PRIMARY KEY,
    seq     INTEGER NOT NULL,
    doc     BLOB NOT NULL,
    ts      INTEGER NOT NULL
);
