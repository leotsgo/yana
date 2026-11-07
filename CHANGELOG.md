# Changelog

This fork versions itself with [semver](https://semver.org): a release
is a `vX.Y.Z` tag on `main`, and its image is
`ghcr.io/leotsgo/yana:X.Y.Z`. While the major version is 0, a minor
bump marks new features and a patch bump marks fixes. Changes land
under Unreleased until the next tag; see
[docs/deployment.md](docs/deployment.md#releases).

## Unreleased

- Backup remotes work over SSH. Each remote has its own key, made by
  the server: Settings → Data → Backups shows the public key first, to
  add to the repository as a deploy key with write access, then asks for
  the address (`git@host:path`, `ssh://…`, or an absolute path to a bare
  repository). "Test and add" connects before it saves, then pushes
  once; a first push that fails (a key without write access) keeps the
  remote and says so. A remote's Key panel copies the key, makes a new
  one, and shows the host's fingerprint, remembered on first contact in
  `.sync/known_hosts`; a host whose key changed is refused until you
  press "Forget".
- A fresh server can start from a git backup. After the owner account
  is created, if the notes folder holds no notes, the app offers an
  empty notebook or "Restore from a git backup": add the key, enter the
  address, and the server clones and indexes it. The repository becomes
  a remote named `backup`, pushing nightly at 02:00; a failed restore
  puts the folder back empty.
- HTTPS remotes and their tokens are gone, and so are `YANA_GIT_REMOTE`
  and `YANA_GIT_PUSH_HOUR`. Existing HTTPS remotes are removed on
  upgrade; add them again over SSH. Existing SSH remotes keep working
  only after the owner makes a key for them (Key → Make a key) and adds
  it to the repository.

- The Go module is `github.com/leotsgo/yana`, and `docker-compose.yml`
  pulls `ghcr.io/leotsgo/yana`.
- Making, moving, renaming and deleting a note no longer wait on a pass
  over every note of its space: only the links that could name the note
  are recomputed. A deleted note leaves the sidebar at once, and making
  one (the daily note too) is no slower in a large space.
- The native Android app is gone, with its gomobile CRDT bindings
  (`mobile/`), its reader bundle, its CI and Makefile targets, and
  `GET /api/notes`, the flat note list only its offline replica read. The
  web app, installed as a PWA, is the phone client.
- `require('yana').setup { space = 'notes' }` in `~/.config/yana/init.lua`
  makes `yana`, `:Yana` and `_` open that space instead of the list of
  spaces.
- `yana` at a terminal is your own neovim with a yana plugin: folders as
  editable oil-style listings, notes whose `:w` merges, live updates,
  `<leader>sf`/`sg` pickers, one `:q` to leave; shell shortcuts (`yana
  today`, `yana new`, `yana <note>`, `yana find`/`grep`/`recent`) and
  bash/zsh completion (`yana completion`).
- `yana` opens where you left it: the same buffers, the current one and
  the cursor in each.
- `yana` at a terminal browses and edits your notes in neovim, where
  every `:w` merges into the note through the new
  `GET`/`PUT /api/notes/{id}/text`; `yana open <path>` opens one note.
  Without a terminal `yana` still serves; the CLI session moves to
  `~/.config/yana` on every OS.
- `yana login`, then `yana today "text"` from a terminal adds a line to
  today's note, like Capture; `yana whoami` and `yana logout` round it
  out. Behind it, `POST /api/notes/{id}/append` adds text at the end of
  a note for any account that may write to it.
- A Drawing button beside New starts a drawing in the folder new notes
  go to.
- Drawings are live: several people or devices draw on one drawing at
  once, see each other's strokes as they happen and each other's named
  cursors, and saves merge shape by shape instead of making conflict
  copies.
- Offline, Today and New open a note you can write in straight away:
  today's note from the device when it is there, otherwise one made on
  the device and sent, under the same id, when the connection returns.
- Settings → Spaces → Defaults picks the space and folder new notes go
  to and those of the daily note, kept with the notes in
  `.yana/defaults.json` so every device and account follows them; the
  owner sets them, other accounts see them read-only.
- Dragging a note in the graph is smooth again: the note stays under the
  pointer every frame, and the layout no longer slows to half its pace
  while you drag.
- Colour themes, one for light and one for dark (Settings → Appearance):
  Catppuccin Latte, Frappé, Macchiato and Mocha, Gruvbox Light and Dark,
  Tokyo Night and Night Day, Rosé Pine and Dawn, Solarized Light and
  Dark, Nord, Dracula, One Dark, Everforest and Kanagawa beside Yana's
  own. The owner adds more by pasting a theme as JSON (three colours are
  enough); they are kept with the notes in `.yana/themes.json`.
- A graph view of how the notes link together (sidebar, palette): every
  readable note as a dot and every link as a line, laid out by a live
  force simulation, with hubs, clusters and orphans plain to see. Hover
  or tap to light a note and its neighbours, click or tap again to open
  it, drag, pan and zoom. A panel filters by text, shows orphans,
  unresolved links and tags, colours by space or folder and tunes the
  forces, all remembered per browser. `/graph?note=<id>` is a note's
  local graph, also in the note's details under Graph, with a depth
  slider and Open as tab. The page follows edits live.
- With thousands of notes in a space, the app no longer hitches every
  30 seconds (most visible while panning the graph) or when a tab opens:
  the sidebar tree redraws only the rows that changed, and none when a
  refresh finds nothing new.
- The details sheet on a tablet or phone takes taps again: its backdrop
  had covered it, so a tap closed it.
- A page opened from the address bar (a reload, a bookmark) shows that
  page, not the tab that was active before.
- A Tasks button in the top bar, beside New, Capture and Today.
- The tasks, activity and trash pages span the pane like the home page:
  the scrollbar at the edge, the content centered or left as Center the
  text says. A long folder path in the tasks filter no longer stretches
  its select past the page.
- The header above a note takes half the height: the folder and tags on
  one small line, the title with the toolbar beside it and the sync state
  (Live, Saved) under it. A long title is cut short there and shows whole
  while you edit it.

## 0.3.3 — 2026-11-04

- Reveal the open note can be turned off (Settings, Appearance,
  Sidebar). On, as before, a note opened from a link, search, the
  switcher or a tab opens the folders above it in the tree and scrolls
  to it; off, the tree stays as you left it while you follow links.

## 0.3.2 — 2026-11-04

- The home page scrolls across the whole pane, its scrollbar at the
  edge instead of halfway across, and its content follows Center the
  text like a note's body.

## 0.3.1 — 2026-11-04

- Center the text centers only the note's body. The header (folder,
  title, tags) is back against the left edge.

## 0.3.0 — 2026-11-04

- Centered text. The note's column sits in the middle of the page,
  reading and editing alike, with the title lined up above it. Center
  the text in Appearance turns it off. On by default.
- The open mode follows you. Switching a note to read, edit or split
  (the toolbar, `e`, Escape, `Mod+E`) also makes that the mode the next
  note opens in, as the Open notes setting would; a new note still opens
  in the editor without changing it.
- A click on the rendered text no longer opens the editor, so selecting
  text while reading stays in read mode. The pencil and `e` still edit.

## 0.2.2 — 2026-11-04

- A drawing also reopens with the tool that was in hand, and its lock.
  One-off tools (image, eraser, laser) are not remembered; the drawing
  comes back with the tool used before them.

## 0.2.1 — 2026-11-04

- A drawing reopens at the scroll position and zoom it was left at.
  The view is kept per drawing in the browser, not in the file, so
  panning and zooming never save; a first visit still scrolls to fit
  the content.

## 0.2.0 — 2026-11-04

Drawings: Excalidraw canvases as notes.

- Drawing notes. A drawing is a `Name.excalidraw.md` file: the usual
  frontmatter, then the Excalidraw scene as JSON in a fenced
  `excalidraw` block. It is a note of a new kind, `drawing`, so it
  moves, goes to the trash and has history like any other. Search finds
  the text written on a drawing, and `is:drawing` lists only drawings;
  the regex search skips them.
  Index migration 14 allows the new kind; the upgrade is in place and
  nothing is rebuilt.
- A drawing editor in the web client. New drawing is in the command
  palette, and New drawing here in a folder's and a space's menu. The
  canvas follows the app theme, saves about a second after the last
  change, and shows Saved, Saving, Offline or Not saved. Offline, the
  scene is kept as a draft on the device and saved when the connection
  returns. A save against a file that changed keeps the file's version
  as `Name.conflict-<timestamp>.excalidraw.md`. A drawing that cannot be
  read opens read-only with a notice and is never saved over, and
  viewers get a view-only canvas. It works in a phone browser; the
  native Android app does not support drawings.
- Links to and from drawings. `[[Name]]` resolves to `Name.md` first and
  then to `Name.excalidraw.md`; `[[Name.excalidraw]]` names the drawing
  explicitly. Moving a drawing rewrites links to it and keeps the
  `.excalidraw` spelling. An element link written `[[target]]` on the
  canvas opens that note when clicked and counts as a link from the
  drawing, so it shows in the target's backlinks and Linked mentions.
  A canvas link is not rewritten when its target moves.
  `![[Name]]` embeds a drawing as an image in the read view.
- Exports show a one-line placeholder for a drawing, "Excalidraw
  drawing. Open it in yana to see it.", in site, single-note and public
  exports.
- MCP. `read_note` on a drawing returns a text summary of what is written
  on it and the links it holds. `write_note` and `append_note` refuse
  drawings: "drawings are edited in the yana web app, not through this
  tool".
- `yana drawings convert [--apply]` converts Obsidian Excalidraw-plugin
  files (compressed-json or json) to drawings in place, keeping each
  note's id. It is a dry run by default and lists every file as `ok` or
  `FAIL`, with a `rename` line for a file whose name lacks
  `.excalidraw.md`; renames go through the move API or `move_note` so
  links follow. See [docs/deployment.md](docs/deployment.md#converting-obsidian-drawings).
- Size. The editor is a lazy download that is not precached for offline
  use, so a page without a drawing loads exactly what it did. It adds
  about 22 MB to the embedded client (`dist/assets/drawing`): 13 MB of
  fonts, served from the app rather than a CDN, and 8.5 MB of JavaScript
  in chunks fetched on demand. The binary and the image grow by that
  much.

## 0.1.0 — 2026-11-04

The first release of the fork, cut from upstream's `main` as of
2026-10-02 (aliases and embeds, Phase 29) plus the changes below.

- Linked mentions at the foot of every note. The read view (and the
  preview in split) closes with the notes linking here, Obsidian style:
  one collapsible group per linking note with its count, each link's
  line in a card with the `[[link]]` marked; a click opens the note.
  Hidden when nothing links here. The Details panel keeps its list.
- Per-folder sort in the sidebar. A folder's menu has a Sort group —
  title, created and modified, ascending or descending — remembered
  per browser beside the tree's open and closed state. The server's
  order, the PWA cache and exports are unchanged; a folder without a
  choice sorts by title.
- `upload_asset` MCP tool. An agent sends a base64 file (a `data:` URL
  works too) and the note it belongs to; the file lands in the note's
  `_assets/` under a free name, is indexed at once, and the result
  carries the markdown link to put in the note.
- Images are published to `ghcr.io/leotsgo/yana`: `latest` and the
  commit SHA on every push to `main`, `X.Y.Z` on release tags, and a
  branch-named preview for `feat/*` and `development`.

## Before the fork

Upstream's history up to the fork point, as it was written there.

- Templates with variables (Phase 26). Any note in a space's
  `templates/` folder is now a template, the way the daily note's
  always was: its frontmatter is dropped on use and the new note gets
  an id of its own, while the folder stays a normal folder — templates
  are edited like notes, they sync and export with everything else.
  Bodies (and file names) may carry `{{title}}`, `{{date}}`,
  `{{date:YYYY-MM-DD}}`, `{{time}}`, `{{user}}`, `{{space}}`,
  `{{folder}}`, `{{cursor}}` (where the caret lands) and
  `{{prompt:Label}}`, which asks once per distinct label in a small,
  phone-friendly form when the note is made. Unknown variables are left
  as written and fenced code blocks are untouched, so a template can
  document its own variables. Substitution is one pure function in
  `internal/templates`, table-tested, that the daily note now runs
  through too (its legacy `{YYYY}`-style tokens keep working);
  `POST /api/templates/{id}/expand` answers with the body, the prompts,
  the caret position and the title the template's own name suggests.
  "New from template" sits in the command palette, in the tree's folder
  menu (a sheet from the bottom on a phone), and on the home screen,
  and "Insert from template" in the editor's overflow puts the expanded
  body into an open note at the caret. The starter space now ships a
  meeting template and a person template so the folder is there from
  the first run. Android follows in a later phase.

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
  it arrives with the Android phases that follow Phase 26.

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
