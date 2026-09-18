# The installable app

The web client is a progressive web app: it installs to a phone or desktop
home screen, opens with no network, keeps the note you were reading and
the edits you make while offline, and accepts shares from other apps.
This page is what it does and where each piece lives.

## Install

`manifest.webmanifest` (written into `web/dist` by `web/build.mjs`) names
the app, its colours (`theme_color` follows the light theme; the page
keeps the `theme-color` meta in step with the dark one at runtime), the
icons at 192 and 512 plus maskable variants, `display: standalone`, and
`start_url: /`. The icons are the sticky-note mark, checked in under
`web/icons/` with the favicon (`.ico` and 16/32 PNG) and the apple-touch
icon; the build copies the set into `dist/`. The maskable variants and
the apple-touch icon are opaque — the mark on the app's background
colour, inside the safe zone — because both Android's adaptive masks
and iOS paint transparency black.

Where the browser offers an install prompt (Chrome and Edge on Android
and desktop, Safari on desktop), the app surfaces it: an Install button
on the home page and in the account menu and command palette, shown only
while the browser has actually made a prompt available. iOS Safari has no
prompt API; there, installing is Share → Add to Home Screen, and the
`apple-touch-icon` and apple meta tags in `index.html` carry the icon and
the standalone window. Sign-in survives the install: the refresh cookie
is HttpOnly and same-origin, so a cold launch of the installed app signs
in on its own — on both platforms, in the standalone window as in a tab.

## Offline

Three stores, one rule each.

**The shell** — `sw.js` (also written by the build, with the hashed
bundle names and a content-hash version baked in) precaches
`index.html`, everything under `assets/` (the app bundle, the mermaid
and KaTeX chunks, KaTeX's stylesheet and fonts), the manifest, and the
icons. Hashed assets are immutable and served cache-first; every navigation is
network-first with the cached shell as the fallback, so a reopened app
with no network boots straight to yesterday's state and a reopened app
with network gets the new deploy on the spot. Nothing under `/api` or
`/ws` is ever cached — the worker does not even see those requests.

**The notes** — every note you open keeps its CRDT document in
IndexedDB (`y-indexeddb`, one database per note). Edits land in the
document whether or not the relay is reachable; the copy on this device
is the document, so a note edited on a plane and closed is there when you
reopen it, and merges with everything the server learned in the meantime
the next time the note connects. Edits made offline are marked in the
editor toolbar (Offline: edits are kept here and merge when the server is
back), and editing is unlocked as soon as the local copy loads — no
waiting for a server that is not there. The note list and the rendered
HTML of the last 20 opened notes are cached the same way, so the tree and
yesterday's reading are on the device too.

**The outbox** — the three operations that need the server before they
can exist (creating a note, opening today's daily note, uploading a
file) queue in IndexedDB when made offline, in the order you made them,
and replay one at a time when the connection returns — on the `online`
event, when the app comes back to the front, or at boot. A queued upload
writes its link into the note now and sends the bytes later; if the
server ends up picking a different name (a collision), the replay says
so. Shares queue the same way. The top bar shows the state plainly:
Offline, N queued, or Sending. Entries the server refuses (a path that
now exists) are dropped with a message rather than retried forever;
entries that fail because there is no session wait for one.

The one honest limitation: images in notes load through the API, so an
offline render shows the text and broken image boxes. The markdown is
all there.

## The share target

The manifest declares a `share_target`: anything shared from another app
arrives at `/share` with the title, text, and URL in the query string.
The page shows the line it will add — `- [title](url)`, the text quoted
under it when there was more than a name — and adds it to today's daily
note through that note's CRDT document, so every open client sees it
land. "Choose a note" picks any note in the tree instead. Offline, the
share queues and lands when the connection returns. Quick capture in
the app (Capture on the home page and the bottom bar) is the same code
path with a line you type; see [editor.md](editor.md).

### On iOS

iOS has no share targets. A Shortcut does the same job through the API.
In the Shortcuts app, build "Add to YANA" with these actions:

1. **Get Contents of URL** — `POST https://your-server/api/auth/login`,
   JSON body `{"username": "…", "password": "…"}`. Expand `API Response`
   and keep `tokens.access_token`.
2. **Get Contents of URL** — `POST https://your-server/api/notes/daily`,
   JSON body `{"space": "", "date": "<Shortcut: Format Date, YYYY-MM-DD>"}`,
   header `Authorization: Bearer <the token from step 1>`. Keep
   `id` and `markdown` from the response.
3. **Text** — the note's markdown from step 2, a blank line, then the
   shared text: `- [Shortcut: Clipboard](Shortcut: Clipboard)` when the
   clipboard is a URL, otherwise `- ` and the clipboard.
4. **Get Contents of URL** — `PUT
   https://your-server/api/notes/<the id>/source`, JSON body
   `{"source": <the text from step 3>, "base_hash": <Shortcut: Hash, the
   content_hash from the note>}`, same Authorization header.

Run it from the share sheet (accepts text and URLs). The credentials and
the server URL live in the shortcut; the login is per-run, so nothing
persistent is handed around. The base hash makes the save collide safely:
if the note changed under you, the server keeps the overwritten copy
beside the note rather than losing either.

## Updates

Deploying a new version needs nothing from the installed clients. The
worker script is served `no-cache`, and the app asks the browser to check
for a new one whenever it comes to the front. A new worker precaches the
new shell, takes over, and the running app shows one line under the top
bar — "A new version is ready. Reload." — which is the whole ceremony.
The next open, with or without the reload, serves the new shell; the
hashed asset names mean nothing stale can ride along.

## Building

`npm run build` in `web/` copies the icons, writes the manifest and `sw.js`
alongside the hashed bundles; `sw.template.js` is the worker's source. In
`npm run watch` the worker is skipped (`__PWA__` is false), so development
against a running server is never shadowed by a cache. The server sends
`sw.js` and `manifest.webmanifest` with `Cache-Control: no-cache`, and
the manifest with `application/manifest+json`.
