# Deletion, trash, recovery

Deleting a note never destroys it. The file moves to the trash, the edit
history is retained, and both come back on restore. Only two actions
destroy anything permanently: deleting one entry forever from the trash,
and emptying the trash. Both ask before they act.

## What a delete does

Deleting a note in the app:

1. moves the file to `.trash/<space>/<original path>`, keeping its
   structure (a second deletion of the same name gets a
   `.deleted-<timestamp>` suffix, so nothing is overwritten);
2. retires the note's CRDT document to `.sync/crdt/retired/<id>.bin`,
   with any edits that had not reached the file yet;
3. records where the note lived, so the trash can list it and restore
   can return it;
4. leaves every inbound wikilink in place — it shows as unresolved until
   the note returns.

Before the delete goes through, the app lists the note's inbound links:
whoever points at the note is about to hold an unresolved link.

## External deletion

A file removed outside the app — `rm`, a sync tool, an editor — is
treated as a delete. There is no trash copy to move, so recovery is the
retained document: the trash lists the note as history only, and restore
rebuilds the file from it, including edits that had not been written to
disk when the file vanished. `rm` a note from a shell, open the trash,
restore, and the note is back with its full edit history.

A file deleted before its document was ever loaded has no history; the
trash does not list it. Git history (Phase 7) still covers the file's
past revisions.

## The trash

The trash page lists deleted notes with their deletion time, original
path, and what recovery holds (the file, the history, or both). Entries
stay for the retention window — 30 days by default, `YANA_CRDT_RETENTION`
to change it — and are swept after it: the `.trash` copy, the retained
document, and the edit log all go together.

Restore returns the note to its original path and re-resolves inbound
links across the space. If a new note has taken the original path, the
restored note lands beside it as `name.conflict-<timestamp>.md`, the
same convention as colliding HTML saves, and nothing is overwritten.

Empty trash destroys every entry you have write access to; each entry's
Delete forever destroys just that one. These are the only operations
that permanently destroy content.

## By hand

The trash is a plain directory: `.trash/projects/ideas/a.md` is the file
`projects/ideas/a.md` had when it was deleted. Move it back with `mv`
and the next scan indexes it again — same id, same history. The index
(`.sync/index.db`) knows nothing the tree does not: rebuild it and the
trash listing is rebuilt from `.trash/` itself, with original paths read
from the directory structure. The one thing a rebuilt index cannot
recover is the original path of a file deleted externally (only the
retained document knew it); its history is still there until the window
passes.

## API

| Method | Path | What it does |
| --- | --- | --- |
| `DELETE` | `/api/notes/{id}` | soft-delete: file to `.trash`, history retained |
| `GET` | `/api/trash` | list deleted notes in your spaces |
| `POST` | `/api/trash/{id}/restore` | return a note to its original path |
| `DELETE` | `/api/trash/{id}` | destroy one entry permanently |
| `POST` | `/api/trash/empty` | destroy every entry you may write |

Space membership applies as everywhere else: a member sees and recovers
trash only in their own spaces, and emptying reaches only those.
