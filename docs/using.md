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

**New note** on the home screen, at the top of the window, or `Alt+T`.
It asks where and what first: the box starts in the folder you were
looking at, so type a name and press Enter to write. `Tab` goes into the
highlighted folder, `Shift+Tab` back out; on a phone tap a folder to go
into it. Enter with no name makes an untitled note there and asks for
its title. The `+` beside a space or folder in the sidebar skips the
question and makes an untitled note right there.

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
yet is shown dashed; click it and the note is created. Every note that
links to the one you are reading is listed under Linked mentions at the
bottom of the note, each with the line the link sits on; Details (the
panel button above the note) lists them too.

A note can answer to other names: an `aliases: [Mom]` line in its
frontmatter (the `key: value` block at the top of the file) makes
`[[Mom]]` find it, and `[[` offers `Mom → its title`.

## Embed one note in another

`![[Note name]]` shows that note's text inside this one, under a small
heading that links to it. One level only: an embed inside an embedded
note shows as a link.

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

From a terminal, the `yana` binary does the same. Sign in once with
`yana login https://your-server` (it asks for the username and password
and keeps the session in `~/.config/yana/session.json`, readable by you
only), then `yana today "call the bank"` adds `- call the bank` to
today's note. `yana today -` (or a pipe) reads the lines from stdin;
`--space` picks a space other than the one set under Settings →
Defaults; `yana yesterday` and `yana tomorrow` take text the same way.
`yana today` with no text opens the note in neovim (below). `yana
whoami` and `yana logout` do what they say.

## From a terminal

Once signed in, `yana` on its own is neovim: your own `nvim`, with your
config, plugins, telescope and motions, plus a yana plugin that shows
the notes on the server. One `:q` (or `:qa`) quits it and you are back
at the shell. Run with no terminal on stdin, as in a container or a
service, `yana` serves instead; `yana serve` always does.

Shortcuts from the shell land straight in neovim:

| Command | Opens |
|---|---|
| `yana` | the buffers you left open last time, else the default space or the spaces |
| `yana today`, `yesterday`, `tomorrow`, `yana 2026-11-04` | that day's note, made if needed (with text, or a pipe, they append instead) |
| `yana new [--space s] <name>` | a new note in the folder for new notes (Settings → Defaults), or in `--space`; a name with `/` is a path |
| `yana open <path or title>`, or just `yana <path or title>` | that note; several notes with that title open the picker on it |
| `yana find [query]`, `yana grep [query]` | the note picker or the full-text search, typed in already |
| `yana recent` | recently opened notes |
| `yana completion bash\|zsh` | prints shell completion (below) |

A shortcut that names a note opens it next to the restored buffers and
makes it current.

### Buffers

Everything is a `yana://<server>/…` buffer. A folder,
`yana://host/home/Projects/`, lists its folders (ending in `/`) and
notes one per line, like oil.nvim; the top, `yana://host/`, lists the
spaces. Edit the listing as text and `:w`: change a name to rename or
move it (a name with `/` moves it into that folder), add a line to make
a note (end it in `/` for a folder), delete a line to send it to the
trash. yana lists what it is about to do and asks before doing it; say
no and the listing keeps your edits. Each line starts with a hidden id,
which is how a rename is told from a delete and a create; leave it be.

A note, `yana://host/home/kiln.md`, is its text (the frontmatter stays
on the server). `:w` merges your text with whatever changed on the
server since you opened it, so someone typing in the app or an agent
appending meanwhile keeps their text, and when that brought something
in the buffer shows the merged text and says `merged 1 change from
elsewhere`. The save runs in the background, so `:w` and `:wq` never
wait on the network; if it fails the buffer is marked modified again
and the message says why, and quitting waits for a save still running,
so a failed one stops the quit instead of losing the text. While a note
is open and unmodified, edits made elsewhere show up in it as they
happen; a buffer you are changing is never overwritten. A note in a
space where you are a viewer opens read-only. HTML notes edit their
source; drawings open in the browser. `:e yana://host/home/new.md` on a
note that is not there makes it on the first `:w`.

When you quit, yana remembers the yana buffers you had open, which one
was current and where the cursor was in each, per server, in
`~/.local/state/yana/nvim.json`; the next `yana` reopens them.

### Keys and commands

In a folder listing (all buffer-local, `g?` shows them):

| Keys | Does |
|---|---|
| `Enter` | open the folder or note |
| `-` | up a folder (in a note: the folder it is in) |
| `_` | the default space, else the spaces |
| `Ctrl-p` | toggle a preview of the entry under the cursor |
| `g?` | help |

In a note, `gf` or `Enter` on a `[[wikilink]]` opens the note it names,
resolved the way the server does (within the note's space); elsewhere
they do what they always do.

In a session `yana` launched, these are global (leader is yours, space
in the usual setup), replacing your own for that session:

| Keys | Does |
|---|---|
| `<leader>sf` | find notes by path or title; `today`, `yesterday`, `tomorrow` or `2026-11-04` offers that daily note first (made when opened), `#tag` keeps notes with that tag, a leading `space/` stays in one space |
| `<leader>sg` | full-text search as you type; opens on the matching line |
| `<leader><leader>` | recently opened notes |

The pickers use telescope, with a preview of each note, when it is
installed, else `vim.ui.select`.

`:Yana` opens the default space, else the spaces; `:Yana {today|yesterday|tomorrow|new
<name>|mkdir <name>|find|grep|recent|open <path>|whoami|logout|help}`
does the rest, with completion. A session `yana` launched has short
forms too: `:Today`, `:Yesterday`, `:Tomorrow`, `:New <name>`,
`:Mkdir <name>`, `:Sf`, `:Sg`, `:Recent`, `:Whoami`, `:Logout`. `:New`
and `:Mkdir` work in the folder of the buffer you are in.

### Config

`~/.config/yana/init.lua` (or under `$XDG_CONFIG_HOME`), when it exists,
runs after the plugin in a session `yana` launched; your own neovim
config loads first, as always. `yana config` prints its path and the
defaults:

```lua
require('yana').setup {
  keys = { find = '<leader>sf', grep = '<leader>sg', recent = '<leader><leader>' },
  picker = 'auto', -- 'telescope', 'select' or 'auto'
  live = true,     -- refresh unmodified notes changed elsewhere
  space = nil,     -- the space yana opens in, like 'notes', instead of the spaces
}
```

With `space` set, `yana`, `:Yana` and `_` open that space; `-` from its
top still lists the others. A key set to `false` is left to your config. `YANA_NVIM` names another
neovim binary.

### In your own neovim

The plugin can live in your everyday neovim too. `yana` writes it to
`~/.local/share/yana/nvim` (or under `$XDG_DATA_HOME`) each time it
starts, so point your plugin manager there; with lazy.nvim:

```lua
{ dir = vim.fn.expand '~/.local/share/yana/nvim', lazy = false } -- it only defines :Yana and the yana:// autocmds until used
```

Then `:Yana` opens the spaces and `:Telescope yana find` (or `grep`,
`recent`) the pickers. It sets no global keys there unless you ask:
`require('yana').setup { global_keys = true }`. The plugin never sees
your token: it runs `yana api …`, which reads the session that
`yana login` keeps, so `yana` has to be on your `PATH`.

### Shell completion

```sh
source <(yana completion bash)   # in ~/.bashrc
source <(yana completion zsh)    # in ~/.zshrc, after compinit
```

or save `yana completion zsh` as `_yana` in a directory on `$fpath`.
It completes the commands (with descriptions in zsh), `--space` and the
space names after it, and note paths after `yana open` and a bare `yana`,
one folder at a time. Paths come from a cache in `~/.local/state/yana`
refreshed in the background, so Tab never waits long on the server.

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
