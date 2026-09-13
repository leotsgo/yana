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
| `YANA_SCAN_SETTLE_TIME` | `2s` | Minimum mtime age before a file is assigned an id |
| `YANA_RIPGREP` | `true` | Enable regex search through `rg` |
| `YANA_RIPGREP_TIMEOUT` | `5s` | Bound on one regex search |

Logs are JSON on stderr, one object per line, with a `component` field and a
`request_id` on HTTP lines.

### The volume

```
/notes                     # YANA_NOTES_ROOT; the only state
  <space>/                 # one directory per space
    .space.yml             # members and roles (later phase)
    <folders...>/<note>.md
    <folders...>/_assets/<image>
  .trash/                  # soft-deleted notes (later phase)
  .sync/                   # derived; safe to delete
    index.db
    index.db-wal
    index.db-shm
```

Everything you care about is the tree of `.md`, `.html`, and `_assets`
files. `.sync/` is rebuilt from that tree on start. If it ever looks wrong:

```sh
docker compose stop yana
rm -rf notes/.sync
docker compose start yana
```

The result has the same note ids, tree, and search results, because ids live
in the files, not the database.

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
- `GET /api/status` returns the version, note count, and whether the index
  is ready.

## Reverse proxy

YANA/ speaks plain HTTP on one port. Terminate TLS in front of it. Later
phases add WebSockets on the same port, so enable upgrade passthrough now and
you will not have to come back.

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

Phase 2 adds a filesystem watcher. It keeps one inotify watch per directory,
so a large tree on Linux can run into the kernel defaults. On the host:

```sh
sudo sysctl fs.inotify.max_user_watches=524288
sudo sysctl fs.inotify.max_user_instances=512
```

Persist in `/etc/sysctl.d/90-yana.conf`. Containers share the host's
inotify limits; setting them inside the container has no effect.

## Backups

Back up the notes directory. That is the whole procedure.

```sh
rsync -a --delete --exclude .sync/ /srv/yana/notes/ backup:/srv/yana/notes/
```

`.sync/` can be excluded; it is derived. When the CRDT log arrives in a
later phase it will live under `.sync/crdt/` and losing it costs edit
history, not content, so the exclude stays valid. Restoring is copying the
directory back and starting the server.

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
