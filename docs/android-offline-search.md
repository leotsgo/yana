# The Android offline replica and its search

The Android app keeps a local copy of the note set — a Room database
that mirrors the server's own SQLite cache — so the tree, the note list,
note reading, and search all work in airplane mode. This note records
how the replica stays in step with the server and where it does not.

## What the replica holds

The tables are the server's (`internal/index/migrations`), the ones a
client needs: `spaces`, `notes` (id, space, path, title, preview, kind,
created, updated — timestamps in epoch nanoseconds, the unit the server
orders by), `tags`, and `note_bodies`, plus two the server does not
have: `tree_nodes`, the flattened folder tree exactly as the server's
nested response sent it (parent paths carry the nesting, positions the
ordering), and `pending_ops`, the queue of offline actions.

The tasks page adds two more client-side tables: `tasks_cache`, the last
fetched tasks listing per filter scope (a scope is the space, tag,
folder, and open/done toggle joined), and `task_fetches`, each scope's
fetch time, which is what the offline banner reads its age from. The
open count that feeds the home screen's Tasks row sits in
`replica_meta` beside it, with its read time.

A sync pulls three responses on launch, on pull-to-refresh, and from a
six-hourly WorkManager job when the network is up: `GET /api/spaces`,
`GET /api/notes` (the flat list the replica is built from), and
`GET /api/tree`. A sync replaces what changed and drops what vanished;
it never stores a note the server did not return for this account, so
offline search cannot surface a note from a space the account does not
belong to. Changing accounts (or servers) wipes the replica first; so
does signing out.

Note bodies are cached when a note is opened: `markdownBody` /
`stripHtml` in `data/NoteText.kt` derive the searchable text the same
way the server's scanner does (`frontmatter.Parse().Body` for markdown,
`render.StripHTML` for HTML). Notes never opened still carry a
`note_bodies` row with empty text, so their titles stay searchable; the
CRDT sync in the editor phase fills the bodies in.

## The search index

The server's index is an FTS5 table over note titles and bodies with
the trigram tokenizer. Room's annotations only cover FTS4, so the
replica declares the FTS5 table and its four triggers as raw SQL in
`ReplicaDatabase.FTS_DDL` — the server's own DDL, word for word —
created beside Room's tables, and queried through a `@RawQuery` DAO.
The bundled SQLite driver (`androidx.sqlite:sqlite-bundled`) puts the
same SQLite build on every device, which is what carries FTS5 and the
trigram tokenizer below the API levels that ship them.

Match semantics, identical on both sides:

- A term of three runes or more matches as a substring, case-folded for
  ASCII, anywhere in a title or body (that is what trigram buys);
  punctuation is just characters.
- Words shorter than three runes cannot match the trigram index. With no
  usable term the query falls back to a `LIKE` over titles, ordered by
  title; with no text at all it runs as pure filters, newest edit
  first.
- Ranking is `bm25(notes_fts, 4.0, 1.0)` — a title match worth four
  times a body match — and snippets come from
  `snippet(notes_fts, 1, '<mark>', '</mark>', '…', 24)` with the note's
  preview as fallback. Both expressions are copied verbatim into the
  offline query builder (`data/search/LocalSearch.kt`), which is a
  line-for-line port of the server's `internal/index/opsearch.go`.
- The query grammar (operators, `#tags`, quoted phrases, negation,
  demotion of unknown operators to plain text) is a line-for-line port
  of `internal/search/query.go` (`data/search/Query.kt`), including the
  rune-based counting, so a term like two emoji is "too short" on both
  sides.

The parity acceptance (MAD-539) is checked by
`android/app/src/androidTest/.../FixtureSearchParityTest.kt`: a
deterministic corpus of 200 notes checked in as
`android/app/src/androidTest/assets/searchfixtures/fixture.json`, run
through the replica with the same queries the server engine answered.
The expected results in the fixture are the server engine's own output
— regenerate both together with:

```sh
go test ./internal/index -run TestAndroidReplicaFixtures -update
```

The plain `go test ./internal/index` run re-reads the checked-in file
and fails if the engine and the expectations drift apart, so the two
sides stay pinned to each other in CI.

## Where offline search diverges

- `author:`, `is:task`, and `has:` need data the replica does not hold
  (the update log, the task index, attachment text). Offline, the
  repository says so instead of guessing; online, the server answers.
- Attachment results (phrases inside PDFs) are a server-only result
  kind; the offline search returns notes alone.
- Full-text matches cover the notes whose bodies are cached (the ones
  opened, plus everything once the editor's CRDT sync lands). Title
  matches cover every note.
- `has:image` and `has:attachment` would also be partial offline even
  for cached bodies; they are treated as server-only for one behaviour
  instead of two.

## Offline actions

`pending_ops` queues three actions — create a note, append to one, move
one — for the editor and capture features to enqueue; the screens the
shell has today are read-only. Replay runs on every sync, oldest first:
create replays through `POST /api/notes`, move through
`POST /api/notes/{id}/move`, and append through a source save for HTML
notes. A network failure keeps the op for the next pass; a 4xx refusal
drops it, because repeating a refused request cannot start working.
Appending to a markdown note has no REST transport — markdown editing
rides the CRDT websocket the editor phase adds — so those ops wait in
the queue until that lands.
