# Agents

Two ways for an agent to work on a notes tree: through the files, when
it runs on the box, or through MCP, when it does not. Both end at the
same place — every write is merged into the note's document, shown live
to open clients, written back to the file, and committed to git under an
identifiable author.

## The filesystem path

Bind-mount the tree (or a space) into the agent's container and let it
work on files with the tools it already has:

```yaml
services:
  agent:
    volumes:
      - ./notes/homelab:/notes:rw
```

An agent told "document the homelab" then reads the tree with `rg` and
`cat`, writes `.md` files, links them with `[[wikilinks]]`, and the
server picks everything up: files are indexed, links resolve, backlinks
appear. No manual fixup, no API to learn. The rules the agent follows
are in [file-format.md](file-format.md) and, per space, in its
`CONVENTIONS.md` (below).

What the agent should know:

- Do not invent frontmatter ids. A file without `id` gets one on its
  next scan; a file with an invented id keeps it forever, including the
  collisions.
- The optional frontmatter keys the server reads are `order`, `trusted`
  and `aliases` (an inline list, `aliases: [Mom, Margaret]`, giving the
  note extra names `[[links]]` resolve by — see [links.md](links.md)).
- Write files completely and finish, or write to a temporary name and
  `mv` into place; a file still being written is indexed after it
  settles.
- `mv` moves the note and rewrites its inbound links; `rm` deletes it
  with a 30-day recovery window; `cp` creates a new note with a fresh
  id.
- A write by an agent on the files is attributed to the author
  `filesystem`, the same as any editor. If attribution matters, use the
  MCP path.

### CONVENTIONS.md

Each space can carry a conventions file at its root, written for the
humans and agents that work on it: the frontmatter contract, wikilink
syntax, the `_assets` convention, and the space's actual folder
structure. Generate it with:

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  https://notes.example.com/api/spaces/homelab/conventions
```

The file lands as `<space>/CONVENTIONS.md` — an ordinary note. An
existing file is returned untouched unless the body is
`{"refresh": true}`; regenerating is never accidental. Point the agent
at it first: an empty space plus a conventions file is a complete brief.

## The MCP path

Agents not on the box use the MCP server at `POST /mcp` (Streamable
HTTP, JSON-RPC 2.0). It is the same loop with attribution: every write
is an operation authored `agent:<label>`, live for open clients, durable
through write-back, and committed to git under the label.

### Tokens

An agent authenticates with a token, minted by the owner on the Agents
page in settings (`/settings/agents`: the MCP URL with a copy button,
the keys with their scope and last use, create and revoke) or at the
API:

```sh
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -d '{"label":"homelab-docs","spaces":["homelab"],"can_write":true}' \
  https://notes.example.com/api/agents
```

The answer carries the secret once:

```json
{"id":"01J...","label":"homelab-docs","spaces":["homelab"],"can_write":true,"token":"ya_..."}
```

Tokens are not user sessions: no password, no refresh flow, no expiry.
A token sees exactly its listed spaces, may write only when
`can_write`, is listed with its last use at `GET /api/agents`, and is
revoked at `DELETE /api/agents/{id}` — the next request with it fails.
Only the secret's hash is stored. Every use is logged.

Point an MCP client at the server with the token as a bearer header:

```json
{
  "mcpServers": {
    "yana": {
      "url": "https://notes.example.com/mcp",
      "headers": {"Authorization": "Bearer ya_..."}
    }
  }
}
```

### Tools

| Tool | What it does |
| --- | --- |
| `list_spaces()` | the token's spaces with note counts |
| `list_tree(space, path?)` | a space's notes as a nested tree, optionally under a prefix |
| `read_note(note)` | one note by id (26-character ULID) or path; metadata and content |
| `write_note(space, path, content, agent_label?)` | create or replace a note |
| `append_note(note, content, agent_label?)` | append to an existing note |
| `search_notes(query, space?, limit?)` | full-text search within the token's scope |
| `move_note(note, new_path)` | move or rename, rewriting inbound links |
| `upload_asset(space, note, filename, data, agent_label?)` | store a base64 file (e.g. an image) in the note's `_assets/`; returns the markdown link to put in the note |
| `draw_flowchart(space, path, mermaid, base?, remove?, agent_label?)` | draw a Mermaid flowchart as an Excalidraw drawing (`*.excalidraw.md`), or add to one; returns the embed to put in a note |
| `add_drawing_image(drawing, asset? \| data?, near?, width?, base, agent_label?)` | add a PNG, JPEG or GIF to a drawing, from a file in the space or base64 |

Drawings are optional. An agent asked for a diagram can still write a
```` ```mermaid ```` block in a note; `draw_flowchart` is for when the user
wants a drawing they will rearrange by hand. On a new path it lays the
flowchart out and creates the drawing; put the `embed` it returns
(`![[arch]]`) in a note to show the drawing there. Only flowcharts are
drawn: anything else is refused.

`read_note` on a drawing returns, instead of the scene JSON, the
flowchart as Mermaid with an id on every node (hand-drawn boxes too),
then a `layout:` section with one line per item: nodes with shape,
position, size, subgraph and colours; edges as `from->to` (a second
parallel edge is `from->to#2`) with line style and label; subgraphs;
and free items (loose text, lines, pictures, other shapes) with derived
ids. A line ends with `link=[[Target]]` when its element carries a
link. Pictures in the drawing come as image content blocks, at most 5
and 4 MiB together; the rest say `(not included)` on their layout line.
The result also carries `base`, the hash of the file as read.

Changing an existing drawing needs that `base`. `draw_flowchart`
upserts the nodes and edges it is sent by id, places new nodes next to
the ones they connect to, moves nothing that is there, and deletes only
the ids listed in `remove`; `add_drawing_image` puts a picture beside
the `near` node or below everything. If the drawing changed since the
read, the write is refused with "the drawing changed since you read it;
read it again", and any refusal writes nothing. A drawing the server
cannot read is never written. Writes go through the same lock as a save
from the app, so an open editor picks them up, and git attributes them
to `agent:<label>`.

`write_note` and `append_note` refuse drawings with "drawings are
changed with draw_flowchart and add_drawing_image, not this tool".
Search finds the text on a drawing, `move_note` renames one and
rewrites links to it, and `list_tree` lists it with the other notes. An
agent on the filesystem path should leave `.excalidraw.md` files alone
([file-format.md](file-format.md#drawings)).

`agent_label` defaults to the token's label. It lets one token name the
job in front of it, and it is what lands in the note's history and git:
the CRDT author and the commit author are both `agent:<label>`.

Writes are edits to the note's document, not file overwrites: an agent
rewriting a note while a phone client has it open updates the phone
live, and any typing in flight merges rather than conflicting. Writes
are rate-limited per label (`YANA_AGENT_RATE`, default 30 a minute); a
refused write says so and can be retried later. All paths go through
the same path-safety module as every other writer, so traversal
attempts are rejected, not followed.

## Attribution and undo

`note_updates.author` records `agent:<label>` for every MCP write, and
the git window commits those files under `<label> <agent@local>` — a
human's edits never mix into an agent's commit. To undo an agent's
work: revert its commit in the notes repository (`git revert`), or
restore a single note from its revision list in the UI. Both are one
action.

| Route | Notes |
| --- | --- |
| `GET /api/agents` | list tokens (owner only) |
| `POST /api/agents` | `{label, spaces, can_write}` — mints a token, owner only |
| `DELETE /api/agents/{id}` | revokes it immediately, owner only |
| `POST /api/spaces/{space}/conventions` | writes or refreshes the space's `CONVENTIONS.md` |
