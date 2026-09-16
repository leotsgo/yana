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
change. Revoking a session closes its live WebSocket connections and
refuses its access token from the next request on: the running server
keeps the ids of sessions revoked in the last access-token lifetime, so
verification still reads no database and the revoked device is back at
the sign-in screen without waiting for the token to expire. The Account
page in the web client lists a person's own sessions with sign-out for
one or for every other device.

## Endpoints

Unauthenticated: `GET /healthz`, `GET /readyz`, `GET /api/auth/state`,
`POST /api/auth/setup`, `POST /api/auth/login`, `POST /api/auth/refresh`,
`POST /api/auth/logout`.

Everything else requires the bearer token, including `GET /ws` (pass the
access token as `?token=`; browsers cannot set headers on a WebSocket).
`GET /api/files/...` accepts `?token=` for the same reason: an `<img>` in a
rendered note cannot set a header either.

| Route | Notes |
| --- | --- |
| `POST /api/auth/setup` | `{username, password}` — works once, creates the owner |
| `POST /api/auth/login` | `{username, password, label?}` |
| `POST /api/auth/refresh` | `{refresh_token}` or the cookie |
| `POST /api/auth/logout` | revokes the session the refresh token names |
| `GET /api/auth/sessions` | own sessions, `current` flagged |
| `DELETE /api/auth/sessions/{id}` | revoke own (owner: any) |
| `GET /api/users` | owner only; the People page in settings |
| `POST /api/users` | `{username, password}`, owner only |
| `DELETE /api/users/{id}` | owner only, not self |
| `POST /api/users/{id}/password` | `{password}`, self or owner |

Agent tokens are a separate credential for the MCP endpoint: minted,
scoped, and revoked by the owner at `/api/agents`, never interchangeable
with a session. See [agents.md](agents.md).

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
| `GET /api/spaces/{space}` | `{name, label, role}`; for space owners also `members`, each `{user, role}` as written in `.space.yml` plus `id` and `username` when the reference resolves to an account |
| `PATCH /api/spaces/{space}` | `{name, members}` — space owner only; rewrites `.space.yml` and caches it at once |
| `DELETE /api/spaces/{space}` | space owner only; refuses when the space still holds files |

`GET /api/spaces` lists the caller's spaces (the owner's list has every
space, the root included), and `GET /api/tree` shows a space you belong
to even while it holds no notes. `GET /api/notes/{id}` carries `role`,
the caller's role in the note's space, so the client can show a viewer
the note without the pencil.

The Spaces page in settings is these routes as forms: create, rename and
remove a space, and for its owners the member list with a role per
person. The owner adds an account on the People page, shares a space
with it there, and that account signs in and edits — with no file
touched by hand. Hand-editing `.space.yml` still works and shows on the
page at once, since the page reads the file itself.

### Realtime relay

The WebSocket handshake verifies the access token and fixes the
connection's author from it; a client cannot claim another identity. Each
`subscribe` does a single lookup of membership and role, and `update` is
accepted only from connections whose subscribe-time role is editor or
better and only for notes they are subscribed to.
