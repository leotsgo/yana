-- Wikilinks between notes. Derived from note bodies like everything else
-- in this database; deleting it and rescanning rebuilds it.

CREATE TABLE links (
    from_id    TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    -- The target note id when the link resolves, NULL when it does not.
    to_id      TEXT,
    raw_target TEXT NOT NULL,
    resolved   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (from_id, raw_target)
);
CREATE INDEX links_to ON links(to_id, resolved);
CREATE INDEX links_unresolved ON links(resolved) WHERE resolved = 0;

-- A note that disappears leaves its inbound links unresolved; the raw
-- targets stay so the report keeps showing them.
CREATE TRIGGER links_target_deleted AFTER DELETE ON notes BEGIN
    UPDATE links SET to_id = NULL, resolved = 0
    WHERE to_id = old.id AND resolved = 1;
END;
