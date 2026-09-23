-- Conflict copies. A note whose file name matches
-- *.conflict-<ts>.(md|html) is a copy another write parked beside the
-- note that survived a collision (a restore over an occupied path, or a
-- save over a diverged HTML note). conflict_of points at the surviving
-- note's id while that note still exists; the scanner recomputes the
-- column on every pass, so it is derived data like links and tasks.

ALTER TABLE notes ADD COLUMN conflict_of TEXT REFERENCES notes(id) ON DELETE SET NULL;
CREATE INDEX notes_conflict_of ON notes(conflict_of) WHERE conflict_of IS NOT NULL;
