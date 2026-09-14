# Deployment

YANA/ is one binary, one container, one writable directory. There is no
external database, cache, or queue. A reverse proxy in front is your choice
and your responsibility.

## Compose

The repository's `docker-compose.yml` is the reference deployment:

```sh
git clone https://github.com/madeofpendletonwool/yana && cd yana
mkdir notes                 # or point YANA_DATA at an existing folder
cp .env.example .env        # optional; every key has a default
docker compose up -d
```

Open <http://localhost:8080>. Put markdown files under `./notes/<space>/`
and they are indexed on the next scan.

To use a prebuilt image instead of building locally, remove the `build:`
block. Images are published to `ghcr.io/madeofpendletonwool/yana`, tagged
with the commit SHA and `latest` on every merge to `main`, and with the
version on release tags.

`.env` carries two host-side keys compose reads directly (`YANA_PORT`,
`YANA_DATA`) and the `YANA_*` keys that are passed into the container. See
`.env.example` for the full list and defaults.

### Configuration

Environment first, then an optional YAML file named by `YANA_CONFIG`, then
defaults. The YAML uses the same keys in snake_case without the prefix:

```yaml
listen: ":8080"
log_level: info
scan_settle_time: 2s
ripgrep: true
```

| Variable | Default | Meaning |
|---|---|---|
| `YANA_NOTES_ROOT` | `/notes` (container), `~/.yana` (bare metal) | Directory that holds spaces |
| `YANA_LISTEN` | `:8080` | Bind address |
| `YANA_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `YANA_CONFIG` | unset | Path to a YAML file |
| `YANA_MAX_NOTE_SIZE` | `10485760` | Largest note indexed, bytes |
| `YANA_MAX_ASSET_SIZE` | `52428800` | Largest asset indexed, bytes |
| `YANA_MAX_NOTES_PER_SPACE` | `100000` | Cap per space |
| `YANA_SCAN_SETTLE_TIME` | `2s` | Minimum mtime age before a file is assigned an id, or believed empty or gone |
| `YANA_WRITEBACK_IDLE` | `2s` | Pause after the last edit before a document is written to its file |
| `YANA_WATCH_DEBOUNCE` | `200ms` | Window for collapsing bursts of filesystem events |
| `YANA_COMPACT_AFTER` | `500` | Per-note CRDT log length that triggers a snapshot |
| `YANA_CRDT_RETENTION` | `720h` | How long a deleted note's document is kept |
| `YANA_RIPGREP` | `true` | Enable regex search through `rg` |
| `YANA_RIPGREP_TIMEOUT` | `5s` | Bound on one regex search |
| `YANA_WS_MAX_CONNECTIONS` | `256` | Concurrent realtime connections (`GET /ws`) |
| `YANA_WS_MAX_ROOMS_PER_CONN` | `16` | Note rooms one connection may join |
| `YANA_WS_MAX_MESSAGE_BYTES` | `1048576` | Largest inbound WebSocket frame |
| `YANA_WS_USER_RATE` | `1200` | Update and awareness messages per user per minute |
| `YANA_WS_AGENT_RATE` | `300` | Same, per agent author |
| `YANA_WS_PING_INTERVAL` | `30s` | Server ping interval for dead-peer detection |
| `YANA_GIT` | `true` | Keep a git history of the notes root |
| `YANA_GIT_QUIET` | `5m` | How long the tree must be unchanged before the window commits |
| `YANA_GIT_INTERVAL` | `1h` | Bound on how long a continuously edited tree goes uncommitted |
| `YANA_GIT_REMOTE` | unset | Remote pushed nightly; unset disables push |
| `YANA_GIT_PUSH_HOUR` | `2` | Local hour of the nightly push |
| `YANA_GIT_USER_NAME` | `yana user` | Git identity human edits commit under |
| `YANA_GIT_USER_EMAIL` | `user@yana.local` | Its email |

Logs are JSON on stderr, one object per line, with a `component` field and a
`request_id` on HTTP lines. The `reconcile` component logs every write-back,
read-in, and suppressed echo with the note id and the file hashes involved;
`YANA_LOG_LEVEL=debug` adds the echoes. If a note ever looks wrong, those
lines say what the loop saw.

### The volume

```
/notes                     # YANA_NOTES_ROOT; the only state
  <space>/                 # one directory per space
    .space.yml             # members and roles (later phase)
    <folders...>/<note>.md
    <folders...>/_assets/<image>
  .git/                    # history of the tree (rebuilt if deleted)
  .gitignore               # ignores .sync/ and the server's temp files
  .trash/                  # soft-deleted notes (later phase)
  .sync/                   # derived
    index.db               # index, search, and the CRDT edit log
    index.db-wal
    index.db-shm
    crdt/<id>.bin          # one document per note
    crdt/retired/<id>.bin  # documents of deleted notes
    git-state.json         # where the open commit window started
```

Everything you care about is the tree of `.md`, `.html`, and `_assets`
files. `.sync/` is rebuilt from that tree on start. If it ever looks wrong:

```sh
docker compose stop yana
rm -rf notes/.sync
docker compose start yana
```

The result has the same note ids, tree, search results, and text, because
ids and text live in the files, not the database. What you lose is
`.sync/crdt/`: the edit history behind each note. Notes edited only through
the file never had any. Deleting `index.db` alone keeps it.

`.sync/crdt/retired/` holds the document of every note whose file was
deleted, for `YANA_CRDT_RETENTION` (30 days). A file that comes back with
the same id picks its document up again. It also holds edits that were
typed but not yet written when the file disappeared, so an `rm` at the
wrong moment is not the end of them.

### File ownership

The process runs as root inside the container by default so that a bind
mount works without a `chown` step. It does not take your files: when the
scanner writes an `id` into a note, the rewritten file keeps the owner it
had, and `.sync/` is created with the owner of the notes directory so you
can delete it. Only the SQLite files inside `.sync/` belong to root.

Create the notes directory yourself before the first `up` (the compose
example points at `./notes`); if Docker creates it for you it will be owned
by root. Once the directory is yours, you can drop root altogether with
`user: "1000:1000"` in the compose file (the example has it commented out).

### Health

- `GET /healthz` returns 200 as soon as the listener is up.
- `GET /readyz` returns 200 once the first scan has finished and 503 before
  that. Point load balancers and `depends_on: condition: service_healthy` at
  this one. The compose healthcheck already does.
- `GET /api/status` returns the version, note count, whether the index is
  ready, a `sync` object: documents loaded, notes waiting for a write-back,
  counts of write-backs, read-ins and suppressed echoes, and whether the
  filesystem watcher is running; and a `realtime` object: live editing
  connections, rooms, updates relayed and dropped, and slow connections
  closed; and a `git` object: whether the history layer is available,
  commits made, the last commit and push times, and errors.

## Reverse proxy

YANA/ speaks plain HTTP on one port. Terminate TLS in front of it. Live
editing uses WebSockets on the same port, so enable upgrade passthrough.

### Caddy

```
notes.example.com {
    reverse_proxy yana:8080
}
```

### nginx

```nginx
server {
    listen 443 ssl http2;
    server_name notes.example.com;

    location / {
        proxy_pass http://yana:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 1h;
    }
}
```

### Traefik (labels)

```yaml
labels:
  traefik.enable: "true"
  traefik.http.routers.yana.rule: Host(`notes.example.com`)
  traefik.http.routers.yana.entrypoints: websecure
  traefik.http.routers.yana.tls.certresolver: letsencrypt
  traefik.http.services.yana.loadbalancer.server.port: "8080"
```

There is no authentication yet. Until Phase 4 lands, put the proxy's own
auth (basic auth, forward auth, a VPN) in front of it or bind it to a
private interface.

## inotify limits

The server watches the notes tree for edits made outside it. On Linux that
is one inotify watch per directory, so a large tree can run into the kernel
defaults (8192 watches on many distributions). When it does, the log says
so once:

```
inotify watch limit reached; directories beyond it are not watched. Raise fs.inotify.max_user_watches on the host
```

and directories past the limit are not watched: edits in them are picked up
when the note is next opened or on the next full scan, not live. On the
host:

```sh
sudo sysctl fs.inotify.max_user_watches=524288
sudo sysctl fs.inotify.max_user_instances=512
```

Persist in `/etc/sysctl.d/90-yana.conf`. Containers share the host's
inotify limits; setting them inside the container has no effect. The
`watch_dirs` field of `/api/status` shows how many directories are watched.

The watcher is also where the settle time matters: a file that shows up
empty or disappears is looked at again after `YANA_SCAN_SETTLE_TIME` before
the server believes it, because editors that save by truncate-and-write and
tools that move files in two steps both look like data loss for a moment.

## Backups

Back up the notes directory. That is the whole procedure.

```sh
rsync -a --delete --exclude .sync/ /srv/yana/notes/ backup:/srv/yana/notes/
```

`.sync/` can be excluded; it is derived. Losing `.sync/crdt/` costs edit
history, not content, so the exclude stays valid; include it if the
history matters to you. Restoring is copying the directory back and
starting the server.

Snapshots taken while the server is writing are safe: every write to a
note, and to its document, is a temp file and a rename, so a backup sees
either the old file or the new one.

## Git history

The notes root is a git repository. On first start the server runs
`git init` there (when no `.git` exists) and writes a `.gitignore`
covering `.sync/` and its own temp files. The repository is derived state
like the index: delete it and the next start rebuilds it, minus the
history.

Commits happen after the tree has been quiet for `YANA_GIT_QUIET`
(default 5 minutes), or at most `YANA_GIT_INTERVAL` (1 hour) apart during
continuous editing, never per keystroke or per write-back. `POST
/api/git/snapshot` commits now. On a clean shutdown the pending window
commits too.

Each commit's author is derived from the edits in its window. An agent's
edits commit under the agent's label (`claude <agent@local>`); everything
else — browser edits, edits made with any editor, scripts — commits under
`YANA_GIT_USER_NAME`/`YANA_GIT_USER_EMAIL`. A window with edits from more
than one author is split into one commit per author wherever the changed
files allow it. `git log` therefore distinguishes human edits from agent
edits, and reverting an agent commit with `git revert` from the shell
works: the changed files are ordinary external edits, and the server
merges the reverted text into every open client.

The per-note history in the UI is this repository: the revision list,
diffs between any two revisions, and restore. Restoring writes the old
text back as an edit through the live sync layer, not as a stomp over the
file, so other clients converge to it and the restore is itself an
editable, revertible change.

### Pushing to a remote

Set `YANA_GIT_REMOTE` and the server pushes `HEAD` nightly at
`YANA_GIT_PUSH_HOUR`. Use SSH or an embedded credential helper for
authentication; the server never prompts (a push that needs a prompt
fails and is retried the next night).

Git in the same directory on the same disk protects against bad edits,
not against a dead drive. For off-box backup, either point
`YANA_GIT_REMOTE` at a remote on another machine, or skip git remotes
entirely and use restic or rclone against the notes directory; both see a
consistent tree because every write is a rename:

```sh
restic -r sftp:backup:/srv/restic-yana backup /srv/yana/notes
rclone sync /srv/yana/notes remote:notes --exclude .sync/**
```

Either can run from cron or a timer next to the server; the server does
not need to know. A plain `rsync -a --delete --exclude .sync/` also
remains a complete backup (see above).

## Bare metal

```sh
make build                       # web client + static binary in ./yana
YANA_NOTES_ROOT=~/notes ./yana   # or leave it unset for ~/.yana
```

Install `ripgrep` for regex search and `git` for the history layer;
without git the history endpoints answer 501 and nothing commits. The
binary is static (`CGO_ENABLED=0`) and runs on any Linux, macOS, or
Windows host without a runtime. A systemd unit is a `Type=simple` service
with `Environment=YANA_NOTES_ROOT=...` and
`ExecStart=/usr/local/bin/yana`.

## Accounts and sessions

Every `/api` route and the WebSocket require an account. The first visit
to a fresh server shows the first-run screen, which creates the owner
account; there is no default password. Passwords are hashed with
Argon2id; sessions are device-labelled and revocable from
`GET /api/auth/sessions`. Full reference, including `.space.yml` sharing:
[auth.md](auth.md).

Two files under `.sync/` matter to accounts:

- `auth_secret` — signs access tokens. Losing it (or deleting `.sync/`)
  invalidates every access token; clients refresh and carry on. Keep it
  out of backups of the notes tree if you like; it is not content.
- the `users` and `sessions` tables in `index.db` — real state, unlike
  the rest of that database. If you back up nothing else, back these up,
  or accept recreating accounts (spaces' `.space.yml` files survive;
  member ids would need re-pointing).

Token lifetimes are tunable: `YANA_ACCESS_TTL` (default `15m`) and
`YANA_REFRESH_TTL` (default `720h`, 30 days).
