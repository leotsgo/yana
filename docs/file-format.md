# File format

This is the contract between YANA/ and the files on disk. It is short on
purpose. Anything you can do to these files with a text editor, `mv`, `cp`,
`rsync`, or a shell script is allowed, and the server catches up on its next
scan. This document doubles as the reference for agents that write notes
directly.

## Tree

```
<notes root>/
  <space>/
    .space.yml
    <folders...>/<note>.md
    <folders...>/<page>.html
    <folders...>/_assets/<file>
  .trash/
  .sync/
```

- A **space** is a top-level directory. Files loose in the root belong to a
  space with an empty name; put them in a directory.
- **Folders are directories.** Nesting is unlimited within path limits. The
  tree the UI shows is the tree on disk; there is no ordering file.
- Names starting with `.` are ignored everywhere in the tree. `.sync/` and
  `.trash/` are the server's; anything else dotted is yours.
- Symlinks are not followed.

## Notes

A note is a file ending in `.md` or `.markdown` (kind `md`) or `.html` /
`.htm` (kind `html`). Everything else outside `_assets/` is ignored.

### Frontmatter

The first time YANA/ sees a note it writes two keys into a YAML block at the
top of the file:

```yaml
---
id: 01JQ8X4K2M9P7R3T5V6W8Y0Z1A
created: 2026-09-12T14:02:11Z
---
```

- `id` is a ULID. It is assigned once and never changes. Moving or renaming
  the file keeps the id; the server matches by id, not path.
- `created` is the UTC time the id was assigned, RFC 3339.

If the file already has a frontmatter block the two keys are inserted into
it. If it has none, a block is added. In either case the rest of the file
is preserved byte for byte: key order, quoting, comments, indentation, and
the line ending style (`\n` or `\r\n`) all survive. The writer is not a YAML
serializer; it edits lines.

The write is atomic (temp file plus rename in the same directory). A file
whose mtime is younger than `YANA_SCAN_SETTLE_TIME` (default 2s) is skipped
and retried, so a copy in progress is not touched.

Optional keys the server understands:

| Key | Type | Meaning |
|---|---|---|
| `order` | int | Sidebar sort within a folder |
| `trusted` | bool | HTML notes only; render without the sanitizer (still sandboxed — see [html-notes.md](html-notes.md)) |
| `template` | string | Reserved |

Do not add keys beyond these for the application's benefit. Keys you add for
your own reasons are carried through untouched.

### Duplicate ids

If two files carry the same `id` (you ran `cp`), the one at the path the
server already knew keeps it and the other is given a fresh id. If neither
path is known, the first one the scan reaches keeps it. Moving a file
(`mv`) is a rename, not a duplicate; nothing is rewritten.

### Title, tags, preview

- The **title** is the first `# H1`, falling back to the filename without
  extension. For HTML notes, the `<title>` or first `<h1>`.
- **Tags** are inline `#tags` in the body: letters, digits, `_`, `-`, `/`,
  case-folded. Purely numeric tokens (`#1`) and anything inside code are not
  tags. Tags are never stored in frontmatter.
- The **preview** is a short plain-text excerpt of the body after the
  title, with code blocks and markdown punctuation removed.

### Markdown

GitHub Flavored Markdown: tables, task lists, strikethrough, autolinks,
footnotes, fenced code with syntax highlighting, and typographic quotes and
dashes. Raw HTML inside a markdown note is dropped from the rendered output.

### Wikilinks

```
[[Meeting notes]]
[[projects/roadmap|the roadmap]]
```

`[[target]]` and `[[target|display text]]` are parsed and resolve to notes
in the same space. Resolution, backlinks, rename propagation, and the
unresolved-link report are covered in [links.md](links.md). HTML notes use
the same resolution through a `data-wikilink` attribute:

```html
<a data-wikilink="Meeting notes">the meeting</a>
```

### HTML notes

`.html` files are indexed (title, text for search) and appear in the tree.
They render on the content origin in a sandboxed frame, sanitized unless
their frontmatter carries `trusted: true`. Editing is source-only with
save-based last-write-wins; a save that lands on a changed file keeps the
overwritten version as `name.conflict-<ts>.html` beside the note. The
full contract — sandbox, CSP, view tokens, the wikilink attribute — is in
[html-notes.md](html-notes.md).

## Editing files while the server runs

The server watches the tree. You can edit any note with any tool at any time
and the change is merged into the note's document, which is what connected
clients see. From the file's point of view the rules are:

- **Any change is merged, not replaced.** The server diffs the new file
  text against what the file held before and applies that difference to the
  document. Text someone was typing at the same moment survives next to
  yours. The author recorded for your change is `filesystem`.
- **The server writes with rename.** When a document changes, its file is
  rewritten two seconds after the last edit (`YANA_WRITEBACK_IDLE`) as a
  temp file in the same directory, `fsync`ed, then renamed over the
  original. You never read a half-written note. The temp files start with
  `.` and are ignored if one is left behind by a crash.
- **The frontmatter is yours.** The server keeps the block exactly as it
  found it and only rewrites the body below it. Add keys, change `order`,
  reformat: the next write-back carries it through.
- **Empty means "not yet".** Editors that save by truncating and rewriting
  show an empty file for an instant. An empty file where a note had text is
  looked at again after `YANA_SCAN_SETTLE_TIME` before it is believed.
- **`mv` is a move.** The server matches files by `id`, updates the path,
  and keeps the document. Move a note while someone is typing in it and
  their next words are written to the new path.
- **`cp` is a new note.** A second file with the same id is given a fresh
  one (see Duplicate ids).
- **`rm` is a delete, with a grace period.** The note leaves the index
  and its document moves to `.sync/crdt/retired/` for 30 days. Put the file
  back with the same id and the document comes with it.
- **A file without an id is a new note.** It gets one once its mtime is
  older than the settle time, and its content becomes the document.

A write-back reads the file once more right before renaming over it, and
keeps the old file open until the rename is done, so a write that lands in
between is read back from the replaced file and merged rather than lost.

Invalid UTF-8 in a note is replaced with U+FFFD when it enters the document
and the file is rewritten that way on the next write-back.

## Assets

Any file under a directory named `_assets` is an asset — a picture, a PDF, a
spreadsheet, whatever. A note refers to one relatively, as in any markdown
file:

```markdown
![diagram](_assets/diagram.png)
![shared](../_assets/logo.svg)
[manual](_assets/kettle-manual.pdf)
```

The UI rewrites those `src` and `href` attributes to `/api/files/<space>/<path>`.
Only paths inside an `_assets` directory are served, with `nosniff` and a
sandboxing content security policy. Assets do not get ids or frontmatter.

The content type served is a deliberate, small set, not whatever the
extension happens to sniff to: images render inline; PDF and plain text
render inline too; everything else — office documents in particular — is
`application/octet-stream` with a `Content-Disposition: attachment`, so the
browser downloads rather than guesses. The size cap is `YANA_MAX_ASSET_SIZE`
for every asset alike.

In the read view, a link to a PDF renders as a card: its name, its size, its
page count once the scanner has read it, a button that expands an inline
viewer, and a download link. A link to anything else under `_assets` renders
as a card with a download link. The viewer opens on the content origin in a
sandboxed frame — the same policy an HTML note renders under
([html-notes.md](html-notes.md)) — so a malicious PDF cannot reach the app's
origin even if it carries a script. On a phone the card opens the file in
the system viewer instead of expanding anything.

### Attachment text in search

The scanner extracts the text of every PDF under `_assets`, in pure Go, no
external binary, into an FTS index keyed by path and content hash: a file is
re-read only when its hash changes, not on every scan. A PDF over
`YANA_MAX_EXTRACT_SIZE` (20 MB by default) or a scanned image with no text
layer still indexes by file name, so it is still findable, just not by what
it says. Search answers with attachments as their own result kind, snippet
and all, alongside notes, each linking to the note or notes that reference
the file and to the file itself.

An asset no note in its space references shows up on the Data page as
unreferenced; moving it to the trash follows the same `.trash/` convention
as a deleted note ([trash.md](trash.md)) — never a delete outright.

## Space config

`<space>/.space.yml` will hold members and roles when Phase 4 lands. It is
reserved now so that nothing else claims the name. Until then a space is a
directory and nothing more.

## Path rules

Every string that becomes a path goes through one module, which rejects:

- `..` segments, absolute paths, null bytes, control characters
- Windows reserved names (`CON`, `PRN`, `AUX`, `NUL`, `COM1`-`COM9`,
  `LPT1`-`LPT9`), trailing dots and spaces
- names that collide case-insensitively with an existing entry
- symlinks that resolve outside the notes root

Paths are normalized to Unicode NFC. Limits: 255 bytes per name, 1024 per
path, 10 MB per note, 50 MB per asset, 100,000 notes per space (all
configurable). A file that breaks a rule is logged and left alone; the rest
of the scan continues.

## Writing notes from scripts and agents

1. Write the file somewhere in the tree with a `.md` extension.
2. Do not invent an `id`. Leave the frontmatter out, or include only your
   own keys; the server adds `id` and `created` on its next scan.
3. Finish writing before the settle window passes, or write to a temp name
   and `mv` into place.
4. To move or rename, `mv` the file. To delete, `rm` it. Both are picked up
   on the next scan; the id follows the file.

The full agent story — bind-mounting the tree, the per-space
`CONVENTIONS.md` generator, and the MCP endpoint with attributed,
rate-limited writes — is in [agents.md](agents.md).

Rebuilding the index (`yana scan`, or deleting `.sync/index.db` and
restarting) produces the same ids, tree, and search results, because
nothing that matters lives only in the database.
