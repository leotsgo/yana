-- Tasks: every `- [ ]` / `- [x]` line in the notes, derived like links.
-- Deleting the table and rescanning rebuilds it. A row's line number is
-- the 0-based line of its marker, counted from the start of the note's
-- body (frontmatter excluded), which is the same line the rendered
-- checkbox carries in data-line.

CREATE TABLE tasks (
    note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    line INTEGER NOT NULL,
    -- List nesting depth of the marker, in levels of two spaces.
    indent INTEGER NOT NULL DEFAULT 0,
    -- The task's text after the marker, rendered to inline HTML at scan
    -- time so listing never re-parses a note body.
    text TEXT NOT NULL,
    done INTEGER NOT NULL DEFAULT 0,
    -- When the scanner first saw this box ticked, kept across rescans of
    -- an unchanged line. NULL while open.
    done_at INTEGER,
    -- The nearest heading above the task, '' at note top level.
    heading TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (note_id, line)
);
CREATE INDEX tasks_done ON tasks(done, done_at);
