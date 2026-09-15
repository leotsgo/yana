# The editor

The browser client is a Preact app around a CodeMirror 6 editor. Opening a
note opens its editor; there is no separate read view. What follows is what
the client does and which endpoints it leans on.

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
the bar above the editor lists who is on the note.

## Preview

The Preview button (or `Mod+E`) opens a pane beside the editor that
renders the live document through `POST /api/render`. The server renders
it with the same goldmark pipeline the note endpoint uses, so wikilinks,
tags, code blocks, and images look the same in the preview as they do
anywhere else. Rendering runs at most every 220 ms while typing. The
choice is remembered per browser.

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
| `Mod+K` | Command palette: everything above plus rename/move, unresolved links, snapshot, sign out |
| `Mod+Shift+F` or `/` | Focus search |
| `Mod+E` | Toggle the preview |
| `Mod+Z` / `Mod+Shift+Z` | Undo / redo (local edits only) |
| `Mod+F` | Find in the open note |
| `Esc` | Close whatever is open |

The switcher and the palette list every note in the tree and filter as you
type; arrows move, Enter picks.

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
| `POST /api/render` | `{markdown}` → `{html}` for the live preview |

`GET /api/status` now includes `daily` with the effective pattern and
template.

## Building

`web/` builds with esbuild (`npm run build`) into `web/dist`, which the
binary embeds. Bundle names carry a content hash (`assets/app-XXXX.js`)
and `index.html` is rewritten to match, so the long immutable cache
lifetime the server puts on `/assets/` is safe across releases.
