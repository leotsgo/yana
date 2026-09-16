# Links

Wikilinks turn a folder of notes into a wiki. `[[target]]` links to a note,
`[[target|display text]]` links and shows something else.

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
4. Otherwise the link is **unresolved**.

A target without an extension is read as `.md`. HTML notes need their
extension spelled out: `[[dashboard.html]]`.

Resolution runs when the index runs, over the note bodies the index holds.
The `links` table is derived data like everything else in the index
database: delete it and rescan, and it comes back.

## Unresolved links

An unresolved link renders as a create affordance: click it and the note is
created at the path the target implies (relative to the linking note, or at
the space root for root-style targets), then opens. The report page lists
every unresolved link per space — `Unresolved links` at the bottom of the
sidebar, or `GET /api/links/unresolved?space=name`.

## Backlinks

Every note carries a panel of notes linking to it, each with the line the
link sits on. `GET /api/notes/{id}/backlinks` returns the same list.

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
rewrites every inbound wikilink to keep pointing at it. Each rewrite is a
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
