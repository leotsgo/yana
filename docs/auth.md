# Accounts, Sessions, and Spaces

Every `/api` route except the sign-in endpoints needs a verified identity.
There are no default credentials: the first visit to a fresh server shows
the first-run screen, which creates the owner account. Until that happens
the API answers `401` with `setup_required` and serves nothing else.

## Sessions

Passwords are hashed with Argon2id (64 MiB, one pass, four lanes, PHC
format). Sign-in returns a pair of tokens:

- An **access token**, signed with HMAC-SHA256 (secret in
  `.sync/auth_secret`, created on first run). It lives 15 minutes
  (`YANA_ACCESS_TTL`) and is sent as `Authorization: Bearer <token>`.
  Verifying it reads no database.
- A **refresh token**, 32 random bytes, stored **only as its SHA-256** in
  the `sessions` table. It lives 30 days of inactivity
  (`YANA_REFRESH_TTL`). In a browser it is an HttpOnly, SameSite=Strict
  cookie scoped to `/api/auth`; other clients send it in the refresh
  body.

Sessions carry a device label (from the sign-in request or the
User-Agent), are listed at `GET /api/auth/sessions`, and are revoked one
at a time at `DELETE /api/auth/sessions/{id}` or all at once by a password
change. Revoking a session also closes its live WebSocket connections.

## Endpoints

Unauthenticated: `GET /healthz`, `GET /readyz`, `GET /api/auth/state`,
`POST /api/auth/setup`, `POST /api/auth/login`, `POST /api/auth/refresh`,
`POST /api/auth/logout`.

Everything else requires the bearer token, including `GET /ws` (pass the
access token as `?token=`; browsers cannot set headers on a WebSocket).

| Route | Notes |
| --- | --- |
| `POST /api/auth/setup` | `{username, password}` — works once, creates the owner |
| `POST /api/auth/login` | `{username, password, label?}` |
| `POST /api/auth/refresh` | `{refresh_token}` or the cookie |
| `POST /api/auth/logout` | revokes the session the refresh token names |
| `GET /api/auth/sessions` | own sessions, `current` flagged |
| `DELETE /api/auth/sessions/{id}` | revoke own (owner: any) |
| `GET /api/users` | owner only |
| `POST /api/users` | `{username, password}`, owner only |
| `DELETE /api/users/{id}` | owner only, not self |
| `POST /api/users/{id}/password` | `{password}`, self or owner |

Passwords are at least 8 characters. Usernames are 2–32 characters of
letters, digits, dot, underscore, dash, and are unique case-insensitively.
Logins are rate-limited to 10 attempts per minute per username.

## Spaces and sharing

Sharing is modeled as **spaces** — top-level directories — not per-note
ACLs. Sharing a note means moving it between spaces, which on disk is a
`mv`.

A space's membership lives in `<space>/.space.yml`, part of the honest
tree and editable by hand:

```yaml
# YANA/ space membership. Edit by hand if you like; the server reloads it on save.
# user is a username or id; role is owner, editor, or viewer.
name: household
members:
  - user: sam
    role: editor
  - user: 01ARZ3NDEKTSV4RRFFQ69G5FAV
    role: viewer
```

`user` may be a username (friendly for hand edits) or a user id; unknown
references are skipped with a warning until the account exists and the
file changes or a scan runs. Roles:

- **viewer** — read, search, subscribe to live editing, see presence.
  Writes are refused.
- **editor** — also create, edit, move notes within and between spaces
  they can write.
- **owner** — also rewrite the space's membership and remove the space.

The global owner account is implicitly an owner of every space,
including spaces with no `.space.yml` and notes loose in the tree root.

The file is parsed on every full scan and on every filesystem change to
it (fsnotify). Hand-editing `.space.yml` to remove a user severs their
access within one watcher cycle, including open realtime subscriptions:
they receive a `forbidden` error for the note and can no longer
re-subscribe, re-read, or search that space. The REST tree, note reads,
backlinks, history, assets, and both search modes all filter by the same
membership, and a space a caller cannot see answers `404`, not `403`, so
its notes' existence is not disclosed.

### Space routes

| Route | Notes |
| --- | --- |
| `GET /api/spaces` | the caller's spaces |
| `POST /api/spaces` | `{name}` — creates the directory and `.space.yml` with the caller as owner |
| `GET /api/spaces/{space}` | role check; shows the caller's role |
| `PATCH /api/spaces/{space}` | `{name?, members?}` — space owner only; rewrites `.space.yml` |
| `DELETE /api/spaces/{space}` | space owner only; refuses when the space still holds files |

### Realtime relay

The WebSocket handshake verifies the access token and fixes the
connection's author from it; a client cannot claim another identity. Each
`subscribe` does a single lookup of membership and role, and `update` is
accepted only from connections whose subscribe-time role is editor or
better and only for notes they are subscribed to.
