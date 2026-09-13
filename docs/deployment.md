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
  .trash/                  # soft-deleted notes (later phase)
  .sync/                   # derived
    index.db               # index, search, and the CRDT edit log
    index.db-wal
    index.db-shm
    crdt/<id>.bin          # one document per note
    crdt/retired/<id>.bin  # documents of deleted notes
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
  closed.

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

Because the tree is plain files, `git init` inside a space is also a
reasonable backup; Phase 7 does this for you.

## Bare metal

```sh
make build                       # web client + static binary in ./yana
YANA_NOTES_ROOT=~/notes ./yana   # or leave it unset for ~/.yana
```

Install `ripgrep` for regex search. The binary is static (`CGO_ENABLED=0`)
and runs on any Linux, macOS, or Windows host without a runtime. A systemd
unit is a `Type=simple` service with `Environment=YANA_NOTES_ROOT=...` and
`ExecStart=/usr/local/bin/yana`.
