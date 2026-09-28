# The Android client

The full guide to the Android client is [android/README.md](../android/README.md)
— build, sign-in, the offline replica, realtime sync, the reading view,
the editor, capture, and settings. This page is the short register of
where Android stands against the web: what is shared by construction,
and what is not there yet.

## Shared by construction

The reading view renders with the same Go engine the server uses; the
editor edits the same CRDT documents over the same relay protocol; the
formatting buttons are a tested port of `web/src/format.tsx`; wikilink
and tag completion follow the same triggers and write the same text as
the web's; names (uploads, new notes) follow the web's conventions so
both clients grow the same tree.

## Not yet available

- **New from template.** The web's template picker (Phase 26) has not
  landed yet. When it does, the Android New flow grows it; until then
  a new note on Android starts as a heading and an empty line, with
  the caret after them.
