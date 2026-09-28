# Android CRDT bind package

`mobile/crdt` is the Go package behind the Android app's CRDT engine. It
wraps the same pure-Go Yjs port the server runs, behind the flat type
surface `gomobile bind` can generate a Kotlin API from, and adds the one
piece the port's own mobile façade leaves out: an undo manager scoped to
this device, so one person's undo never reverts someone else's text.
Everything speaks the same V1 update bytes as the server and the web
client, with no translation layer. It also carries the markdown render
(the server's own goldmark engine), so the phone's reading view produces
the same HTML as the web's by construction. Background and rationale:
[docs/crdt-decision.md](../../docs/crdt-decision.md).

## Building the AAR

```sh
make android-crdt
```

The AAR lands at `android/crdt/libs/yana-crdt.aar`, where the Android
project's `:crdt` project publishes it to the app
([android/README.md](../../android/README.md#the-crdt-engine)). It
is a build artifact: not committed, rebuilt by `make android-crdt` and by
CI, which uploads it as a workflow artifact on every pull request that
touches `mobile/`.

The build needs a JDK (17+), the Android SDK, and `ANDROID_NDK_HOME`
pointing at an NDK. Pinned versions, kept identical in the `ci` job and
the `Makefile`:

| Piece | Version |
|---|---|
| gomobile / gobind | `v0.0.0-20260908204917-8b95e45f8d3e` (also pinned in `go.mod` via `tools.go`) |
| Android NDK | `29.0.14206865` |
| `minSdk` of the AAR | API 24 (`-androidapi 24`) |
| Java package | `com.collinpendleton.yana.crdt` |

## The surface

| Kotlin | What it does |
|---|---|
| `Crdt.newDoc()` / `Crdt.newDocWithClientID(id)` | create a document (id in [0, 2^53-1]) |
| `Crdt.loadDoc(state)` | create from a snapshot or full state |
| `doc.text()` / `doc.len()` | read the body; `len` is in UTF-16 code units |
| `doc.insert(pos, s)` / `doc.delete(pos, n)` | edit at a UTF-16 offset; returns the update bytes |
| `doc.edit(pos, del, s)` | replace `del` units at `pos` with `s` in one transaction: one update, one undo step — the shape a text field's diff produces |
| `doc.editMany(ops)` | several replacement hunks (`[{"p":..,"d":..,"i":..}]`, positions against the text as it stands, non-overlapping) in one transaction — one update, one undo step, the shape a formatting button produces |
| `doc.replaceText(want)` | diff-apply: mutate the body so it reads as `want`, keeping untouched characters' identity |
| `doc.applyUpdate(update)` | integrate a peer's update |
| `doc.stateVector()` / `doc.diff(remoteVector)` / `doc.state()` | delta handshake and snapshot |
| `Crdt.mergeUpdates(a, b)` | compact an update log without a document |
| `doc.observeUpdates(observer)` | `OnUpdate(update, local)` per committed transaction |
| `doc.observeText(observer)` | `OnText(text, delta, local)` per committed transaction; `delta` is the change as replacement hunks (`[{"p":3,"d":0,"i":5}]`) for cursor mapping |
| `Crdt.newUndoManager(doc)` | `undo()`, `redo()`, stack sizes, scoped to this device's edits |
| `doc.relativePositionJSON(index, assoc)` | a cursor anchor as the JSON relative position the web's awareness states carry |
| `doc.resolveRelativePositionJSON(json)` | resolve such a position against the current text; -1 when it cannot resolve |
| `Crdt.renderMarkdown(body)` | render a note body to HTML with the server's own goldmark engine, on the device |
| `Crdt.wikiLinks(body)` | the distinct raw `[[targets]]` of a body, newline-joined — the extraction the render performed |

Every mutating method returns the update it produced (nil when nothing
changed), so a sync client can forward it without a second encode; the
observer delivers the same incremental updates for the UI path, one per
transaction, never a re-emission of a whole block.

```kotlin
val doc = Crdt.loadDoc(stateBytes)
val undo = Crdt.newUndoManager(doc)
val sub = doc.observeUpdates(object : UpdateObserver {
    override fun onUpdate(update: ByteArray, local: Boolean) {
        if (local) socket.send(update) // remote ones already came from there
    }
})
doc.insert(doc.len(), "hello ")        // returns the same update too
undo.undo()                            // reverts only this device's edit
sub.close()
doc.close()
```

## Offsets are UTF-16 code units

`len`, `insert`, and `delete` count UTF-16 code units — the same unit as
Kotlin's `String.length` and `TextField` indices, and as the wire format.
A character outside the BMP (an emoji, say) counts as two. An offset must
never fall inside a surrogate pair: text-field selections never do, and
`replaceText` never splits one because it derives its offsets from a
character diff.

## Threading and lifecycle

Methods are safe from any thread but synchronous and blocking; keep
`applyUpdate` and the encode calls off the main thread. Observer
callbacks run on a background goroutine in commit order, holding no
locks; post to the app's dispatcher from them and return promptly. Call
`doc.close()` when a note closes — it stops the observers and undo
managers, and every later call is a safe no-op.

## Undo granularity

One edit is one undo step: each `insert`, `delete`, or `replaceText`
call undoes as a unit. (The port's capture-timeout grouping mis-handles
a group that starts at the client's first clock, so this package does
not expose it; an editor that wants coarser steps calls `undo` the extra
times, with its own grouping boundaries.) Edits integrated from a peer
are never on the stack.
