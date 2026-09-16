# The editor

The browser client is a Preact app around a CodeMirror 6 editor. A note
opens as its rendered view; the editor is a mode you step into and out
of. What follows is what the client does and which endpoints it leans
on.

## The shell

One component tree, three layouts, picked by width:

| Width | Layout |
|---|---|
| under 720px | Phone. One pane. The sidebar is a drawer; a bar along the bottom has Notes, Search, Today and New. A note reads or edits, never both; while editing, a formatting bar replaces the bottom bar. |
| 720 to 1023px | Tablet. The drawer stays; the top bar has room for New and Today. Read, Edit and Split are all available. |
| 1024px and up | Desktop. The sidebar is a column that collapses from the top-left button; Split puts the editor beside the render. |

The sidebar holds search (full text, or a regular expression with the `.*`
switch when the server has ripgrep), the tree, and the links to the
unresolved-link report and the trash. Search results replace the tree
while a query is typed.

Light and dark themes follow the system unless picked in the account menu
(top right). The choice, the sidebar state, the mode notes open in, the
hide-syntax switch and the recently opened notes are kept per browser in
`localStorage` under `yana.*`; nothing else is stored there.

## Title

The heading at the top of a note is its title and is edited in place.
Committing a new title (Enter, or clicking away) rewrites the note's first
`# ` heading through the CRDT — so it reaches every open client like any
other edit — and renames the file to match, dropping only the characters
a path cannot hold. The rename goes through `POST /api/notes/{id}/move`,
so wikilinks follow. A note whose title comes from its file name (no
heading) is only renamed. Escape puts the old title back.

Path, dates, size and tags live in the Details drawer beside the note,
with the backlinks and the history.

## Read, edit, split

A note opens in read mode: the rendered document, the same HTML the
server produces everywhere else. Wikilinks open the note they point at
(or offer to create it), images load from `_assets/`, footnotes jump.
Task boxes are live: ticking one rewrites its `[ ]` or `[x]` through the
CRDT, so the change shows on every open client and reaches the file on
the next writeback, like any other edit. The renderer stamps each box
with the source line its marker sits on (`data-line`, counted from the
start of the body after any frontmatter); the client checks that line
still holds a marker in the expected state before writing, and re-renders
instead if the text has moved underneath.

Edit is the source editor. Get there with the pencil, by pressing `e`, or
on a desktop by clicking the body of the note anywhere that is not a link
or a box (a drag to select text does not count). Escape, or Done on a
phone, goes back to reading. Split shows the editor and the render side
by side and is for screens 720px and wider; `Mod+E` toggles it.

The mode a note opens in — read, edit, or split — is a preference in the
account menu and the palette. A note you just created always opens in
the editor with the caret ready. A phone never opens in split; it reads.

Presence chips in the toolbar list the other people who have the note
open, one chip per person, in any mode. Your own account in another tab
is not listed.

### On a phone

While editing, a bar of formatting buttons sits above the keyboard:
bold, italic, heading (cycles `#`, `##`, `###`, none), list, task,
quote, code (inline, or a fence around a multi-line selection), link,
image, undo and redo. Image opens the photo picker or the camera and
uploads through the same `_assets/` path as drag and drop. The buttons
take no focus, so the keyboard stays up.

The shell sizes itself to the visual viewport while the keyboard is
open, so the caret and the bar stay above it. Lines wrap, long words
break, and nothing scrolls sideways; tables and code blocks scroll
inside themselves in the render. Autocorrect is off in the editor on a
phone (it rewrites paths, code and link targets); spellcheck and
sentence capitalisation stay on.

### Hide the syntax

An optional switch, off by default, collapses markdown marks on the
lines the caret is not on: `#` before headings, `**` and `_` around
emphasis, backticks around inline code, `~~`, and the brackets and
target of an inline link, so `[text](url)` reads as `text`. Move the
caret onto a line and its marks come back. Block marks (list bullets,
quotes, fences) and wikilinks are always shown. It is in the account
menu as "Hide syntax while editing".

## Editing

Each note's text is a Yjs document bound to the editor through
`y-codemirror.next`. Keystrokes become CRDT updates, batched on a 50 ms
timer and sent over the relay (`docs/realtime.md`); updates from other
clients and from the filesystem arrive the same way and splice into the
editor without touching the local caret.

Undo is scoped to the local client. The undo manager tracks only
transactions whose origin is the local binding; remote updates carry the
origin `remote` and are never on the stack. Undo in one tab does not revert
text typed in another, which the Phase 0 harness verified for the library
and the browser smoke test verifies for the app.

Presence comes from the awareness protocol: each client announces its
user (name, colour) and the editor binding publishes its cursor as a
relative position. Remote carets draw in the editor with the author's name;
the bar above the note lists who else is on it.

## Rendering

The read view and the split preview render the live document through
`POST /api/render` with the same goldmark pipeline the note endpoint
uses, so wikilinks, tags, code blocks, and images look the same
everywhere. The render that came with the note is shown first; the live
one replaces it once the session is up. Rendering runs at most every
220 ms while typing.

## Files: drag, drop, paste

Dropping files onto the editor, or pasting an image, uploads each one to
the note's sibling `_assets/` directory with `PUT /api/files/<path>` and
inserts `![alt](_assets/name.png)` at the drop point (a plain link for
non-images). While the upload runs the document holds a placeholder link,
so other clients see something sensible; the server picks a free name
(`shot.png`, `shot-2.png`, …) rather than overwriting, and the link uses
the name it actually wrote. Uploads are bounded by `YANA_MAX_ASSET_SIZE`
and require write access to the space.

Because an `<img>` cannot send an `Authorization` header, `GET
/api/files/...` also accepts the access token as `?token=`, the same way
`/ws` does.

Dragging a note from the sidebar tree onto a directory, a space heading, or
another note moves it there. That is a `mv` on disk through
`POST /api/notes/{id}/move`, so wikilinks pointing at the note are
rewritten (`docs/links.md`); the toast reports how many. Dragging a note
into the editor inserts a wikilink to it.

## Hotkeys

`Mod` is Command on a Mac and Control elsewhere. Browser-reserved
combinations (`Mod+N`, `Mod+T`, `Mod+W`) are avoided because pages cannot
intercept them.

| Key | Does |
|---|---|
| `Alt+N` | New note: a prompt for the path, then the editor with the caret ready |
| `Alt+D` | Today's daily note (created on first use) |
| `Mod+P` | Quick switcher: fuzzy match on title and path; Enter on no match creates that note |
| `Mod+K` | Command palette: everything above plus rename/move, export, unresolved links, snapshot, theme, sign out |
| `Mod+Shift+F` or `/` | Focus search |
| `E` | Edit the open note |
| `Esc` | Back to reading |
| `Mod+E` | Editor and render side by side (on a phone: in and out of the editor) |
| `Mod+Z` / `Mod+Shift+Z` | Undo / redo (local edits only) |
| `Mod+F` | Find in the open note |
| `Esc` | Close whatever is open |

The switcher and the palette list every note in the tree and filter as you
type; arrows move, Enter picks. The switcher puts recently opened notes
first. The full list of shortcuts is in the account menu.

## Daily note

`Alt+D` (or the palette) opens today's note. The client sends its local
date to `POST /api/notes/daily` with the space to use — the open note's
space, or the first one in the tree — and the server answers with the
existing note or creates it.

Two settings shape it, both paths relative to the space:

| Setting | Default | Meaning |
|---|---|---|
| `YANA_DAILY_PATTERN` | `journal/{YYYY}/{MM}/{YYYY}-{MM}-{DD}.md` | Where the note lives |
| `YANA_DAILY_TEMPLATE` | `templates/daily.md` | A note whose body seeds a new daily note |

`{YYYY}`, `{MM}`, `{DD}` and `{date}` (`YYYY-MM-DD`) expand in both. When
the template note does not exist the new note starts as a heading with the
date. The template's own frontmatter is dropped; the daily note gets an id
of its own like any other file.

## Endpoints added for the editor

| Method and path | Purpose |
|---|---|
| `PUT /api/files/{path}` | Upload one file under an `_assets/` directory; body is the file, response carries the path written |
| `POST /api/notes/daily` | `{space, date}` → today's note, created from the template if missing |
| `POST /api/render` | `{markdown}` → `{html}` for the read view and the preview |

`GET /api/status` now includes `daily` with the effective pattern and
template.

## Building

`web/` builds with esbuild (`npm run build`) into `web/dist`, which the
binary embeds. Bundle names carry a content hash (`assets/app-XXXX.js`)
and `index.html` is rewritten to match, so the long immutable cache
lifetime the server puts on `/assets/` is safe across releases.
