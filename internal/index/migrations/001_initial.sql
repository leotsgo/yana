-- Derivable from the notes tree. Safe to drop and rebuild by rescanning.

CREATE TABLE notes (
    id           TEXT PRIMARY KEY,
    space        TEXT NOT NULL,
    rel_path     TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    preview      TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('md', 'html')),
    content_hash TEXT NOT NULL,
    size         INTEGER NOT NULL,
    mtime        INTEGER NOT NULL,
    created      INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    sort_order   INTEGER,
    trusted      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX notes_space_path ON notes(space, rel_path);

-- Body text lives here so the FTS index can be external-content and the
-- notes row stays small.
CREATE TABLE note_bodies (
    note_rowid INTEGER PRIMARY KEY,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL
);
CREATE TRIGGER notes_ad AFTER DELETE ON notes BEGIN
    DELETE FROM note_bodies WHERE note_rowid = old.rowid;
END;

CREATE VIRTUAL TABLE notes_fts USING fts5(
    title, body,
    content = 'note_bodies',
    content_rowid = 'note_rowid',
    tokenize = 'trigram'
);

CREATE TRIGGER note_bodies_ai AFTER INSERT ON note_bodies BEGIN
    INSERT INTO notes_fts(rowid, title, body) VALUES (new.note_rowid, new.title, new.body);
END;
CREATE TRIGGER note_bodies_ad AFTER DELETE ON note_bodies BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, title, body) VALUES ('delete', old.note_rowid, old.title, old.body);
END;
CREATE TRIGGER note_bodies_au AFTER UPDATE ON note_bodies BEGIN
    INSERT INTO notes_fts(notes_fts, rowid, title, body) VALUES ('delete', old.note_rowid, old.title, old.body);
    INSERT INTO notes_fts(rowid, title, body) VALUES (new.note_rowid, new.title, new.body);
END;

CREATE TABLE tags (
    note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    tag     TEXT NOT NULL,
    PRIMARY KEY (note_id, tag)
);
CREATE INDEX tags_tag ON tags(tag);

CREATE TABLE assets (
    space    TEXT NOT NULL,
    rel_path TEXT NOT NULL PRIMARY KEY,
    note_id  TEXT,
    size     INTEGER NOT NULL
);

-- Scan bookkeeping (not content): when the last full scan finished.
CREATE TABLE scan_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
