# Changelog

## Unreleased

- Phase 4 — Accounts, sessions, and spaces. Every route now needs an
  account: the first visit to a fresh server creates the owner account,
  and there are no default credentials. Passwords hash with Argon2id;
  sessions carry a device label, a hashed refresh token, and can be
  revoked (which also closes their live sockets). Sharing is spaces, not
  per-note ACLs: each top-level directory holds a hand-editable
  `.space.yml` naming its members and their roles (owner, editor,
  viewer), which the server reloads on every scan and filesystem change.
  The tree, note reads, search (full-text and regex), assets, history,
  and the realtime relay all stop at space boundaries; a space a member
  cannot see answers as if it did not exist, and removing a user from
  `.space.yml` severs their access — including open subscriptions —
  within one watcher cycle.
- Phase 7 — Git history. The notes root is now a git repository. It is
  initialised on first start and committed after the tree has been quiet
  for five minutes (an hour at most during continuous editing), so a day
  of typing is a small number of commits rather than thousands. Each
  commit names its author: agent edits commit under the agent's label,
  everything else under yours, and mixed windows split where the files
  allow it. Every note has a history panel with revisions, diffs, and
  restore; restoring is an edit, not a file stomp, so it reaches every
  open client. An optional remote pushes nightly. Reverting an agent
  commit with plain `git revert` works too.
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
