# Realtime sync protocol

Live editing runs over one WebSocket endpoint, `GET /ws`, with one logical
room per note id. The server is a dumb relay: it routes opaque CRDT payloads
by note id and never parses, merges, or interprets them. Merging is the CRDT
library's job on each client (Yjs in the browser, the same wire format on
the server through the reconciliation loop).

## Framing

Every WebSocket frame is **binary** and carries exactly one **msgpack**
message. Msgpack was chosen over protobuf because a dumb relay benefits from
a schemaless codec: structs encode to short maps, both sides ignore unknown
fields, and no code generation step enters the build. Message identity is a
single string field, `t`.

Unknown message types or malformed frames close the connection. Text frames
are refused.

## Client to server

| `t` | Fields | Behaviour |
|---|---|---|
| `sub` | `n` note id, `sv` optional state vector, `a` optional author | Join the note's room. Authorizes, pins the note, and replies `subd` with everything the client's state vector is missing. Empty `sv` means "send the whole document". |
| `unsub` | `n` | Leave the room. An emptied room unpins the note. |
| `upd` | `n`, `u` CRDT payload, `a` author | Apply through the reconciliation loop: appended to `note_updates`, marked dirty for write-back, and fanned out to the room. |
| `aw` | `n`, `p` awareness payload | Broadcast to the room's other members only. Never persisted. |
| `watch` | `s` space, `a` optional author | Hear about changes in a whole space, without joining rooms. Authorizes against space membership and replies `watchd`. |
| `ping` | — | Replied with `pong`. |

Field names on the wire: `t` type, `n` note id, `s` space, `sv` state
vector, `u` update payload, `a` author, `p` awareness payload, `path`,
`c` error code, `r` error reason.

## Server to client

| `t` | Fields | Meaning |
|---|---|---|
| `subd` | `n`, `u` | Reply to `sub`: the delta the client is missing (empty when it is current). Live `upd` messages may arrive before or after it; CRDT updates are idempotent and commutative, so order does not matter. |
| `upd` | `n`, `u`, `a` | A relayed update. Includes edits authored by the filesystem (external writes) and by other clients. The sender does not receive its own updates back. |
| `aw` | `n`, `p` | A relayed awareness payload (cursor, selection, user colour). |
| `moved` | `n`, `path` | The note's file moved or was renamed. |
| `deleted` | `n` | The note's file is gone. |
| `watchd` | `s` | Reply to `watch`: the space is being watched. |
| `chg` | `n`, `path` | A note in a watched space changed (an edit, a move, a delete). Carries no payload; a listing page refetches what it needs, debounced. |
| `pong` | — | Reply to `ping`. |
| `err` | `n`, `c`, `r` | Code and reason: `not_found`, `invalid_author`, `rate_limited`, `too_many_rooms`, `invalid`, `forbidden`, `internal`. |

## Authors

Every `upd` carries an author identity: `user:<name>` or `agent:<label>`.
The string feeds the CRDT log (`note_updates.author`), the write rate
limiter, and the git history layer. `filesystem` is reserved for the
reconciliation loop and refused from clients.

The handshake authenticates the connection: pass the access token as
`?token=` (or an `Authorization: Bearer` header) when dialing `/ws`. The
server fixes the author from the token — a client's declared `a` is
ignored — so one connection cannot claim another identity. Each `sub`
does one lookup of the caller's membership and role in the note's space;
`upd` is accepted only from rooms the connection subscribed to with the
editor or owner role, and membership revoked through a space's
`.space.yml` severs the subscription within one watcher cycle
(`forbidden`). A server built without accounts accepts any connection
and trusts the declared author.

## Sync model

On connect a client sends `sub` with its state vector and receives only the
delta; a fresh client sends no vector and receives the full document. Browsers
batch keystrokes on a 50ms timer and send merged updates, so a typing session
produces at most ~20 messages a second instead of one per keystroke.

Reconnects use exponential backoff (500ms doubling to 8s, with jitter). A
client that was offline keeps editing locally; on reconnect it receives the
missed delta and sends its queued updates, and both sides merge. Killing and
restarting the server loses nothing: the log and sidecars persist per note,
and clients resume from their state vectors.

The server also pings each connection every `YANA_WS_PING_INTERVAL`; browsers
answer protocol pings automatically and send an application `ping` when idle
to keep intermediaries from dropping the socket.

## Limits

| Limit | Default | Knob |
|---|---|---|
| Concurrent connections | 256 | `YANA_WS_MAX_CONNECTIONS` |
| Rooms per connection | 16 | `YANA_WS_MAX_ROOMS_PER_CONN` |
| Watched spaces per connection | 32 | `YANA_WS_MAX_SPACES_PER_CONN` |
| Inbound frame size | 1 MiB | `YANA_WS_MAX_MESSAGE_BYTES` |
| Messages per user per minute | 1200 | `YANA_WS_USER_RATE` |
| Messages per agent per minute | 300 | `YANA_WS_AGENT_RATE` |

The message rate reuses the token-bucket semantics of `internal/pathsafe`'s
write limiter, keyed by author. The defaults leave headroom for a client at
the top of its batching rate. Each connection has a bounded outbound queue
(256 messages); a connection that cannot keep up is closed rather than
allowed to grow memory without bound.

## Observability

`GET /api/status` carries a `realtime` object: active connections, rooms,
subscriptions, updates relayed and dropped, and slow connections closed.
Pending write-backs are the `sync.dirty` field of the same response, and the
reconciliation logs name every write-back and read-in.
