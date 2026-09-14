-- Accounts, sessions, and space membership. The users and sessions
-- tables are real state (they cannot be rebuilt from the tree); spaces
-- and space_members are derived from each space's .space.yml and are
-- refreshed by every scan and by the watcher.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    is_owner      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Only the SHA-256 of the refresh token is stored; a stolen database
    -- cannot be replayed into a login.
    refresh_hash  TEXT NOT NULL UNIQUE,
    label         TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    last_used_at  INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL,
    revoked_at    INTEGER
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE spaces (
    space      TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE space_members (
    space   TEXT NOT NULL,
    user_id TEXT NOT NULL,
    role    TEXT NOT NULL CHECK (role IN ('owner', 'editor', 'viewer')),
    PRIMARY KEY (space, user_id)
);
CREATE INDEX space_members_user ON space_members(user_id);
