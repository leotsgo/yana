-- Agent tokens. Real state like users and sessions: these rows cannot be
-- rebuilt from the tree. A token is the credential an agent presents at
-- /mcp; it is distinct from a user session (no refresh flow, no password,
-- it lives until revoked), it is scoped to named spaces, and only its
-- SHA-256 is stored.

CREATE TABLE agent_tokens (
    id           TEXT PRIMARY KEY,
    label        TEXT NOT NULL,
    -- Only the SHA-256 of the token secret is stored.
    token_hash   TEXT NOT NULL UNIQUE,
    can_write    INTEGER NOT NULL DEFAULT 1,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    revoked_at   INTEGER
);
CREATE INDEX agent_tokens_label ON agent_tokens(label);

-- The spaces a token may see. Empty (no rows) means the token may see
-- nothing; an agent that needs the whole tree is listed in every space.
CREATE TABLE agent_token_spaces (
    token_id TEXT NOT NULL REFERENCES agent_tokens(id) ON DELETE CASCADE,
    space    TEXT NOT NULL,
    PRIMARY KEY (token_id, space)
);
