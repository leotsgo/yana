-- Drawings (Excalidraw) are a third note kind. SQLite cannot alter a CHECK
-- constraint, and rebuilding notes would cascade-delete every table that
-- references it, so the constraint text is edited in place. This only widens
-- a CHECK; no row or page changes. It depends on the CHECK text of 001 and 007
-- being exactly as written there. RESET (not OFF) makes the connection reload
-- the schema it just edited.
PRAGMA writable_schema = ON;
UPDATE sqlite_schema
   SET sql = replace(sql, 'kind IN (''md'', ''html'')', 'kind IN (''md'', ''html'', ''drawing'')')
 WHERE type = 'table' AND name IN ('notes', 'deleted_notes');
PRAGMA writable_schema = RESET;
