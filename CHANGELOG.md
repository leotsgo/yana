# Changelog

## Unreleased

- Phase 1 — Read-only core. A `yana` binary that scans a directory of
  markdown, assigns each note an id, indexes it for full-text and regex
  search, renders it, and serves a browser UI for reading. One container,
  one volume. Delete the database and nothing is lost. Nothing is editable
  yet; that is the next several phases.
- Phase 0 — CRDT decision. Y-CRDT on both ends. See `docs/crdt-decision.md`
  for the part that took longer than it should have.
