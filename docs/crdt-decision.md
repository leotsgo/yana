# CRDT decision (Phase 0)

The build plan flags the CRDT as the highest-risk unknown: every phase from
realtime sync onward assumes its outcome. This document records the decision,
the evidence behind it, and the code that Phases 2, 3, and 12 build on.

## Decision

| Where | What | Version evaluated |
|---|---|---|
| Library | Yjs (the algorithm and the V1 wire format) | — |
| Browser | `yjs` from npm, the reference implementation | 13.6.32 |
| Server | `github.com/reearth/ygo`, a pure-Go port | v1.50.0 |
| Server fallback | `github.com/Deln0r/ygo`, a second pure-Go port | v1.19.0 |
| Android | reearth/ygo bound with `gomobile bind` through a yana-owned package | — |
| Android fallback | `yrs` (the Rust implementation) through uniffi with a self-generated Kotlin binding | — |

Every peer speaks the same Yjs V1 update format. The browser, the server, and
the phone can exchange raw update bytes with no translation layer, and the
server stays a single static Go binary with no cgo, no JS runtime, and no
sidecar.

## Why Yjs over Automerge

The plan lists five criteria in priority order. Yjs wins on the ones that
matter most for this project.

1. **Kotlin/Android bindings.** Neither library has an official, maintained
   Kotlin binding. On the Automerge side, `org.automerge:automerge` (0.0.7,
   an AAR) is the strongest official binding of any CRDT for Android. On the
   Yjs side, `y-crdt/ykt` is inactive and `y-uniffi` ships Swift only. That
   would favour Automerge, except that both pure-Go Yjs ports build with
   `gomobile bind`, which gives Android the same engine the server runs with
   no third-party binding at all. That is a stronger position than depending
   on a 0.0.x artifact, and it is the tie-breaker (see Android, below).
2. **Scoped undo.** Yjs `UndoManager` tracks transactions by origin, so user
   A's undo never reverts user B's text. Automerge has no scoped undo; it
   would have to be built above the library. Both Go ports implement
   `UndoManager` with tracked origins, and the harness proves it.
3. **Text diff application.** Equivalent in both: compute a diff between the
   current CRDT text and the file text, replay it as insert/delete
   operations. Untouched characters keep their identity, so an external file
   edit merges with concurrent typing instead of replacing it.
4. **Snapshot/compaction.** Both can encode a full state document from an
   update log. Yjs's state document for a 500-update log is 4.4KB.
5. **Update size and merge performance.** On a ~100KB document a single
   keystroke update is 39 bytes, loading the document and applying that
   keystroke takes under 0.5ms, and applying a 100-replacement external diff
   takes under 30ms.

There is one more reason that is not on the list. The plan wants the server
to render CRDT to text and to apply file diffs as CRDT operations, and it
wants the server to be pure Go. Yjs has no first-party Go implementation, but
two pure-Go ports exist, both are listed on the Yjs ports page, and both pass
the harness. Automerge has no Go implementation at all.

## Why reearth/ygo over Deln0r/ygo

Both ports pass every test in the harness on identical inputs. The
differences are operational.

| | reearth/ygo | Deln0r/ygo |
|---|---|---|
| Per-transaction update | Emitted by `OnUpdate` after each transaction; 24 bytes per append, constant over history | Computed by `EncodeDiff` against a saved state vector; re-emits the whole boundary block, so 27 bytes grows to 1720 bytes over 200 appends |
| Maintenance | Organisation-owned, CI conformance suite against the JS reference | Single maintainer |
| Undo with tracked origins | Yes | Yes |
| gomobile | `mobile/` façade, no `UndoManager` | `gomobile/` façade with an `UndoManager` that has no tracked-origin option |
| Wire format | Yjs V1 (and V2) | Yjs V1 (and V2) |

The update-size behaviour of Deln0r/ygo is documented upstream and is a
bandwidth cost, not a correctness one: receivers de-duplicate, and the harness
confirms convergence when a receiver already holds part of a re-emitted
block. It still matters for the server, which is a long-lived appender for
every note it reconciles. reearth/ygo emits proper incremental updates.

Because the two ports share a wire format and a shape (doc, text, transaction,
update bytes), swapping one for the other is a contained change in the server's
CRDT adapter. Deln0r/ygo stays in `go.mod` for the spike so both keep running
in CI until Phase 2 lands; it is removed with the spike.

## Android

The Android app binds reearth/ygo with `gomobile bind`. The port's `mobile/`
package is written for that boundary (only `string`, `int64`, `bool`,
`[]byte`, `error`, and bound pointers cross it) and already exposes create,
apply update, encode state, encode state vector, encode diff, text read, and
text mutation. It does not expose `UndoManager`.

YANA/ therefore owns a small bind package, `mobile/crdt` in this repository,
that wraps the `crdt` package with the same type constraints and adds the
`UndoManager` (with a tracked origin for local edits) on top of the operations
the `mobile/` façade already has. `gomobile bind -target android -javapkg
com.<domain>.yana.crdt ./mobile/crdt` produces the AAR. This is Phase 12 work;
the decision here is that no third-party Kotlin binding is needed.

Fallback if gomobile becomes a problem (toolchain, binary size, or JNI
overhead in the editor loop): `yrs` through uniffi with a Kotlin binding we
generate ourselves. Same wire format, different engine, more build machinery.

## Evidence

The harness lives in `spike/crdt/` (Deln0r/ygo) and `spike/crdt/reearth/`
(reearth/ygo) and runs under `go test ./spike/...`. It is not production code.

- **Three-writer convergence.** Two headless clients and one simulated
  filesystem writer, which only ever knows the rendered text and replaces it
  wholesale (applied as a diff), mutate one document over 1000 randomised
  interleavings with random delivery order. All three converge every time,
  with no pending updates left over. Both ports.
- **Cross-language.** `spike/crdt/js/fixtures.mjs` drives `yjs` 13.6.32.
  JS-authored state loads in Go; Go-authored updates load in JS, including
  out of order and overlapping; a full state round trip (Go, JS edit, delta
  back to Go) converges. Both ports. Skipped when `node` or the fixture's
  `node_modules` is missing; CI installs both.
- **Scoped undo.** Two users write into one document; an `UndoManager`
  tracking only `user:a` undoes A's two edits and refuses to undo B's. Both
  ports.
- **Snapshot/compaction.** A 500-update log (1.06MB as recorded by
  Deln0r/ygo, inflated by the caveat below) merges into a single 4.4KB
  update and encodes to a 4.4KB state document; a fresh replica loads either
  and reads the same text.
- **100KB document.** Keystroke update 39 bytes, load and apply under 0.5ms,
  diff-apply of 100 replacements under 30ms.
- **Incremental update size.** 201 appends by one writer: reearth/ygo 24
  bytes each; Deln0r/ygo 27 bytes growing to 1720.

## Code samples

Go samples use reearth/ygo and are lifted from the harness. JS samples use
`yjs`. The shared text is a `Y.Text` named `body`; the frontmatter is not part
of the CRDT (the reconciliation loop prepends it on write-back).

### Create a document

```go
import "github.com/reearth/ygo/crdt"

doc := crdt.New()          // random client id; crdt.WithClientID to pin one
text := doc.GetText("body")
```

```js
import * as Y from 'yjs'

const doc = new Y.Doc()
const text = doc.getText('body')
```

### Encode an update

Every committed transaction produces one incremental V1 update. Subscribe
once and forward the bytes; the origin tells an echo guard where the change
came from.

```go
unsubscribe := doc.OnUpdate(func(update []byte, origin any) {
    if origin == "remote" {
        return // already came from the network
    }
    broadcast(update)
})
defer unsubscribe()

doc.Transact(func(txn *crdt.Transaction) {
    text.Insert(txn, 0, "hello", nil)
}, "local")
```

```js
doc.on('update', (update, origin) => {
  if (origin !== 'remote') ws.send(update)
})
doc.transact(() => text.insert(0, 'hello'), 'local')
```

To encode everything a peer is missing rather than a single transaction, pass
its state vector (see below): `crdt.EncodeStateAsUpdateV1(doc, sv)`, or
`Y.encodeStateAsUpdate(doc, sv)`. With a nil state vector this is the full
document.

### Apply an update

Updates apply in any order. Anything that references history a replica has
not seen yet is parked and integrated when the gap fills.

```go
if err := crdt.ApplyUpdateV1(doc, update, "remote"); err != nil {
    return err
}
```

```js
Y.applyUpdate(doc, update, 'remote')
```

### Encode a state vector

A state vector is the per-client clock the replica has reached. Peers exchange
them on connect to work out what each is missing.

```go
sv := crdt.EncodeStateVectorV1(doc)                 // []byte, send to peer

remote, err := crdt.DecodeStateVectorV1(peerBytes)  // peer's, received
if err != nil {
    return err
}
missing := crdt.EncodeStateAsUpdateV1(doc, remote)  // what the peer lacks
```

```js
const sv = Y.encodeStateVector(doc)
const missing = Y.encodeStateAsUpdate(doc, remoteSv)
```

### Diff-apply an external text edit

The filesystem writer knows only the rendered text. The reconciliation loop
diffs the current CRDT text against the file and replays the diff as
operations, so the characters the external editor left alone keep their
identity and merge with concurrent typing. `Y.Text` indexes in UTF-16 code
units, so each chunk is measured in UTF-16 lengths.

```go
import (
    "unicode/utf16"

    "github.com/reearth/ygo/crdt"
    "github.com/sergi/go-diff/diffmatchpatch"
)

func u16len(s string) int { return len(utf16.Encode([]rune(s))) }

// applyTextDiff mutates text so that it reads as want. Read the current text
// before the transaction: the doc lock is not re-entrant.
func applyTextDiff(doc *crdt.Doc, text *crdt.YText, want string) {
    have := text.ToString()
    if have == want {
        return
    }
    dmp := diffmatchpatch.New()
    diffs := dmp.DiffCleanupSemantic(dmp.DiffMain(have, want, false))
    doc.Transact(func(txn *crdt.Transaction) {
        pos := 0
        for _, d := range diffs {
            n := u16len(d.Text)
            switch d.Type {
            case diffmatchpatch.DiffEqual:
                pos += n
            case diffmatchpatch.DiffDelete:
                text.Delete(txn, pos, n)
            case diffmatchpatch.DiffInsert:
                text.Insert(txn, pos, d.Text, nil)
                pos += n
            }
        }
    }, "filesystem")
}
```

### Snapshot and compaction

An update log compacts either by merging the updates without a document or by
encoding the full state of a loaded document. Both produce one update that a
fresh replica loads directly. The reconciliation loop uses the second form
when a note's log passes the compaction threshold.

```go
merged, err := crdt.MergeUpdatesV1(log...)   // no Doc needed
if err != nil {
    return err
}

state := doc.EncodeStateAsUpdate()            // from a loaded Doc

fresh := crdt.New()
if err := fresh.ApplyUpdate(state); err != nil {
    return err
}
```

```js
const merged = Y.mergeUpdates(log)
const state = Y.encodeStateAsUpdate(doc)
```

### Scoped undo

An `UndoManager` with tracked origins captures only transactions tagged with
those origins. Remote updates arrive under a different origin and are never
on the stack. In the browser each user's editor is its own replica, so the
tracked origin is simply the local one; the sample uses two origins on one
document to show the boundary.

```go
um := crdt.NewUndoManager(doc, []crdt.SharedType{text},
    crdt.WithTrackedOrigins("user:a"),
    crdt.WithCaptureTimeout(-1)) // one stack item per transaction
defer um.Destroy()

doc.Transact(func(txn *crdt.Transaction) { text.Insert(txn, 0, "hello ", nil) }, "user:a")
doc.Transact(func(txn *crdt.Transaction) { text.Insert(txn, 6, "world", nil) }, "user:b")
doc.Transact(func(txn *crdt.Transaction) { text.Insert(txn, 11, "!", nil) }, "user:a")

um.Undo() // "hello world"
um.Undo() // "world"
um.Undo() // false: user:b's edit is not on this stack
```

```js
const um = new Y.UndoManager(text, { trackedOrigins: new Set(['user:a']), captureTimeout: 0 })
doc.transact(() => text.insert(0, 'hello '), 'user:a')
doc.transact(() => text.insert(6, 'world'), 'user:b')
doc.transact(() => text.insert(11, '!'), 'user:a')
um.undo() // 'hello world'
um.undo() // 'world'
```

## Caveats

These are properties of the libraries, recorded so Phases 2, 3, and 12 do not
rediscover them.

- **reearth/ygo: the document lock is not re-entrant.** `Transact` holds the
  write lock for the whole closure. Do not call `ToString`, `Len`, `GetText`,
  `OnUpdate`, `ApplyUpdate`, or a nested `Transact` on the same document from
  inside one; it deadlocks silently. Read what you need before the
  transaction and mutate inside it, as the diff-apply sample does. Observer
  callbacks fire after the lock is released and may call anything.
- **reearth/ygo: origin tokens compare with `==`.** A string origin is fine.
  A pointer origin must point at a non-zero-size type, or distinct tokens may
  share an address and compare equal.
- **reearth/ygo: point-in-time snapshots need GC off.** `CaptureSnapshot` and
  `RestoreDocument` (a view of the document as it was, distinct from the
  compaction above) require `crdt.WithGC(false)`, which keeps deleted content.
  The reconciliation loop does not need this; the git history layer holds
  point-in-time text.
- **Deln0r/ygo: `EncodeDiff` re-emits the boundary block.** An update from a
  long-lived appender grows with its history. Convergence is unaffected. If
  this port is swapped in, size updates from the server accordingly or track
  per-transaction updates another way.
- **Both: text indexes are UTF-16 code units.** Go strings are UTF-8. Every
  index handed to `Insert` or `Delete` must be a UTF-16 offset, and a range
  must never split a surrogate pair. The harness has an `alignUTF16` helper
  for the random generator; production code derives offsets from the diff,
  which never splits a character.
- **Both: no rollback.** A panic or error inside a transaction commits the
  work done so far. Validate before mutating.

## What this settles for later phases

- Phase 2 renders `text.ToString()` to the file and applies file changes with
  the diff-apply sample under origin `filesystem`.
- Phase 3 relays the bytes from `OnUpdate` and `doc.on('update')` as-is; the
  server persists them per note and answers a connecting client's state
  vector with `EncodeStateAsUpdateV1(doc, remote)`.
- Phase 12 binds reearth/ygo through `mobile/crdt` and needs no Kotlin CRDT
  dependency.
