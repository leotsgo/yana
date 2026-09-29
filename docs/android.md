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
both clients grow the same tree. Attachments that are not images ride
the same `_assets/` upload path and link as the plain link the web
writes; PDFs open on the content origin, sandboxed like HTML notes;
public links read, make, and revoke through the same API the web uses.

## Large screens

On a tablet, a foldable opened flat, or a Chromebook, the app uses the
width the way the web does at tablet size: medium and expanded widths
go list-detail (the tree, search, tasks, or a tag page on the left,
the note on the right), expanded width holds two notes side by side
over a draggable divider, and a hardware keyboard carries the web's
shortcuts — switcher, new note, search, edit, close. State survives a
rotation and a fold without reloading the note or losing the caret.
The web's tabs are not ported; the recents list and the switcher cover
the same need on Android. See
[android/README.md](../android/README.md) for the details.

## Not yet available

- **New from template.** The web's template picker (Phase 26) has not
  landed yet. When it does, the Android New flow grows it; until then
  a new note on Android starts as a heading and an empty line, with
  the caret after them.
