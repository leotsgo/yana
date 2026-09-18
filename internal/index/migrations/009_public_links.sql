-- Public links: one note served read-only at an unguessable URL on the
-- content origin, with no account. Real state like sessions and agent
-- tokens, not derived from the tree, and deliberately not frontmatter:
-- the file stays honest and a link does not travel with an export. Only
-- the SHA-256 of the token is stored; the token itself is derived from
-- the row id with the content origin's secret, so the app can show the
-- same URL again without keeping it.

CREATE TABLE public_links (
    id         TEXT PRIMARY KEY,
    note_id    TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    created_by TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER,
    revoked_at INTEGER
);
CREATE INDEX public_links_note ON public_links(note_id, revoked_at);
