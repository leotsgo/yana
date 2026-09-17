-- Git remotes the history layer pushes to as off-box backups. Real state,
-- like agent tokens: the owner configures them in settings and they are
-- not derived from the tree. The credential is sealed with the key in
-- .sync/git_secret, so a copy of this file alone does not hold a token.

CREATE TABLE git_remotes (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    url           TEXT NOT NULL,
    schedule      TEXT NOT NULL CHECK (schedule IN ('commit', 'hourly', 'nightly')),
    push_hour     INTEGER NOT NULL DEFAULT 2,
    username      TEXT NOT NULL DEFAULT '',
    secret        BLOB,
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL,
    pushes        INTEGER NOT NULL DEFAULT 0,
    last_push     INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT NOT NULL DEFAULT '',
    last_error_at INTEGER NOT NULL DEFAULT 0
);
