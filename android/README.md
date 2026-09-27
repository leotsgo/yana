# YANA/ for Android

The Android client: Kotlin, Jetpack Compose, Material 3. It signs in to a
YANA/ server and browses its spaces, folders and notes, with an offline
replica (Room) that keeps the tree, note reading, and search working in
airplane mode. Markdown notes read rendered — headings, lists, code,
tables, callouts, mermaid diagrams, math, images, tappable wikilinks and
tags, and task boxes that tick — and edit live through the shared
document: a plain text field whose changes become document operations,
with undo scoped to this device and other people's cursors drawn in
their colors. HTML notes render in a sandboxed WebView and edit by
source. A Tasks page gathers every open box across a space (or every
space) grouped by the note it lives in, ticked in place, following
changes live while it is open and shown from the cache with its age when
the network is gone. History reads the same way it does on the web: a
note's revisions with their diffs and restores, the activity feed of
what changed by whom grouped by day, and a point-in-time restore
previewed before it runs — online only, because the history lives on
the server. Capture is the phone's whole point: the share target, two
quick-settings tiles, a home-screen widget, and Today and Capture on
the home screen all make their note in the replica first — a
client-minted ULID, an inbox path, an empty document ready to type in —
and sync carries it to the server later.

## Build

```sh
make android-crdt             # at the repo root: builds the CRDT AAR the app needs
make android-reader           # at the repo root: builds the reader WebView's assets
cd android
./gradlew build              # lint, unit tests, debug and release builds
./gradlew assembleDebug      # just the debug APK
./gradlew installDebug       # onto a running emulator or a connected phone
./gradlew connectedDebugAndroidTest  # the WebView sandbox tests, on a device
```

The build makes one APK per ABI (`arm64-v8a` for phones, `x86_64` for
an Intel emulator, `armeabi-v7a` for old 32-bit phones); `installDebug`
picks the right one. The debug APKs land at
`app/build/outputs/apk/debug/app-<abi>-debug.apk` and install beside a release build (`com.collinpendleton.yana.debug`). Debug builds
sign with the checked-in `app/debug.keystore`, so a CI build installs over
a local one and the other way round. CI
runs `make android-crdt` and `make android-reader` and then
`./gradlew build` on every pull request that touches `android/` and
attaches the debug APKs to the run as `yana-debug-apk`.

The CRDT AAR needs a JDK (17+), the Android SDK with the pinned NDK,
and Go; see [mobile/crdt/README.md](../mobile/crdt/README.md). Without
it the app does not compile — the realtime sync layer calls into it —
so build it first.

The reader assets (the reader page, its script, its stylesheet and the
KaTeX fonts under `app/src/main/assets/reader/`) are a build artifact
too: `make android-reader` runs the web toolchain over
`web/src/android-reader.*` and copies the output in. Without them the
gradle build fails with the instruction to run it.

The instrumented tests (`./gradlew connectedDebugAndroidTest`, emulator
or device attached) check the offline search against the fixture corpus
in `app/src/androidTest/assets/searchfixtures/`; see
[docs/android-offline-search.md](../docs/android-offline-search.md)
for how that corpus is generated and what parity it pins. The reader's
sandbox test (`ReaderSandboxTest`) proves a note's injected markup
cannot run in the reading view.

## The reading view

A markdown note reads the way it reads on the web: the same goldmark
engine the server renders with runs on the device through the bind
package (`Crdt.renderMarkdown`), and the reader page — an APK asset
built from the web's own sources (`web/src/android-reader.*`: the
rich.ts runtime with mermaid and KaTeX bundled, the app.css tokens and
markdown styles) — draws it in a WebView over `WebViewAssetLoader`:
no network from the page, scripts only from the app's assets, file
access off, and no bridge back to the app. Taps (a wikilink, a dashed
link, a tag, a task box) leave as `yana://` navigations the
`WebViewClient` answers; external links go to the system browser.
Wikilinks resolve from the note payload when online, or against the
replica with the server's own resolution rules when not; a dashed link
creates the note at the path the web would (offline, through the
`pending_ops` queue). Task boxes tick through `PATCH /api/tasks`; a
tick offline queues as a pending op and the box reads back ticked.
`_assets/` images load through the app's fetcher with the auth header
and a disk cache, so the token never enters the page.

When Phase 29 (aliases and embeds) lands, `![[note]]` embeds render
through the same path; the render side of the engine is shared, so
nothing new is needed here beyond picking the bundle up.

## The tasks page

Every open `- [ ]` across a space — or across every space the account
belongs to — grouped by the note it lives in, over `GET /api/tasks`.
Filters pick a space, a folder, a tag, and a toggle for tasks completed
in the last 30 days. A row opens its note scrolled to the task's line;
the line's markdown shows styled (bold, italics, code, strikethrough)
the way the note renders it.

Ticking calls `PATCH /api/tasks` with the note id and the body line,
exactly the write the web's tasks page and the in-note checkbox use, so
everyone's list agrees. A tick the server cannot take (the line moved
under it, a viewer's space) reverts with the server's reason; a 409
refetches the listing, which is how the server says the list catches up
on its own. Offline, the tick queues in `pending_ops`, flips the cached
note and the cached listing so it reads back ticked, and replays on
reconnect — the replay of an already-applied tick writes nothing, so
there is no duplicate edit.

While the screen is open the sync engine watches the listed spaces over
its socket (the same `watch`/`chg` frames the web's page uses) and the
listing refetches, debounced, as changes land. The last fetched list is
cached in Room per filter and shows with its age when the network is
gone. The open count feeds the home screen's Tasks row.

## History and activity

The history layer is the git repository under the notes root, the same
one the web reads: online only, because that is where it lives. A
note's History (the clock in its title bar, a screen of its own until
the details sheet lands) lists its revisions over
`GET /api/notes/{id}/history` with who made each — a person, an agent,
or the files — and when. A revision opens its diff
(`GET /api/notes/{id}/history/diff`) as a wrapped, tinted list at phone
width: additions green, removals in the error color, hunks headed;
an older revision diffs against the newest so the diff says what
changed since it stood, the newest against the one before it.
Restore confirms, then writes the old text back as a live edit over
`POST /api/notes/{id}/history/restore` — the open editor converges on
the restored text and the history shows who restored it, the same
revertible edit the web's panel makes.

The activity feed ("What changed", from home or a space's title bar)
reads `GET /api/spaces/{space}/activity` for one space or for every
space the account belongs to, merged by time and grouped by day. An
agent's run of commits is one entry with its span; the notes an entry
touched open at a tap (a deleted note carries its path, untappable).
Opening the feed marks it seen — kept on this device the way the web
keeps it per browser — and the "Since I last looked" window plus the
divider under what is new follow from that marker; the home screen's
What changed row counts what landed since without marking anything.

An entry's restore icon (only where the server would take one: the
space's feed says for that scope, and the tree is the owner's move)
opens a preview over `POST /api/git/restore/preview` that lists every
note that comes back, changes, moves, or goes — the same list the
web's preview shows for the same point — and the confirm runs
`POST /api/git/restore`. What stands now is committed and tagged
first, and what the restore removes goes to the trash, so it comes
back the same way.

Deleted notes (Settings → Data) list over `GET /api/deleted-notes`
with what a restore recovers each from — the trash copy, the retained
edits, or the history — and restoring one brings it back to where it
lived, or a free name beside whatever took its path, opening the note
when it lands. With history off (a build without the git layer) these
screens say so instead of guessing.

Conflict copies — the `name.conflict-<ts>` files the server parks when
two writes meet one path — surface and settle the way the web does. A
note with copies waiting carries a banner above its body
(`conflict_count` on the note, `GET /api/notes/{id}/conflicts` behind
it), Settings → Data lists every copy in the account's spaces with the
count in its row (`GET /api/conflicts`), and both doors open the same
resolve screen: the copy's diff against its survivor
(`GET /api/conflicts/{id}/diff`) as the same wrapped, tinted list the
history screen shows, and the web's three choices over
`POST /api/conflicts/{id}/resolve` — keep this note (the copy moves to
the trash), keep the copy (its text becomes this note), keep both (the
copy is renamed to an ordinary note). Each choice confirms with its
consequence first, lands as one commit, and the screen closes itself
when nothing waits. An HTML save that parks a copy says so beside the
source editor with a Resolve button into the same screen. A copy whose
original is gone is a plain note; the list opens it instead of
resolving it. The tree keeps copies out of its rows entirely (they
nest under their survivor server-side), and search treats them as the
real notes they are, the same as the web.

## Capture

Getting a thought in is the point of the app on a phone, so every fast
entry point makes its note in the replica first and lets sync carry it
to the server later. A note composed offline is born with a ULID this
device mints, lands in the replica with an empty document that is ready
to type into immediately, and a create op joins `pending_ops` carrying
the id in the content's frontmatter — the server's `EnsureID` keeps an
id that is already there, so the note arrives everywhere with the ULID
it was born with. The create replays empty on purpose: the server seeds
a note's document from its file on first sight, so a create that
arrived with text would meet the same text authored again by the
editor's document, doubled. Everything the person wrote rides the
document's own outbox instead, and converges.

The entry points:

- **Home: Today and Capture.** Today opens or makes the daily note —
  online over `POST /api/notes/daily` (so the server's template seeds
  it), offline at the path the cached daily pattern names
  (`GET /api/status` carries the pattern; the server's default stands
  in until it has answered). Capture takes one line onto the end of
  today's note without opening it, as `- text`, the web's capture.
- **The share target** (`ACTION_SEND`, text and URLs): a light activity
  shows what arrived with two ways in — a new note in the inbox folder,
  or the block appended to a note picked from the recent list with
  search over the replica. A new note opens the editor inside the share
  activity, so backing out returns to the app that shared; an append
  confirms and returns on its own.
- **The APPEND intent** (`com.collinpendleton.yana.APPEND`, exported):
  appends a timestamped line (`- HH:mm text`) to a note by id — the
  share target's append, exposed for automation apps. Extras:
  `com.collinpendleton.yana.extra.NOTE_ID` and `.TEXT`.
- **Two quick-settings tiles**: one makes an inbox note and opens the
  editor on it; the other opens the one-line capture. Both show over
  the lock screen — the note and its edits live in the replica, and the
  network parts wait for the unlock.
- **The home-screen widget** (Glance): a New note tap target and the
  last three notes, each a tap from open.
- **Long-press shortcuts** on the launcher icon: new note, capture,
  today.

Appends go through the note's CRDT document — the same path the web's
share target uses — so they merge with open editors and land on every
device. Offline, an append needs the note as a local document (opened
once before), exactly the web client's rule; Today and Capture make
their note locally when it is missing, so they never fail for that.

Settings holds the two knobs: the space Today, Capture, and the tiles
work in (the first space by default), and the inbox folder under each
space (`inbox` by default). The daily-note pattern is read from the
server and cached, never edited here — it is the server's setting.

Debug builds log each entry point's cost: intent receipt to the first
editable frame (`adb logcat -s CapturePerf`).

## Settings

Settings covers what a phone does well and hands the rest to the web
by link. Account holds the password — changing it signs every other
device out; this one stays — and every device signed in with its
label, each revocable (`GET /api/auth/sessions`,
`DELETE /api/auth/sessions/{id}`): the device a revoke ends signs out
on its next request, this one included. Spaces and sharing lists each
space with the role this account holds in it, makes new ones, and for
a space's owner renames the label and adds, changes, or removes
members (`PATCH /api/spaces/{space}` writes the label and the whole
member list at once, the one write the server takes), picking from the
server's accounts by name when the server's owner does it. People (the
server's owner only) lists the accounts, adds one with a starting
password, resets one, or removes it (`/api/users`). Data holds the
deleted-notes and conflicts lists and exports a space as a zip
(`GET /api/spaces/{space}/export/notes.zip`, downloaded with the
session's auth) through the share sheet; the trash arrives with its
own screen (Phase 12o). Help opens the Start here note, asking the
server to make it (`POST /api/guide`) when it is not there yet. Agents
and backups (the owner's), site export, and space conventions stay on
the web: their rows open the signed-in server at its `/settings/`
section.

Put the SDK location in `android/local.properties`
(`sdk.dir=/path/to/Android/sdk`) or set `ANDROID_HOME`. Android Studio
writes the file itself.| Piece | Version | Why |
|---|---|---|
| JDK | 17 | What AGP 9 needs; the same JDK the CRDT AAR builds with |
| Gradle | 9.8.0 (wrapper, checksum pinned) | |
| Android Gradle Plugin | 9.4.1 | Compiles Kotlin itself, so there is no separate Kotlin Android plugin |
| Kotlin | 2.4.20 (Compose and serialization compiler plugins) | |
| `compileSdk` | 37 | Current AndroidX and OkHttp releases require it to compile against |
| `targetSdk` | 36 (Android 16) | The newest release the app has been run against; it moves up after a run on the next release's behaviour changes |
| `minSdk` | 26 (Android 8.0) | Adaptive icons and `java.time` without desugaring, the Keystore features `EncryptedSharedPreferences` relies on, and about 97% of active devices. The CRDT AAR's floor is 24, so it does not constrain this |
| Room | 2.8.5 (runtime + KSP compiler) | The offline replica |
| Bundled SQLite | 2.7.1 (`androidx.sqlite:sqlite-bundled`) | The same SQLite build on every device, which is what carries the FTS5 trigram tokenizer the replica's search index is declared with below the API levels that ship it |
| WorkManager | 2.12.0 | The periodic replica sync |

## Point it at a local server

Run `yana` on your computer, then enter its address on the first
screen:

- **Emulator:** `http://10.0.2.2:8080`. The emulator reaches the host's
  loopback at 10.0.2.2, so a server on `127.0.0.1` is enough.
- **Phone on the same network:** `http://<your computer's LAN IP>:8080`.
  The server must listen on every interface (the default `:8080` does;
  `YANA_LISTEN=127.0.0.1:8080` does not).

A bare host name becomes `https://`. Plain HTTP works because a home
server often has no certificate; the app says when an address uses it.
User-installed CA certificates are trusted, so a server behind a private
CA works once its root is installed on the phone.

A fresh server with no accounts shows the first-run form, which creates
the owner, the same as the web's first visit.

## Sign-in and sessions

Sign-in sends the password once and gets back an access token (15
minutes) and a refresh token (30 days of inactivity). Both, with the
server address and the account, live in `EncryptedSharedPreferences`
under an AES-256 Keystore key, and are left out of backups: the key does
not travel, so a restored copy could not be opened anyway.

Every request carries the access token. One that is about to lapse is
swapped for a new one before the request goes out; a request refused
with `401` trades the refresh token once and retries. The app keeps its
session across restarts without asking for the password again.

The session is labeled with the device (`android · Pixel 8`), so it
appears by name in the session list on the web's Account page. Revoking
it there signs the phone out on its next request: the refresh is
refused, the stored tokens are cleared, and the app returns to sign-in
saying the device was signed out. Sign out in the app's settings revokes
the session on the server first, then forgets it locally, even when the
server cannot be reached.

## The REST client

Retrofit over OkHttp, with kotlinx.serialization for JSON. The token
lifecycle sits in one OkHttp interceptor and one `Authenticator`
(`data/TokenAuth.kt`), which is the whole of silent refresh. The same
OkHttp client carries the realtime WebSocket in the sync phase, so
there is one HTTP stack, one connection pool and one TLS configuration.
Ktor would need an engine plus its auth and content-negotiation plugins
for the same result.

## The offline replica

A Room database (`data/replica/`) mirrors the server's cache tables —
spaces, notes, tags, note bodies — plus the flattened folder tree and a
`pending_ops` queue for offline create/append/move actions. Metadata
syncs on launch, on pull-to-refresh, and from a six-hourly WorkManager
job; a note's text is cached when it is opened. The replica belongs to
the signed-in account: a different account or server wipes it, and so
does signing out.

Search runs against an FTS5 trigram index declared with the server's
own DDL, and the query grammar and SQL are line-for-line ports of the
server's, so the same query over the same notes returns the same
ordered results offline and online. The divergences (author:, is:task,
has: are server-only) are recorded in
[docs/android-offline-search.md](../docs/android-offline-search.md).

Screens never touch Room or REST directly: they go through
`NoteRepository` (`data/NoteRepository.kt`), which answers from the
server when it can be reached and from the replica when it cannot. The
editor and capture features join the shell there.

## Realtime sync

Markdown notes are CRDT documents, the same ones the web edits: one
WebSocket per app speaks the relay protocol in
[docs/realtime.md](../docs/realtime.md) — binary msgpack frames, a
`sub` with the device's state vector, the missing delta back as
`subd`, then streamed `upd` frames — with the same 50ms keystroke
batching and the same reconnect backoff (500ms doubling to 8s, with
jitter) the web client uses. The wire codec lives in `data/rt/Wire.kt`
and its tests pin the bytes against frames the server's own encoder
produced.

Each note's document is one Room blob (`note_crdt.state`, the
compaction of everything applied or authored here) plus an outbox
(`crdt_outbox`) of encoded updates the server has not confirmed.
Confirmation is the application `ping`/`pong`: the server reads frames
in order, so a pong proves it applied every update sent before the
ping, and rows leave the outbox then and only then. A dropped
connection, a dead process, or a day offline all leave the rows in
place; the next connection re-sends them and CRDT idempotence absorbs
the duplicates. A connection dot in the note's title bar says where
things stand — offline, syncing, or live — with no toasts.

## The markdown editor

A note's edit button opens the editor (`ui/editor/`): a plain text
field bound to the document. Each change the field reports diffs to one
replacement — `doc.edit(pos, del, insert)` commits the delete and the
insert as one transaction, so a keystroke is one update, one undo step,
and one outbox row. Document changes from elsewhere — a peer's typing,
an undo, the initial sync — arrive on the `observeText` feed as the
text plus the change's replacement hunks, and the editor maps the
cursor through them instead of resetting it. Undo is scoped to this
device by the bind package's manager; a typing burst (keystrokes no
more than 700ms apart) undoes as one step, and the buttons sit beside
the field with Ctrl-Z / Ctrl-Y for hardware keyboards.

Presence is the awareness protocol the web speaks: the local cursor
broadcasts as a JSON relative position on a 50ms throttle over the
relay's `aw` frames, and other people's cursors and selections draw
over the text in their colour with a name chip. The codec
(`data/rt/Awareness.kt`) is pinned by tests against bytes the web's own
libraries produced. Peers silent for thirty seconds are swept, and
leaving the editor withdraws the cursor.

Every offset is a UTF-16 code unit end to end — the text field's
selections, the document, and the wire agree — so emoji and combining
characters edit cleanly.

The work happens three ways:

- The note screen opens a session and streams while it is on screen.
- Going to the background with unconfirmed edits enqueues an expedited
  WorkManager job that flushes the outbox without the app being open.
- A periodic job (every 30 minutes, network permitting) flushes
  anything left and pulls deltas for recently opened notes, updating
  the replica and its search index.

One platform caveat, recorded rather than hidden: after a literal
force stop, Android will not run any of the app's jobs until it is
opened again. The outbox is durable, so the edits survive and go out
on the next launch (or the next job the system allows); no app can do
more under a force stop.

Verifying the reconnect story by hand, against `yana` on your
computer:

```sh
# With the emulator signed in and a markdown note open:
adb shell am force-stop com.collinpendleton.yana.debug   # restarts cold, edits intact
# On the computer: stop the server mid-typing, watch the dot go
# offline, restart the server, watch the dot return to live and the
# note converge with what the web shows.
# Airplane mode for ten minutes with local edits, then back: both
# sides merge.
```

The JVM suite (`SyncEngineTest`) runs the same scenarios against
MockWebServer speaking the real frames, with a stand-in for the CRDT
engine the AAR provides.

## HTML notes

HTML notes render in a WebView on the content origin, the server's
second listener, with the same boundary the web's iframe has:
JavaScript runs, but there is no bridge to the app, no file access, and
mixed content is blocked. The signed view URL is the only credential
the WebView holds, minted fresh on every open and expiring in five
minutes. Navigation stays on the content origin; other links open in
the system browser. The source edits in a plain text screen with
explicit saves — whole-file, last-write-wins, with the server parking
the diverged version as a conflict copy the save's notice names, with
a Resolve button into the conflict screen. Trust shows as a read-only badge; it changes on the web. An
instrumented test (`NoteWebViewSandboxTest`) runs a hostile note on a
device and checks that its script cannot fetch the API, read the app
origin's cookies, or navigate the WebView off the content origin, and
that a trusted note's canvas animation runs. The minted view URL and
the source saves go through `NoteRepository` like everything else, so
an HTML note offline reads as its cached source until the server
returns.

## Layout

```
app/src/main/java/com/collinpendleton/yana/
  YanaApp.kt, MainActivity.kt   the app's single client, replica, sync engine, and preferences
  data/                          API models, Retrofit interfaces, token refresh, session store
  data/CaptureNotes.kt           the capture engine room: offline compose, Today, appends
  data/CaptureKit.kt             the pure half: ULIDs, share blocks, the daily pattern
  data/replica/                  the Room replica: entities, DAO, tree cache, pending ops, CRDT state
  data/rt/                       the realtime layer: wire codec, socket, engine, workers
  data/search/                   the query grammar and offline search SQL (ports of the server's)
  data/NoteRepository.kt         the one door the screens go through
  ui/Nav.kt                      routes: server → sign-in → spaces → space tree → note; search; tasks; activity; history; settings
  ui/Icons.kt                    the glyphs the chrome shares with the web client (Lucide)
  ui/ConnectionDot.kt            the offline/syncing/live indicator
  ui/screens/                    one file per screen
  ui/activity/                   the feed model, formatting, and the point-in-time restore dialog
  ui/history/                    the unified-diff parser and renderer
  ui/editor/                     the markdown editor over the CRDT
  ui/htmlnote/                   the sandboxed WebView, the source editor, view-token minting
  ui/reader/                     the reading view's WebView and asset fetcher
  ui/theme/                      the Identity palette and type
  capture/                       the fast entry points: share target, capture prompt, APPEND, tiles, widget
  crdt/                          the CRDT engine slot (below)
  fonts/                         licenses for the bundled fonts
```

## The CRDT engine

`crdt/` is where the AAR that `make android-crdt` builds from
`mobile/crdt` lands (`crdt/libs/yana-crdt.aar`; see
[mobile/crdt/README.md](../mobile/crdt/README.md)). The AAR is a build
artifact and not committed. The `:crdt` project publishes it as its
only artifact, so the app's `implementation(project(":crdt"))` picks up
its classes and native libraries when it is there and nothing when it
is not. The realtime sync layer (`data/rt/`) calls into it through the
`RtDocFactory` seam, behind which the JVM tests substitute a fake — so
build the AAR before `./gradlew build`, which CI does.

## Identity

The UI face is Source Sans 3, a humanist sans. Mono (Source Code Pro)
is used only for the `YANA/` wordmark and code. Colours are the web
client's tokens: warm off-white, near-black ink, one amber accent, with
light and dark variants. Settings can follow the system or pin one. The
launcher icon is the slash from the wordmark. Both fonts are under the
SIL Open Font License; the texts are in `fonts/`.
