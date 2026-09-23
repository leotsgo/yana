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
| `YANA_CONTENT_LISTEN` | `:8081` | Content origin bind address; `off` disables HTML rendering |
| `YANA_CONTENT_ORIGIN` | derived | Public base URL of the content origin when a proxy maps a subdomain onto it |
| `YANA_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `YANA_CONFIG` | unset | Path to a YAML file |
| `YANA_MAX_NOTE_SIZE` | `10485760` | Largest note indexed, bytes |
| `YANA_MAX_ASSET_SIZE` | `52428800` | Largest asset indexed, bytes |
| `YANA_MAX_EXTRACT_SIZE` | `20971520` | Largest PDF whose text is extracted for search, bytes; bigger ones index by file name only |
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
| `YANA_GIT_REMOTE` | unset | Seeds the backup remotes list on first run (see below), and over an empty notes root is cloned before the first scan |
| `YANA_GIT_PUSH_HOUR` | `2` | Local hour the seeded remote pushes at |
| `YANA_GIT_USER_NAME` | `yana user` | Git identity human edits commit under |
| `YANA_GIT_USER_EMAIL` | `user@yana.local` | Its email |
| `YANA_ACCESS_TTL` | `15m` | Access token lifetime |
| `YANA_REFRESH_TTL` | `720h` | How long a session may go unused |
| `YANA_AGENT_RATE` | `30` | MCP writes per agent label per minute |

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
  how many commits the history holds, the last commit and push times
  (both read from the repository and the remotes table, so they survive
  a restart), how many backup remotes are enabled, and the error count
  since the server started with the newest error's message.

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

### The content origin

HTML notes render on a second listener (`:8081` by default), which must
stay a separate origin from the app — that separation is the sandbox. Two
ways to expose it:

- **Second port** (the default, zero config): publish 8081 the way 8080
  is published. The app derives view URLs from each request's host with
  the content port appended.
- **Subdomain**: route `content.example.com` to the container's 8081 and
  set `YANA_CONTENT_ORIGIN=https://content.example.com`. Caddy:

  ```
  content.example.com {
      reverse_proxy yana:8081
  }
  ```

Do not merge the two origins onto one hostname; the sandbox's guarantees
rest on them being different. `YANA_CONTENT_LISTEN=off` disables HTML
rendering entirely (notes still index and edit as source) and public
links with it.

Public links (`/p/{token}`, see [auth.md](auth.md#public-links)) are
served from this origin too, so it must be reachable by whoever gets a
link — from outside the house, if that is who you send them to. Nothing
on it needs an account: HTML note views open only with a short-lived
token the app mints, and public pages only with a live link. If the app
sits behind the proxy's own auth or a VPN, leave the content origin
outside it, or public links open for nobody. The token that public links
derive from lives in `.sync/content_secret`; losing it turns every
shared link into a `404` (share again for a new address).

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

When `YANA_GIT_REMOTE` is set and the root has never held notes — a
fresh volume, nothing but `.sync/`, `.trash/` and dotfiles — the server
clones that remote instead of initialising an empty repository, so a
rebuilt container comes up with every space, note and id in place. The
clone goes through the same credential handling a push uses. A root that
already holds notes is never cloned over; a clone that fails (the backup
is unreachable) leaves the root empty, is logged clearly, and the server
starts without git history, retrying on the next start.

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

### Backup remotes

Git in the same directory on the same disk protects against bad edits,
not against a dead drive. The owner adds backup remotes under Settings →
Data → Backups: any number of repositories the server pushes `HEAD` to,
each on its own schedule — after every commit, hourly, or nightly at a
chosen hour. A push carries only commits the remote has not seen, so
"after every commit" costs one push per quiet window. Every remote shows
its last push and, when the newest push failed, git's error message;
"Push now" commits what is pending and pushes, "Test" runs `ls-remote`
without pushing. The same surface is `GET/POST /api/git/remotes`,
`PUT/DELETE /api/git/remotes/{id}`, and `POST /api/git/remotes/{id}/push`
and `/test`, owner-only.

Three kinds of remote work:

- **HTTPS with a token** — a private GitHub, Gitea, GitLab, or Forgejo
  repository. Create a token scoped to that one repository with write
  access to its contents (GitHub: a fine-grained personal access token
  with *Contents: read and write*; Gitea: an access token with
  `repository` write) and paste it into the Token field. Any username
  works; the token is what authenticates. The token is encrypted at rest
  with a key in `.sync/git_secret` and is handed to git through an
  in-memory credential helper for each push: it never appears in the
  URL, on a command line, or in a log line. A token pasted into the URL
  (`https://me:token@host/…`) is moved out of it on save.
- **SSH** — `git@github.com:you/notes.git` or `ssh://git@host:port/…`.
  The server needs a key it can read: bind-mount a directory holding
  `id_ed25519` and `known_hosts` at the container user's `~/.ssh`
  (`/root/.ssh` by default), or set `GIT_SSH_COMMAND` to point at one.
  The image ships `openssh-client`; unknown hosts are accepted on first
  connection and pinned after (`StrictHostKeyChecking=accept-new`).
- **A path** — an absolute path to a bare repository (`git init --bare`)
  on a mounted backup disk or network share, seen from inside the
  container.

The server never prompts for credentials: a push that would need to
fails, records the error on the remote, and is retried after ten
minutes by the schedule or at once by "Push now". A rejected
non-fast-forward push is a real signal — something else wrote to the
backup repository — and is reported rather than forced over.

`YANA_GIT_REMOTE`, when set, seeds one remote (nightly at
`YANA_GIT_PUSH_HOUR`) into an empty remotes list on first start; after
that the settings page owns the list and the variable is ignored.

### Restoring from a backup

Losing the volume does not lose the notes: any backup remote can bring
them back, either by itself on a fresh install or on demand.

**A fresh install restores itself.** Start the container over an empty
volume with `YANA_GIT_REMOTE` pointing at the backup and the server
clones it before the first scan (see above): every space, note and id
arrives in place, tree browsable and search working, with no manual git.

**An existing server restores from settings.** Settings → Data →
History gains "Restore from a backup", owner-only. Pick an enabled
remote and the server fetches it and shows a preview — how many commits
and notes it holds, its newest commit, and how it stands against the
local history (identical, ahead, behind, diverged) — before anything is
touched. Confirming means typing the remote's name.

The restore itself always moves by ref, never a merge: the current tree
is committed and tagged `pre-restore/<timestamp>` first, so the
pre-restore state is findable without the reflog, then the branch and
the working tree are reset to the backup's newest commit. Local and
backup histories may share no ancestor — a root that was re-initialised
has an unrelated history — and the reset does not care. Notes keep the
ids in their frontmatter, so links, tasks and public links that
reference them survive; editors open on a changed note converge to the
restored text without a reload; a rescan brings the index, search,
tasks and links in line with the tree.

`.sync/` and `.trash/` are gitignored and are not part of any backup:
accounts, sessions, the auth and content secrets, the sealed remote
credentials and the trash belong to this server. A restore replaces
none of them and signs nobody out. The same surface is `POST
/api/git/remotes/{id}/restore/preview` and `POST
/api/git/remotes/{id}/restore` (body `{"confirm": "the remote's name"}`),
owner-only; a restore is refused for a non-owner account and for a
disabled remote, and an unreachable remote fails before anything is
touched.

**The manual fallback**, when the app is not available to drive it:

```sh
docker stop yana
mv /srv/yana/notes /srv/yana/notes.broken
git clone <backup-url> /srv/yana/notes
mv /srv/yana/notes.broken/.sync /srv/yana/notes/.sync   # keeps accounts, sessions and public links
chown -R $(stat -c %u /srv/yana/notes.broken) /srv/yana/notes
docker start yana
```

Moving `.sync/` back is what keeps the owner account, sessions, agent
tokens and public links; skip it to start over with a clean first-run
setup instead. `notes.broken` is the manual pre-restore state — keep it
until the restore is verified, then delete it.

Remotes are one option. restic or rclone against the notes directory
also see a consistent tree, because every write is a rename:

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
- `content_secret` — signs note views and derives public-link tokens.
  Losing it kills every shared link; the notes are untouched.
- the `users`, `sessions`, `agent_tokens` and `public_links` tables in
  `index.db` — real state, unlike the rest of that database. If you back
  up nothing else, back these up, or accept recreating accounts (spaces'
  `.space.yml` files survive; member ids would need re-pointing) and
  shared links.

Token lifetimes are tunable: `YANA_ACCESS_TTL` (default `15m`) and
`YANA_REFRESH_TTL` (default `720h`, 30 days).

## Agents

Agents reach the tree through the files (bind-mount the space, or the
whole root for a trusted agent on the box) or through the MCP endpoint at
`POST /mcp`, authenticated with a token minted at `POST /api/agents`.
Both paths, including scoping, revocation, rate limits, and git
attribution, are in [agents.md](agents.md).

## Daily note

`YANA_DAILY_PATTERN` (default `journal/{YYYY}/{MM}/{YYYY}-{MM}-{DD}.md`)
says where today's note goes inside a space, and `YANA_DAILY_TEMPLATE`
(default `templates/daily.md`) names a note in that space whose body seeds
it. See [editor.md](editor.md).
