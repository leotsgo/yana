# Changelog

## Unreleased

- Phase 3 — Realtime sync. Notes are editable in the browser. A relay at
  `GET /ws` moves CRDT updates between the clients editing a note and the
  reconciliation loop; the server still never looks inside a payload. Two
  tabs see each other's keystrokes with a presence bar (name, colour,
  cursor position), edits made offline or across a server restart merge in
  both directions, and every connection is bounded by room, message-size,
  and per-author rate limits. The wire protocol is documented in
  `docs/realtime.md`. The editor is a plain textarea for now; the real
  editor lands with a later phase.
- Phase 2 — Reconciliation. Every note now has a CRDT document that follows
  its file and vice versa. Type into the document and the file is written
  two seconds after you stop; edit the file with anything and the change is
  merged into the document without losing what someone else was typing.
  Kill the process whenever you like. This is the part that eats notes if
  it is wrong, so it has more tests than everything else combined.
- Phase 1 — Read-only core. A `yana` binary that scans a directory of
  markdown, assigns each note an id, indexes it for full-text and regex
  search, renders it, and serves a browser UI for reading. One container,
  one volume. Delete the database and nothing is lost. Nothing is editable
  yet; that is the next several phases.
- Phase 0 — CRDT decision. Y-CRDT on both ends. See `docs/crdt-decision.md`
  for the part that took longer than it should have.
