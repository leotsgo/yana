# Export and publish

Everything is exportable. At any moment you can walk away with the tree
and lose nothing but edit history. There are three ways out, all
downloads from the Data page in settings (`/settings/data`), all scoped
to spaces you are a member of, and all reading the same files the app
edits.

## One note as a single HTML file

`GET /api/notes/{id}/export.html` — "Export as HTML" in the open note's
overflow menu, or any note from the Data page — renders one note as a
self-contained document: images inlined
as data URIs, the stylesheet embedded, no external references. It opens
anywhere: mail it, archive it, open it on a machine that has never heard
of this app. Markdown notes render as they do in the app; HTML notes
carry their trust flag — an untrusted note is sanitized on the way out,
a trusted one runs as written. A note with a mermaid diagram or math
carries the library that draws it (and KaTeX's fonts as data URIs) in
the file, so the diagram is drawn from `file://` as it is in the app;
a note without one carries nothing extra.

## A space as a static site

`GET /api/spaces/{space}/export/site.zip`, with `?path=` to scope the
site to a subtree — a space and "Site" on the Data page. The zip
holds a directory that works from `file://` or any static host, with no
server and no network:

- one HTML page per note, mirroring the real directory structure;
- the assets copied beside the pages, byte for byte, so relative
  references keep resolving;
- a navigation sidebar generated from the directory structure, not from
  anything the app stores;
- resolved wikilinks as relative hrefs between pages; unresolved ones
  render as plain marked text, since a static site cannot create notes;
- a "Linked from" section on every page carrying its backlinks and the
  line each link sits on;
- client-side search: a prebuilt index (`search-index.js`) and a small
  runtime (`search.js`, minisearch) loaded as classic scripts, which is
  what makes them work from `file://` where `fetch` does not;
- `mermaid.js`, and `katex.js` with `katex.css` and `katex-fonts/`,
  when a page in the site holds a diagram or math; each page loads only
  what it uses.

A site built without the web client (a binary with no embedded client)
omits the search page; everything else still exports.

Two notes whose names differ only by extension (`a.md` and `a.html`)
produce the same page name and the export refuses rather than silently
overwriting one with the other.

## The tree as a zip

`GET /api/spaces/{space}/export/notes.zip`, `?path=` for a subtree —
a space and "Zip" on the Data page. Markdown, HTML notes, assets,
and the space's `.space.yml`, byte-identical to disk, no transformation.
Unzip it at a notes root and the space comes back exactly: note ids,
wikilinks, and directory structure are preserved, because they were
never changed. This is the format to reach for when moving between
instances or handing a space to another tool.

## Publishing

The static site doubles as the public-sharing story for a whole space.
The app itself sits behind a reverse proxy on a private network; the
site export is what you can put on any static host. For one note — a
recipe for someone with no account — share a link instead: the note's
menu makes an address on the content origin that renders it read-only
with its pictures, until you revoke it ([auth.md](auth.md#public-links)). Keep in mind what a site carries: every note
in the space, sanitized or trusted exactly as in the app, and the full
text of every note in the search index. Export a subtree
(`?path=public`) when the space holds more than it should publish.
