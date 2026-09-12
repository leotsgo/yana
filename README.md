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

Early. What runs today is the read-only core: a single `yana` binary that
scans a directory of markdown files, assigns each note an id, indexes it for
full-text (and regex) search, renders it, and serves a browser UI. Live
editing, sync, auth, links, git history, export, and the Android app are
tracked as phases in the build plan and are not built yet.

## Quick start

Requirements: Go 1.26+, Node 20+ (for the web client), optionally `rg`
(ripgrep) for regex search.

```sh
cd web && npm ci && npm run build && cd ..
go build -o yana ./cmd/yana
YANA_NOTES_ROOT=~/notes ./yana
```

Open <http://localhost:8080>. Drop `.md` files into `~/notes/<space>/` and
they appear on the next scan. Each top-level directory is a space.

Configuration is by environment variable (`YANA_NOTES_ROOT`, `YANA_LISTEN`,
`YANA_LOG_LEVEL`, `YANA_RIPGREP`, ...) with an optional YAML file named by
`YANA_CONFIG`. Defaults: notes in `~/.yana` (bare metal) or `/notes`
(container), listening on `:8080`.

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
  .trash/                # soft-deleted notes (later phase)
  .sync/                 # derived state; safe to delete
    index.db
```

A note's frontmatter is minimal and is the only thing YANA/ ever writes into
a file it did not author:

```yaml
---
id: 01JQ8X4K2M9P7R3T5V6W8Y0Z1A   # ULID, assigned on first sight, never changes
created: 2026-09-12T14:02:11Z
---
```

Existing keys are left byte-for-byte as you wrote them.

## Development

```sh
go test ./...                 # server and CRDT spike
cd web && npm run typecheck   # client
```

Layout: `cmd/yana` (entry point), `internal/` (server packages; `pathsafe` is
the only way a string becomes a filesystem path), `web/` (TypeScript client,
embedded into the binary), `spike/crdt/` (Phase 0 CRDT evaluation harness),
`docs/`.

Work happens on short-lived branches off `main`, with Conventional Commit
messages and a pull request per change.

## License

MIT.
