# Changelog

## Unreleased

- Android editing affordances (Phase 12n). A markdown note reads by
  default; Edit opens the editor with the scroll carried over from the
  reading and Done returns to the rendered note about where the
  editing left it, and a new note opens straight in the editor with
  the caret at the end. Above the keyboard sits the web's phone
  formatting bar — bold, italic, heading, list, task, quote, code,
  link, image, tag, undo, redo — where every button is the same edit
  the web's button makes over the same selection, committed as one
  document operation and one undo step through a new multi-hunk
  transaction in the CRDT bind package. Typing `[[` offers the notes
  of the space in the switcher's order (a pick finishes the link and
  leaves the caret inside for a `|alias`), `#` offers the tags in
  use, and the title in the app bar renames the note the way the
  web's title does — the heading follows through the document and the
  file moves with its links rewritten, online now or on the next sync
  when offline. "New from template" is not yet available on Android;
  it arrives with Phase 26.

- Android images (Phase 12h). The editor grows an image action — the
  photo picker or the camera — that uploads through the same
  `PUT /api/files` the web editor uses into the note's sibling
  `_assets/` directory and inserts `![alt](_assets/name)` at the
  cursor as one document operation, with a placeholder marker while
  the upload runs. Names follow the web client's convention exactly
  (the same sanitization, the same stamped `photo-` fallback), a
  photo longer than 2048 pixels on its longest edge is downscaled to
  it and re-encoded as JPEG 90 with its EXIF rotation applied, and
  anything smaller goes up untouched. Offline the link is written
  immediately and the upload joins `pending_ops` with its bytes
  staged on disk, replaying on the next sync; a replay that lands
  under another name fixes the note's link through the document. The
  reading view shows a placeholder naming photos whose upload still
  waits and re-renders when they land. The share target accepts
  `ACTION_SEND`/`ACTION_SEND_MULTIPLE` images into a new or existing
  note through the same path, uploading first when the network is
  there so the links carry the names the server chose.

- Android capture speed (Phase 12f). Every fast entry point — the share
  target, two quick-settings tiles, a Glance home-screen widget,
  launcher shortcuts, and Today and Capture on the home screen — makes
  its note in the offline replica first and lets sync carry it to the
  server later, so nothing waits on the network. A note composed
  offline is born with a client-minted ULID (kept on create by sending
  it in the frontmatter, which the server's EnsureID preserves), a
  default path in a per-space inbox folder (`inbox/`, configurable in
  settings beside the daily-note space), and an empty CRDT document
  seeded locally so the editor opens instantly; the create replays
  empty and the typed text rides the document's outbox, which avoids
  the doubled seed text a content-carrying create would converge into.
  Today opens or makes the daily note (`POST /api/notes/daily` online,
  the cached pattern's path offline); Capture appends one line to
  today's note without opening it. The share target
  (`ACTION_SEND`, text and URLs) offers a new note in the inbox or an
  append to a note picked from recents plus replica search, and the
  append path is exported as a reusable intent
  (`com.collinpendleton.yana.APPEND`, note id plus text, a timestamped
  line) for automation apps. The tiles show over the lock screen, so
  tile → typing needs no unlock; the note stays in the replica until
  the network parts can run. A sync keeps notes whose offline create is
  still queued (their server row has not arrived yet), and the replica
  drop-missing computation no longer walks NOT IN batches, which
  deleted every note past the first 500 of a large account. Debug
  builds log each entry point's intent-to-editable-frame time under
  `CapturePerf`.

- The Android app's offline replica. A Room database under `android/`
  mirrors the server's cache tables — spaces, notes, tags, note bodies,
  the flattened folder tree, and a `pending_ops` queue for offline
  create, append and move actions that replay on the next sync.
  Metadata syncs on launch, on pull-to-refresh, and from a six-hourly
  WorkManager job; a note's text is cached as it is opened; the
  replica is wiped when the account or server changes or on sign-out.
  The tree, note list, note reading, and search all work in airplane
  mode. Search answers from an FTS5 trigram index declared with the
  server's own DDL over the bundled SQLite driver, and the query
  grammar and search SQL are line-for-line ports of the server's, so
  the same query returns the same ordered results offline and online —
  pinned by an instrumented test over a 200-note fixture corpus whose
  expectations are the server engine's own output, regenerated and
  verified by a Go test. Screens go through a `NoteRepository` instead
  of Room or REST, and a search screen joins the shell.
  `GET /api/notes` lists the visible notes flat with metadata and tags;
  it is what the replica syncs from. See
  docs/android-offline-search.md for the match semantics and the
  recorded divergences (author:, is:task and has: are server-only
  offline; markdown appends wait for the editor's CRDT transport).

- HTML notes render on Android, under `android/`: a sandboxed WebView
  on the content origin with the same boundary the web's iframe has —
  JavaScript on, no bridge to the app, no file access, mixed content
  blocked, and navigation held to the content origin with other links
  handed to the system browser. The signed view URL is the only
  credential the WebView carries; the app mints a fresh one on every
  open and every save. The source edits in a plain text screen with
  explicit last-write-wins saves that name the conflict copy the server
  parks, and trust shows as a read-only badge that changes on the web.
  Offline, the source shows as text and the note says the rendered view
  needs the server. An instrumented test runs a hostile note on a
  device: its script cannot fetch the API, read the app origin's
  cookies, or navigate the WebView away, and a trusted note's canvas
  animation runs.

- The Android app's first build, under `android/`: Kotlin, Compose and
  Material 3 in the Identity palette and type, light and dark, with a
  launcher icon cut from the wordmark's slash. It connects to a server
  by address, creates the owner account when the server has none, and
  signs in with a password; the token pair lives in
  EncryptedSharedPreferences and refreshes silently, across restarts.
  The session carries the device's name, so it shows on the web's
  Account page, and revoking it there signs the phone out on its next
  request. It browses spaces, each space's folder tree, and notes
  (read-only for now), and settings holds sign-out, the theme, and the
  about page. CI builds, lints and unit-tests it on every pull request
  that touches `android/` and attaches the debug APK.

- The Android CRDT engine. `mobile/crdt` is the bind package over the
  Go Yjs port: create, edit, and diff-apply a note's body; apply and
  encode updates; snapshot and compact; an undo manager scoped to the
  device's own edits, so one person's undo never reverts another's; and
  an update observer that hands Kotlin one incremental update per
  committed transaction, in UTF-16 offsets that map straight onto
  text-field indices. `make android-crdt` builds the AAR into
  `android/crdt/libs` with gomobile, the NDK, and the API floor pinned,
  and CI builds and attaches it on every pull request that touches
  `mobile/`. The Go tests converge with the browser reference
  implementation on shared fixtures.

- Conflict copies, surfaced and resolved. The scanner marks notes whose
  file names say `*.conflict-<ts>.(md|html)` with a `conflict_of`
  column pointing at the surviving note while it exists (migration
  `012_conflicts.sql`; derived data, recomputed every pass). The
  survivor's title bar carries a "1 conflict" chip; the tree nests the
  copy under its note with a mark instead of interleaving it among the
  siblings; the Data page lists every conflict in the caller's spaces
  with its age and size; a copy whose original is gone is a plain note
  with a line under its title saying so. The chip opens a diff of the
  two bodies — the same unified view the history panel shows — with
  three ways out: Keep mine moves the copy to the trash (never deletes),
  Keep theirs writes the copy's text into the note as an edit that open
  clients converge on, and Keep both renames the copy to
  `name (older).ext`. Each resolution is one git commit under the
  caller. The home screen counts waiting conflicts, and a toast says
  when a copy appears for a note the user has open.

- Attachments beyond images: PDFs, spreadsheets, documents — any file —
  upload through the same drag, drop, paste and Attach button (renamed
  from Image, its picker widened) as a picture, landing as a plain link
  when it is not one. In the read view a link into `_assets/` renders as
  a card: name, size, a PDF's page count, a download link, and — for a
  PDF — a button that expands an inline viewer on the content origin,
  sandboxed like an HTML note; on a phone the card opens the system
  viewer instead. The scanner extracts PDF text in pure Go (no external
  binary) into an `attachments` FTS table keyed by path and content
  hash, re-extracting only when the hash changes; a PDF over
  `YANA_MAX_EXTRACT_SIZE` or with no text layer still indexes by file
  name. Search answers with attachments as their own result kind,
  linking to the notes that reference them and to the file. The served
  MIME set is now deliberate — images, PDF and plain text inline,
  everything else (office formats among them) `application/octet-stream`
  with `Content-Disposition: attachment`. The Data page lists assets no
  note references and offers to move them to `.trash/` — never a delete
  (migration `011_attachments.sql`).

- What changed, by whom. The git history becomes a space-level activity
  feed: `GET /api/spaces/{space}/activity` walks the log with a cursor
  (never re-walking a page) and reports each entry's author and kind —
  person, agent, or the filesystem — with the notes it touched, notes
  that no longer exist showing as deleted or renamed rather than as
  links. Consecutive commits by one agent inside a quiet stretch fold
  into a single entry, so an overnight agent session reads as one line;
  a person's quiet-window commits stay one entry each. The `/activity`
  page groups the feed by day, marks what landed since the last visit,
  and filters by kind of author, one author, window and space; the
  home screen carries a one-line "What changed" summary with a count
  since the marker, the palette and Help open the page, and a folder's
  menu narrows the feed to that folder. The Details history rows gain
  an author chip; edits that arrived on the files now commit under a
  `filesystem` identity instead of the human one, so the feed can tell
  them apart.
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
