# HTML notes

HTML notes are for the things markdown cannot draw: dashboards, diagrams,
interactive tables. The cost is that rendering one is rendering code its
author — usually an agent — wrote. Two layers keep that safe, and they
solve different problems.

## The two layers

1. **The sandbox is the boundary.** HTML notes render in an iframe served
   from a second origin (the *content origin*) that the server runs on
   `YANA_CONTENT_LISTEN` (default `:8081`, `off` to disable). The frame
   carries `sandbox="allow-scripts"` and nothing else: no
   `allow-same-origin`, no popups, no top navigation. Scripts run, but
   inside an opaque origin that cannot read the app's cookies or storage,
   cannot touch the app's DOM, and cannot call the API — the content
   origin does not have one. The content origin's CSP finishes the job:
   `connect-src 'none'`, `form-action 'none'`, `img-src 'self'`,
   `default-src 'none'`. A note can draw; it cannot phone home.
2. **The sanitizer keeps notes inert by default.** Unless a note's
   frontmatter carries `trusted: true`, its body passes through
   [bluemonday](https://github.com/microcosm-cc/bluemonday) before it is
   served: scripts, event handlers, forms, and absolute URLs (http,
   https, data, javascript, …) are stripped. Only relative references
   survive, and they resolve to assets beside the note. Flipping
   `trusted` to false re-sanitizes on the very next render; no restart,
   no reload of anything but the note.

Trust is a per-note decision the account holder makes after reading what
the note does. The Trust button on an HTML note writes the frontmatter
flag; it does not turn the sandbox off.

## View tokens

The frame loads `GET /n/{id}?token=…` on the content origin. The client
gets that URL from `GET /api/notes/{id}/view`, which mints a token signed
with a secret that lives only in the server process. The token:

- opens exactly one note (and the assets of that note's space) for five
  minutes,
- is not an account token — it cannot call the API, and the API cannot
  accept it,
- travels in the URL because a sandboxed frame cannot set headers. A
  note that reads its own URL learns nothing that opens anything else.

Assets load through `/t/{token}/f/{path}`; the note page carries a
`<base>` tag pointing there, so relative `src` attributes and CSS
`url()` references resolve to the note's own directory on the content
origin. Only `_assets` directories serve, and only within the note's
space.

## Editing

There is no live editing for HTML notes — HTML does not merge, so there
is no CRDT session. Editing is source-only, in a plain text editor, with
explicit saves (`Ctrl/Cmd-S` or the Save button). Saves are
last-write-wins: the incoming text always lands. When a save does not
build on the file that is currently on disk — someone else, or an agent,
rewrote it since you loaded it — the server first parks the overwritten
version beside the note as `name.conflict-<ts>.html`. The conflict copy
is a real note: it is indexed, searchable, and keeps its own id. It also
shows as a conflict on the note it belongs to — a chip on the title bar,
a nested row in the tree, a diff and three ways to resolve it — as
[the editor's conflicts section](editor.md#conflicts) describes.

## Wikilinks

HTML notes link to other notes through an attribute, not a wiki syntax:

```html
<a data-wikilink="metrics.html">the metrics note</a>
```

Targets resolve exactly like `[[targets]]` in markdown: an exact relative
path from the linking note, an exact path from the space root, then a
unique filename in the space. Resolution feeds the same link index, so
backlinks, the unresolved-link report, and search treat HTML and markdown
notes alike. In the rendered frame a resolved wikilink carries a
server-added `data-wikilink-id`; clicking it posts a message to the app,
which opens the target note. Unresolved links report their target and do
nothing else.

## Authoring notes for agents

- Write the file anywhere in a space with a `.html` extension and no
  `id`; the scanner assigns one.
- Inline `<style>` and `style` attributes survive sanitization; `url()`
  loads do not (the CSP stops them), so keep styles local and reference
  images with `<img src="_assets/…">`.
- Scripts run only in trusted notes. Assume your reader starts
  untrusted: the page must read without them.
- Do not write `data-wikilink-id` yourself; the server rewrites it.

## Android

The Android client renders HTML notes the same way the web does: a
sandboxed WebView pointed at the content origin's `/n/{id}?token=…`,
with JavaScript on and **no bridge back to the app, no file access**,
and mixed content blocked. The signed URL is the only credential the
WebView carries; the app mints a fresh one on every open and every
save, the same as the web client. Navigation holds to the content
origin — other links go to the system browser, and anything that is
not an http(s) page goes nowhere. The source edits in a plain text
screen with explicit saves, last-write-wins, with the conflict copy
named when the server parks one. When the server cannot be reached the
source shows as text and the note says the rendered view needs the
server. Trust appears as a read-only badge and changes on the web.
Nothing about the WebView may weaken the two layers above; if a feature
seems to need that, the feature is wrong.
