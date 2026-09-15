-- Notes whose files are gone. The content lives on in two places, both
-- swept after the same retention window: a copy of the file under
-- .trash/<space>/... (an in-app delete) and the retired CRDT sidecar at
-- .sync/crdt/retired/<id>.bin (always, when the document was ever
-- loaded). This row carries where the note used to live, which neither
-- of those can say on its own.

CREATE TABLE deleted_notes (
    id         TEXT PRIMARY KEY,
    space      TEXT NOT NULL,
    rel_path   TEXT NOT NULL,
    title      TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('md', 'html')),
    created    INTEGER NOT NULL,
    deleted_at INTEGER NOT NULL,
    trash_path TEXT NOT NULL DEFAULT ''
);
CREATE INDEX deleted_notes_space ON deleted_notes(space, deleted_at);
