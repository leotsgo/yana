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
regex) search, renders it, and serves a browser UI for reading. Underneath,
each note has a CRDT document that stays in step with its file in both
directions: edits to the document are written to the file, and edits to the
file (any editor, `echo >>`, `rsync`) are merged into the document. The
browser cannot edit yet; the realtime relay that connects it to those
documents is the next phase. Auth, links, git history, export, and the
Android app are tracked as later phases.

## Quick start

```sh
git clone https://github.com/madeofpendletonwool/yana && cd yana
mkdir notes
docker compose up -d
```

Open <http://localhost:8080>. Notes live in `./notes` next to the compose
file; drop `.md` files into `./notes/<space>/` and they appear on the next
scan. Files stay owned by you. Each top-level directory is a space. Copy `.env.example` to `.env` to
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
  .trash/                # soft-deleted notes (later phase)
  .sync/                 # derived state
    index.db             # the index and the CRDT edit log; safe to delete
    crdt/<id>.bin        # each note's document; keep it to keep edit history
    crdt/retired/        # documents of deleted notes, for 30 days
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

Layout: `cmd/yana` (entry point), `internal/` (server packages; `pathsafe` is
the only way a string becomes a filesystem path, `reconcile` keeps documents,
files, and the index in step, `ydoc` wraps the CRDT library), `web/`
(TypeScript client, embedded into the binary), `spike/crdt/` (Phase 0 CRDT
evaluation harness), `docs/` (`deployment.md`, `file-format.md`, and
`crdt-decision.md`, which records which CRDT library each client uses and
why).

The reconciliation tests include a 60 second oscillation check and a
process-kill check; `go test -short ./...` shrinks them.

The CRDT spike's cross-language tests need `node` and `npm ci` in
`spike/crdt/js`; without them those tests skip.

Work happens on short-lived branches off `main`, with Conventional Commit
messages and a pull request per change. CI runs gofmt, vet, build, tests,
the web typecheck, and a container smoke test on every pull request; merges
to `main` publish the image.

## License

MIT.
