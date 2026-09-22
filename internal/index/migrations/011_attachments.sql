-- Attachments: files under _assets/ that carry text worth searching.
-- Everything here is derived from the tree (hash and extracted text can
-- be rebuilt by a scan), so the table follows the same rule as notes:
-- safe to drop and rebuild.

CREATE TABLE attachments (
    rel_path     TEXT PRIMARY KEY,
    space        TEXT NOT NULL,
    name         TEXT NOT NULL,
    size         INTEGER NOT NULL,
    mtime        INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    pages        INTEGER,          -- PDF page count; NULL for other files
    extracted    INTEGER NOT NULL DEFAULT 0, -- 1 once text was extracted for this hash
    extracted_at INTEGER
);
CREATE INDEX attachments_space ON attachments(space, rel_path);

-- Extracted text lives here so the FTS index can be external-content and
-- the attachments row stays small; name rides along so a scanned PDF
-- with no text layer is still findable by file name.
CREATE TABLE attachment_bodies (
    att_rowid INTEGER PRIMARY KEY,
    name      TEXT NOT NULL,
    body      TEXT NOT NULL
);
CREATE TRIGGER attachments_ad AFTER DELETE ON attachments BEGIN
    DELETE FROM attachment_bodies WHERE att_rowid = old.rowid;
END;

CREATE VIRTUAL TABLE attachments_fts USING fts5(
    name, body,
    content = 'attachment_bodies',
    content_rowid = 'att_rowid',
    tokenize = 'trigram'
);

CREATE TRIGGER attachment_bodies_ai AFTER INSERT ON attachment_bodies BEGIN
    INSERT INTO attachments_fts(rowid, name, body) VALUES (new.att_rowid, new.name, new.body);
END;
CREATE TRIGGER attachment_bodies_ad AFTER DELETE ON attachment_bodies BEGIN
    INSERT INTO attachments_fts(attachments_fts, rowid, name, body) VALUES ('delete', old.att_rowid, old.name, old.body);
END;
CREATE TRIGGER attachment_bodies_au AFTER UPDATE ON attachment_bodies BEGIN
    INSERT INTO attachments_fts(attachments_fts, rowid, name, body) VALUES ('delete', old.att_rowid, old.name, old.body);
    INSERT INTO attachments_fts(rowid, name, body) VALUES (new.att_rowid, new.name, new.body);
END;
