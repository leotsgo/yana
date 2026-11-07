-- SSH backup remotes. A remote holds the public half of a server-made
-- key (the private half is sealed in secret) instead of a username and
-- token. HTTPS remotes cannot become key-based, so they are dropped.

CREATE TABLE git_remotes_new (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    url           TEXT NOT NULL,
    schedule      TEXT NOT NULL CHECK (schedule IN ('commit', 'hourly', 'nightly')),
    push_hour     INTEGER NOT NULL DEFAULT 2,
    public_key    TEXT NOT NULL DEFAULT '',
    secret        BLOB,
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL,
    pushes        INTEGER NOT NULL DEFAULT 0,
    last_push     INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT NOT NULL DEFAULT '',
    last_error_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO git_remotes_new (id, name, url, schedule, push_hour, public_key, secret, enabled, created_at, pushes, last_push, last_error, last_error_at)
    SELECT id, name, url, schedule, push_hour, '', NULL, enabled, created_at, pushes, last_push, last_error, last_error_at
    FROM git_remotes
    WHERE url NOT LIKE 'http://%' AND url NOT LIKE 'https://%' AND url NOT LIKE 'git://%';
DROP TABLE git_remotes;
ALTER TABLE git_remotes_new RENAME TO git_remotes;
