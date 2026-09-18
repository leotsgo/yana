# Changelog

## Unreleased

- Share one note by link. "Share a link" in a note's menu makes an
  unguessable address on the content origin (`/p/{token}`) that renders
  the note read-only with its pictures, attachments, diagrams and math,
  with no account; the dialog shows the address with Copy, the share
  sheet on a phone, a QR code drawn on the spot, an expiry (a day, a
  week, never) and Revoke. One live link per note, shown again on the
  next visit; a globe marks a shared note in the tree and beside its
  crumbs; the Data page lists every live link with Revoke and Revoke
  all. Wikilinks on the page link only to notes that are public too;
  HTML notes are sanitized whatever their trusted flag; the page is
  `noindex`, `no-store`, cookie-free and rate-limited per link. A
  revoked, expired or never-issued link answers the same `404`;
  deleting the note revokes its link and a restore does not bring it
  back. The token is derived from the link row with
  `.sync/content_secret` and only its hash is stored (migration
  `009_public_links.sql`).
- The app teaches itself. The owner's first sign-in seeds an empty tree
  with a **Start here** note (and the note and picture it references)
  that shows links, pictures, tasks, tags, capture, search, folders,
  history, sharing and agents by using each one, with a line per feature
  for desktop and for phone. Help, in the account menu and the palette,
  now lists "how do I" entries that open the note at the right heading
  and re-create it when it was deleted (`POST /api/guide`); keyboard
  shortcuts follow. The desktop toolbar carries the formatting buttons
  the phone already had (link, image, task, tag, and the rest); typing
  `[[` offers the notes of the space and `#` the tags in use, in the
  editor on every device. Home actions say what they do, an empty space
  in the sidebar offers a first note, and `docs/using.md` is the guide
  for people rather than implementers, and the README is rewritten
  around what the app does, with screenshots. Fixes: a palette opened from
  another palette no longer keeps the previous query; the fuzzy matcher
  no longer misses a query when its boundary preference skips the only
  run that works ("picture" against "Put a picture in a note"); a
  top-level `_assets` directory is no longer listed as a space; the
  editor's find-and-replace panel uses the app's own controls, so its
  buttons are readable in the dark theme and the checkboxes sit on their
  labels.
- The logo. YANA/ has a mark — a sticky note with a slash — and it is
  now the favicon, the install icon on every platform (with opaque
  maskable and apple-touch variants), the mark beside the wordmark in
  the top bar, on the home page and the sign-in screen, and the icon
  and header mark in single-file and site exports. The icon set is
  checked in under `web/icons/`; the build copies it rather than
  rasterising the old slash mark.
- Backup remotes. Settings → Data → Backups lists the git repositories
  the server pushes the history to, each with its own schedule (after
  every commit, hourly, or nightly at an hour), its last push, and the
  newest error's message when a push failed. HTTPS remotes take a
  token, stored encrypted and handed to git through an in-memory
  credential helper so it never lands in a URL, a command line, or a
  log; SSH and bare-path remotes work too, and the image now ships
  `openssh-client`. `YANA_GIT_REMOTE` seeds the list on first start.
  The History block names the newest git error instead of pointing at
  the log. Fix: git commands now run with `safe.directory` set for the
  notes root, so a container running as root over a bind mount owned by
  another user commits instead of failing every window with "dubious
  ownership".
- Phase 17 — Capture and everyday use. New note never asks for a path:
  an untitled note opens with the title selected, and the file follows
  the title on Enter (the path prompt survives in the palette). Capture,
  from the home page, the phone's bottom bar, the top bar and `Alt+C`,
  appends a line to today's note without opening it, with Undo in the
  toast; the share target uses the same path. Pinned notes and folders
  at the top of the sidebar and on the home page, a per-browser
  preference. Tags: `#tags` render as links, chips under the title, a
  `/tags` index and a page per tag, and the switcher matches `#tag`.
  Tree actions from a right-click, the hover `⋯`, or a long press on a
  phone: open, pin, move to a folder, rename, delete for notes; new note
  here, new folder, pin, rename, move, delete for folders. Folders are
  real directories: `POST /api/dirs` makes one (the tree lists empty
  ones), `POST /api/dirs/move` renames or moves one through the note
  move so every inbound link is rewritten, `DELETE /api/dirs` trashes
  the notes and removes the empty shell. Search on a phone is a page
  with recent queries; a result opens in read mode with the match
  scrolled into view and marked. The keyboard shortcuts are one palette
  entry. Server: `GET /api/tags`, `GET /api/tags/{tag}`, tags on tree
  rows, inline tag spans in the render.
- Phase 16 — Settings. `/settings`, from the account menu, the palette
  and the sidebar: a column of sections on a desktop, a list on a
  phone. Account: display name (presence, and the edit author without
  accounts), change password, every signed-in device with sign-out for
  one or all others. People (owner): add, remove, reset a password.
  Spaces and sharing: create, rename, remove; members with viewer,
  editor and owner roles for space owners, your own role otherwise;
  the default space for new notes and for the daily note. Agents
  (owner): the MCP URL with a copy button, keys listed with scope and
  last use, create (the secret shown once, with a client configuration
  to copy) and revoke. Appearance: theme, text size, line width, how
  notes open, hide syntax, sidebar density, all in one localStorage
  module the shell and the editor read. Data: the three exports (moved
  here from the palette), trash retention, git status and Snapshot now,
  index state and counts, ripgrep availability and version. A page an
  account cannot use says what it is for and who can. A viewer sees a
  note without the pencil. Server: `GET /api/spaces/{space}` returns
  the label and, for owners, the member list read from `.space.yml`;
  `GET /api/notes/{id}` carries the caller's role; `/api/status`
  carries `accounts`, `regex_version` and `trash.retention_days`; a
  revoked session's access token is refused from the next request, not
  at expiry; the owner's `GET /api/spaces` lists every space (it listed
  only the root); a space just created or shared is cached and shown in
  the tree at once, empty.
- Phase 15 — Installable and offline. The web client is a PWA: a
  manifest and slash-mark icons install it to a home screen, a service
  worker precaches the shell (never the API) and serves it for every
  route with no network, and a new deploy shows up as a one-line reload
  notice. Each note's CRDT document persists in the browser
  (y-indexeddb), so a note edited offline is there when the app reopens
  and merges both ways; the tree and the last 20 opened notes' renders
  are cached for reading; new note, daily note, and uploads made
  offline queue in order and replay when the connection returns, with
  the offline and queued state in the top bar. A share target appends
  what other apps share to today's note (or a chosen note) through the
  CRDT; on iOS a documented Shortcut does it through the API. Sign-in
  carries over to the installed app via the refresh cookie.
- Phase 14 — Reading mode and mobile editing. A note opens as its
  rendered view, and task boxes in it are live: a tick rewrites the
  `[ ]` through the CRDT, so it shows on every open client and lands in
  the file. Edit is a mode: the pencil, `e`, or a click on the body on a
  desktop; Escape or Done comes back. Split stays on wide screens. The
  mode notes open in is a preference. On a phone the editor gets a
  formatting bar above the keyboard (bold, italic, heading, list, task,
  quote, code, link, image from the camera or the library, undo, redo),
  the shell follows the visual viewport so the caret stays above the
  keys, autocorrect is off, and nothing scrolls sideways. Presence
  chips list other people only. Optional, off by default: hide the
  markdown syntax on lines the caret is not on.
- Phase 13 — The web client works on a phone, and looks like it was
  meant to. One shell, three layouts: a drawer and a bottom bar under
  720px, a collapsible column above 1024px, the in-between for tablets.
  Light and dark themes, following the system or picked from the
  account menu. Icons where there were mono labels; a title at the top
  of every note that is edited in place and renames the file to match;
  path, dates and backlinks in a details drawer; delete in an overflow
  menu instead of beside Preview. The home page shows recent notes and
  two buttons rather than a list of keyboard shortcuts. Empty states
  everywhere a blank column used to be. Leaving a note now flushes the
  last batch of keystrokes before the socket closes.
- Phase 10 — Export and publish. Everything leaves: one note as a
  single self-contained HTML file (images inlined as data URIs,
  stylesheet embedded), a space or subtree as a static site zip — one
  page per note mirroring the real folders, assets beside them,
  navigation from the directory structure, wikilinks as relative hrefs,
  backlinks on every page, and offline search from a prebuilt index and
  a bundled minisearch runtime, working from `file://` or any static
  host — and the tree as a zip of markdown and assets byte-identical to
  disk, which round-trips ids, links, and structure exactly. Exports
  never cross a space boundary the caller cannot see. The palette has
  the three of them.
- Phase 9 — HTML notes. `.html` files now render instead of showing their
  source: on a second origin (port 8081 by default) inside a frame with
  `sandbox="allow-scripts"` and nothing else, behind a CSP with no
  connects, no forms, and images only from the same origin. Notes are
  sanitized before serving unless their frontmatter says `trusted: true`;
  flipping the flag re-sanitizes on the next render. Editing is
  source-only with explicit saves — HTML does not merge — and a save that
  lands on a changed file keeps the overwritten version beside the note
  as `name.conflict-<ts>.html`. Wikilinks work through a `data-wikilink`
  attribute with the same resolution, backlinks, and reports as
  `[[targets]]`.
- Phase 8 — Agent integration. Agents work on the tree two ways. On the
  box, bind-mount the tree and let the agent read and write files; a
  per-space `CONVENTIONS.md` generator (`POST
  /api/spaces/{space}/conventions`) writes the contract — frontmatter,
  wikilinks, `_assets`, folder structure — for it to follow. Off the box,
  an MCP server at `POST /mcp` exposes seven tools (list spaces and
  tree, read, write, append, search, move) over the Streamable HTTP
  transport. Agents authenticate with tokens of their own: minted by the
  owner at `/api/agents`, scoped to named spaces, individually
  revocable, and distinct from user sessions. Every MCP write is a
  document edit authored `agent:<label>`, so it merges with concurrent
  human typing, reaches open clients live, writes back through the
  normal path, and commits to git under the label — where `git revert`
  undoes it in one action. Writes are rate-limited per label
  (`YANA_AGENT_RATE`, 30 a minute by default).
- Phase 6 — The editor. Opening a note opens CodeMirror, bound to the
  note's CRDT document, with a live preview beside it rendered by the same
  code as everywhere else. Undo is yours alone: it never reverts what
  someone else typed. Drop or paste an image and it lands in the note's
  `_assets/` directory with the link inserted; drag a note in the sidebar
  to move it, links and all. `Alt+N` makes a note, `Alt+D` opens today's,
  `Ctrl+P` finds one, `Ctrl+K` does the rest. The client is Preact now;
  the bundle carries a content hash so a new release is not served from
  an old cache. Images in rendered notes load again when accounts are on;
  they had been asking for a header an `<img>` cannot send.
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
