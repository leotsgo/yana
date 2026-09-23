# Using it

How to do the everyday things, for someone who was handed a sign-in and
nothing else. The same material lives inside the app: the first sign-in
seeds a note called **Start here** that shows every feature by using it,
and Help (in the account menu and the command palette) opens it at the
right section.

## Sign in

Open the address you were given and sign in with the username and
password the owner made for you. On a phone, add the app to the home
screen when it offers (or from the account menu): it opens full screen
and works offline.

## Write a note

**New note** on the home screen, at the top of the window, or in the
sidebar beside a space name. Type a title, press Enter, and write. The
file is named after the title and lands in the space you were looking at.

A note is markdown. Text is a paragraph; a line starting with `#` is a
heading; `- ` starts a list; `**bold**`, `_italic_`, and `` `code` ``. On
a desktop the buttons in the bar above the editor insert all of these for
you; on a phone the same buttons sit above the keyboard. Press **E** to
edit and **Esc** to go back to reading; on a phone tap **Edit** and
**Done**.

## Link two notes

Type `[[` and start typing a note's name; pick it from the list and the
link is finished. The Link button does the same. To show different text,
`[[Note name|the words to show]]`. A link to a note that does not exist
yet is shown dashed; click it and the note is created. Details (the
panel button above the note) lists every note that links to the one you
are reading.

## Add a picture

Drag an image onto the editor, paste one, or use the Image button (on a
phone it opens the photo picker or the camera). The file is stored beside
the note in an `_assets` folder and the line that shows it is written for
you. Other kinds of file upload the same way and become a plain link.

## Tasks

A list item starting with `[ ]` is a task; the Task button makes one.
Tick the box while reading and the file is updated.

The **Tasks** page gathers every open box across a space (or every
space you belong to) in one list, grouped by the note it lives in.
Each row links back to its line in the note, ticking there ticks the
note itself, and the list follows changes as they happen. Filters
narrow it to a folder or a tag; a toggle shows what was completed in
the last thirty days. It is in the sidebar, the bottom bar on a phone,
the command palette, and the keyboard shortcut listed in Help.

## Tabs

On a tablet or a computer, notes open in tabs above the page. Clicking
around the tree fills one preview tab (its title in italics) instead of
opening a new tab each time; start typing in it, or double-click it,
and it stays. `Ctrl`-click (`⌘`-click on a Mac) or middle-click a note
anywhere to open it in a new tab behind the one you are reading.
Right-click a tab to close others, pin it, or keep it open. The tabs
come back after a reload, each where you left it. `Alt+]` and `Alt+[`
move between them, `Alt+1` to `Alt+9` jump to one, `Alt+W` closes one,
and Help lists the rest of the keys.

On a computer you can also put two notes side by side: pick Open to the
right on a note's menu, or press `Ctrl+\` (`⌘\` on a Mac) to open the
note you are in beside itself, one side reading and the other editing.
Drag the bar between them to resize, and drag tabs from one side to the
other.

## Tags

A word with `#` in front is a tag, anywhere in the note. Tags are
clickable, every tag has a page listing its notes, and the switcher finds
notes by tag when you type `#`.

## Today and capture

**Today** opens today's daily note, making it if needed. **Capture**
takes one line and appends it to today's note without opening it, which
is the quickest way to write something down on a phone.

## Find things

The search box in the sidebar searches titles and bodies (on a phone,
search is a tab in the bottom bar). The switcher opens a note by name or
tag. The tasks page lists every open box across your spaces, with the
open count on the home screen. Recently opened and pinned notes are on
the home screen; pin a note from its menu.

## Move, rename, delete

Drag a note onto a folder in the sidebar, or use **Move to a folder** in
the note's menu. Rename by editing the title. Delete from the menu; the
note goes to the trash for thirty days and can be brought back. Links to
a moved or renamed note are rewritten for you.

## History

Details shows every saved version of the note; open one to see what
changed and restore it if you want. The author beside each revision
says who made it — a person, an agent, or an edit that arrived on the
files.

## What changed

The activity page (from the home screen, the command palette, or a
folder's right-click menu) shows the same history as a feed: who
changed which notes and when, grouped by day. An agent's overnight run
reads as one entry. The line under the home screen's summary counts
what changed since you last looked; opening the feed is what marks it
seen.

Every entry carries a restore. **Restore this space to here** (or, on
the every-space feed, **Restore the tree to here**) opens a preview
first: every note that would come back, change, move, or go, listed
one by one, before anything is touched. The restore itself is work
like any other — the current state is committed and tagged, what it
removes lands in the trash, and the history reads who restored what
and to when. A whole-tree restore is the owner's; a space restore
needs write access on that space.

A single note deleted by accident does not need the feed. Settings →
Data lists deleted notes, each one restorable to where it lived — from
the trash while it holds the copy, and from the history after that,
under a free name if something new has taken the path.

## Share a space

The top-level folders are spaces. The owner adds a person under Settings
→ People and gives them a role in a space under Settings → Spaces. That
person signs in and sees exactly the spaces they belong to.

## Agents

An agent can read and write notes through the files, or from another
machine through MCP with a key from Settings → Agents. Its changes show
up live and are attributed to it in the history. See
[agents.md](agents.md).

## Keyboard

Help, in the account menu and the command palette, lists every shortcut.
