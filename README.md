# YANA/

YANA/ is a self-hosted notes app. Your notes are markdown files in folders.
You can open them in any editor, move them with `mv`, and back them up with
`cp`. Editing syncs live across every device. Delete the database and nothing
is lost.

None of this is novel. That's sort of the point.

*Yet Another Notes App.*

## Invariants

These hold in every version. If a change would break one, the change is wrong.

1. **The filesystem is the source of truth.** Deleting `.sync/index.db` and
   restarting rebuilds everything by walking the notes tree.
2. **Files are honest.** A note is a `.md` (or `.html`) file at a real path,
   readable and editable with any text editor. Frontmatter is two keys.
3. **Folders are folders.** Real nested directories. No tags-as-folders, no
   hidden ordering files.
4. **Concurrent edits converge.** Two clients plus an external file write to
   the same note end up with the same text, with no conflict dialog.
5. **The server does not understand documents.** It relays opaque CRDT
   updates and persists them. Merge logic lives in the client library.
6. **Everything is exportable.** At any moment you can walk away with the
   tree and lose nothing but edit history.

## Status

Early. What runs today: a single `yana` binary that scans a directory of
markdown files, assigns each note an id, indexes it for full-text (and
regex) search, renders it, and serves a browser UI. Underneath, each note
has a CRDT document that stays in step with its file in both directions:
edits to the document are written to the file, and edits to the file (any
editor, `echo >>`, `rsync`) are merged into the document. Notes are editable
in the browser over a realtime relay (`GET /ws`): two tabs on one note see
each other's keystrokes as they type, with a presence bar showing who else
is there and where their cursor is. Kill the server mid-session or edit
offline for a while and everything merges on reconnect. Wikilinks resolve
between notes, every note shows its backlinks, unresolved links create
their note on click, and moving or renaming a note rewrites every inbound
link on disk and in open clients ([docs/links.md](docs/links.md)). The
notes root is also a git repository: the server commits after the tree has
been quiet, agent edits are distinguishable from human edits by commit
author, and every note has a revision list, diffs, and restore in the UI
([docs/deployment.md](docs/deployment.md), git history). Accounts gate
every route: the first run creates the owner account (no default
credentials), sessions are device-labelled and revocable, and sharing is
modeled as spaces — top-level directories whose `.space.yml` names their
members — with the tree, search, exports, and live subscriptions never
crossing a space boundary a member cannot see
([docs/auth.md](docs/auth.md)). Agents work on the tree two ways: by
writing files (bind-mount the tree, follow the space's
`CONVENTIONS.md`), or through the MCP endpoint at `/mcp` with a
space-scoped, revocable agent token; every agent write is authored, rate
limited, live for open clients, and committed to git under its label
 ([docs/agents.md](docs/agents.md)). The client works on a phone and a
 desktop and has a dark theme. The editor is CodeMirror with a live
 preview, per-user undo, drag-and-drop and paste for images, and drag to
 move notes in the tree. New note, daily note, quick switcher, and command
 palette are one key away ([docs/editor.md](docs/editor.md)). HTML notes
 render on a second origin in a sandboxed frame — sanitized by default,
 runnable as written only after you mark the note trusted — with
 source-only editing that keeps a `name.conflict-<ts>.html` copy when
 saves collide ([docs/html-notes.md](docs/html-notes.md)). Deleting a
  note is soft: the file moves to `.trash/` and its edit history is
  retained, both for a 30-day window; the trash lists every deleted note
  with its original path, restore returns it (a note `rm`'d from a shell
  comes back from its history with everything intact), and emptying the
  trash is the only permanent destruction
  ([docs/trash.md](docs/trash.md)). Everything exports: one note as a
  self-contained HTML file, a space or subtree as a static site with
  navigation, relative wikilinks, backlinks, and offline search, and the
  whole tree as a byte-identical zip that round-trips ids, links, and
  structure exactly ([docs/export.md](docs/export.md)).
  The Android app is tracked as a later phase.

## Quick start

```sh
git clone https://github.com/madeofpendletonwool/yana && cd yana
mkdir notes
docker compose up -d
```

Open <http://localhost:8080>. The first visit creates the owner account;
there are no default credentials. Notes live in `./notes` next to the compose
file; drop `.md` files into `./notes/<space>/` and they appear on the next
scan. Files stay owned by you. Each top-level directory is a space; share
it by listing members in its `.space.yml` ([docs/auth.md](docs/auth.md)).
Copy `.env.example` to `.env` to
change the port or point the volume at a folder of markdown you already have.

Prebuilt images: `ghcr.io/madeofpendletonwool/yana` (`latest`, or a commit
SHA).

Without a container, with Go 1.26+ and Node 20+:

```sh
make build                       # web client + static binary
YANA_NOTES_ROOT=~/notes ./yana   # default is ~/.yana
```

Install `rg` (ripgrep) for regex search; without it the rest still works.

Configuration is by environment variable (`YANA_NOTES_ROOT`, `YANA_LISTEN`,
`YANA_LOG_LEVEL`, `YANA_RIPGREP`, ...) with an optional YAML file named by
`YANA_CONFIG`. `.env.example` lists every key with its default;
[docs/deployment.md](docs/deployment.md) covers reverse proxies, backups,
and the volume layout.

```sh
./yana scan      # rebuild the index once and exit
./yana version
```

## On disk

```
<notes root>/
  <space>/
    .space.yml           # members and roles (later phase)
    <folders...>/<note>.md
    <folders...>/_assets/<image>
  .trash/                # soft-deleted notes, in their own structure
  .sync/                 # derived state
    index.db             # the index and the CRDT edit log; safe to delete
    crdt/<id>.bin        # each note's document; keep it to keep edit history
    crdt/retired/        # documents of deleted notes, for the retention window
```

A note's frontmatter is minimal and is the only thing YANA/ ever writes into
a file it did not author:

```yaml
---
id: 01JQ8X4K2M9P7R3T5V6W8Y0Z1A   # ULID, assigned on first sight, never changes
created: 2026-09-12T14:02:11Z
---
```

Existing keys are left byte-for-byte as you wrote them. The full contract,
including wikilinks, assets, and what scripts and agents may write, is in
[docs/file-format.md](docs/file-format.md).

## Development

```sh
make web        # build the browser client into web/dist
make build      # embed it and build ./yana
make test       # go test ./... and the web typecheck
make lint       # gofmt and go vet
make docker     # build the image locally
```

Layout: `cmd/yana` (entry point), `internal/` (server packages; `pathsafe`
is the only way a string becomes a filesystem path, `reconcile` keeps documents,
files, and the index in step, `rt` is the realtime relay, `mcp` is the agent
tool endpoint, `ydoc` wraps the CRDT library), `web/` (Preact and CodeMirror
client, embedded into the binary), `spike/crdt/` (Phase 0 CRDT evaluation
harness), `docs/` (`deployment.md`, `file-format.md`, `agents.md`,
`realtime.md` for the wire protocol, `editor.md` for the client,
`html-notes.md` for the sandbox and trust model, `trash.md` for deletion
and recovery, `export.md` for the export formats, and `crdt-decision.md`,
which records which CRDT library each client uses and why).

The reconciliation tests include a 60 second oscillation check and a
process-kill check; the relay tests include a server-restart convergence
check and a 20-connection load check; `go test -short ./...` shrinks the
former.

The CRDT spike's cross-language tests need `node` and `npm ci` in
`spike/crdt/js`; without them those tests skip.

Work happens on short-lived branches off `main`, with Conventional Commit
messages and a pull request per change. CI runs gofmt, vet, build, tests,
the web typecheck, and a container smoke test on every pull request; merges
to `main` publish the image.

## License

MIT.
