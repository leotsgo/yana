# Links

Wikilinks turn a folder of notes into a wiki. `[[target]]` links to a note,
`[[target|display text]]` links and shows something else, and `![[target]]`
embeds a note's body inside another (see [Embeds](#embeds)).

```
[[Meeting notes]]
[[projects/roadmap|the roadmap]]
[[../archive/2025.md]]
```

## Resolution

A target resolves within the space of the note holding it (the top-level
directory; a space is the sharing boundary, so links never reach into
another one). The order:

1. **Exact relative path.** The target joins onto the linking note's
   directory, so `[[roadmap]]` in `projects/notes.md` means
   `projects/roadmap.md`. `..` segments work as expected.
2. **Exact path from the space root.** `[[projects/roadmap]]` resolves from
   the top of the space no matter where the linking note sits.
3. **Unique filename.** A bare filename that matches exactly one note in
   the space resolves to it; two notes with the same filename leave the
   link unresolved rather than guessing.
4. **Unique alias.** A bare name that exactly one note in the space claims
   in its `aliases` list resolves to it; two notes claiming the same alias
   leave the link unresolved, and the report page names both (see
   [Unresolved links](#unresolved-links)).
5. Otherwise the link is **unresolved**.

A target without an extension is read as `.md`. HTML notes need their
extension spelled out: `[[dashboard.html]]`.

Resolution runs when the index runs, over the note bodies the index holds.
The `links` table is derived data like everything else in the index
database: delete it and rescan, and it comes back.

## Aliases

A note answers to more names than its path. The frontmatter holds them:

```yaml
---
aliases: [Mom, Margaret]
```

The inline list is the form the server reads, in the note's own frontmatter
([file-format.md](file-format.md)). An alias is matched exactly as
written, after the three path steps, so a real filename always wins over
an alias and a path-shaped target never hits one. `[[` completion offers
aliases as `Mom → Margaret` and inserts the alias; the rendered link shows
the alias text as typed.

Renaming or moving a note keeps its aliases — they travel with the note's
id — and a link written through an alias is left as written: it still
resolves. Editing the alias list re-resolves the space's links on the next
scan.

## Embeds

`![[target]]` renders the target's body inline in the read view, once,
under a small header linking to the note. This is the whole of
transclusion: no recursion (an embed inside an embedded body renders as a
link), no partial selectors, no headings-only form. A note embedding
itself renders a link. An unresolved embed is the create affordance, like
an unresolved link. Embeds count as references: they appear in backlinks,
and a move rewrites their targets like any other link. In exports and
public pages an embed flattens to a link to the note.

## Unresolved links

An unresolved link renders as a create affordance: click it and the note is
created at the path the target implies (relative to the linking note, or at
the space root for root-style targets), then opens. The report page lists
every unresolved link per space — `Unresolved links` at the bottom of the
sidebar, or `GET /api/links/unresolved?space=name` — and, under it, every
alias two notes claim at once; each row names both notes so the duplicate
can be sorted out.

## Backlinks

Every note carries a panel of notes linking to it, each with the line the
link sits on. The same list closes the read view, under the note's last
line; it is left out when nothing links here.
`GET /api/notes/{id}/backlinks` returns the same list.

## Moving and renaming folders

`POST /api/dirs/move` with `{"path": "main/team", "to": "main/crew"}`
renames or moves a folder by moving every note under it through the
note move above, shallowest first, so each note's inbound links are
rewritten as it goes; then the rest of the directory (assets, files the
scanner does not index) follows and the empty shell is removed. Links
between notes inside the folder hold: a relative link keeps pointing at
its sibling, a root-style path is rewritten to the new one, a bare
filename stays a filename. The response counts the notes moved and the
links rewritten. A failure part-way stops there with the count; each
note already moved is a complete move of its own, so nothing is left
half-renamed. A folder cannot move inside itself.

## Moving and renaming notes

`POST /api/notes/{id}/move` with `{"path": "new/path.md"}` moves a note and
rewrites every inbound wikilink to keep pointing at it, keeping its
aliases: a link that resolved through an alias is left as written, because
the alias still resolves. Each rewrite is a
CRDT edit with author `filesystem`, so anyone with a linking note open sees
the new target live, and the files on disk follow through the ordinary
write-back. The style of each link is kept: relative links stay relative,
root-style paths stay rooted, filenames stay filenames.

The move is all-or-nothing. Path safety, target collisions, and write
permission on the spaces involved are all checked before anything changes;
a refused move applies nothing. A move into another space cannot rewrite
the links left behind (a target cannot reach across spaces), so it leaves
them as written, counts them in the response as `broken`, and they show up
in the unresolved report.

When spaces get real permissions (a later phase), a move that would rewrite
a link in a space the actor cannot write fails with 403 and applies
nothing.

## Endpoints

| Endpoint | Purpose |
| --- | --- |
| `GET /api/notes/{id}` | note payload, including its `links` |
| `GET /api/notes/{id}/backlinks` | notes linking here, with context |
| `GET /api/links/unresolved?space=` | unresolved-link report |
| `POST /api/notes` | create a note (the create affordance) |
| `POST /api/notes/{id}/move` | move/rename with link propagation |
