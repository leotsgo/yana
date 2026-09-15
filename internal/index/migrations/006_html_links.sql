-- HTML notes keep their raw body here. The body column holds the
-- tag-stripped text search reads; links are extracted from raw_body,
-- which for markdown notes is the body itself.

ALTER TABLE note_bodies ADD COLUMN raw_body TEXT;
UPDATE note_bodies SET raw_body = body WHERE raw_body IS NULL;
