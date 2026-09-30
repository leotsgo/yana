package com.collinpendleton.yana.data.rt

import com.collinpendleton.yana.data.MemorySessionStore
import com.collinpendleton.yana.data.Session
import com.collinpendleton.yana.data.YanaClient
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.ConcurrentLinkedQueue
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicLong
import kotlin.random.Random
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.OkHttpClient
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/**
 * The sync engine against a scripted relay: MockWebServer speaks the
 * real WebSocket upgrade, the frames are the real msgpack wire, and
 * only the CRDT engine and the store are fakes (the AAR's native
 * libraries load on a device alone). The scenarios mirror the phase's
 * acceptance list: converge after a dead connection, merge both ways
 * after offline edits, and drain the outbox without a note open.
 */
class SyncEngineTest {
    private lateinit var server: MockWebServer
    private lateinit var store: FakeRtStore
    private lateinit var docs: FakeDocFactory
    private lateinit var scope: CoroutineScope
    private lateinit var engine: SyncEngine
    private val relays = ConcurrentLinkedQueue<FakeRelay>()
    private val liveRelays = CopyOnWriteArrayList<FakeRelay>()
    private val connections = AtomicLong()

    private val note = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

    @Before
    fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                connections.incrementAndGet()
                val relay = relays.poll() ?: FakeRelay()
                relay.server = serverTextRef
                liveRelays.add(relay)
                return MockResponse.Builder().webSocketUpgrade(relay).build()
            }
        }
        server.start()
        store = FakeRtStore()
        docs = FakeDocFactory()
        scope = CoroutineScope(SupervisorJob() + Dispatchers.Default.limitedParallelism(1))
        engine = SyncEngine(
            client = YanaClient(MemorySessionStore(session())),
            store = store,
            transport = OkHttpRtTransport(OkHttpClient()),
            docs = docs,
            scope = scope,
            random = Random(7),
            timings = SyncEngine.Timings(
                flushMs = 10,
                backoffMinMs = 5,
                backoffMaxMs = 40,
                backoffJitterMs = 2,
                keepaliveMs = 60_000,
                persistDebounceMs = 5,
                bodyDebounceMs = 10,
                dialTimeoutMs = 5_000,
                backgroundTimeoutMs = 5_000,
            ),
        )
    }

    @After
    fun stop() {
        engine.shutdown()
        server.close()
        scope.cancel()
    }

    /** What the server would send as the note's delta; the fake doc appends it. */
    @Volatile
    private var serverTextRef: String = ""

    private fun session() = Session(
        server = server.url("/").toString(),
        userId = "u1",
        username = "ada",
        isOwner = true,
        sessionId = "s1",
        accessToken = "access-1",
        accessExpiresAt = System.currentTimeMillis() + 3_600_000,
        refreshToken = "refresh-1",
    )

    private fun waitUntil(timeoutMs: Long = 5_000, cond: suspend () -> Boolean) {
        try {
            runBlocking {
                withTimeout(timeoutMs) {
                    while (!cond()) delay(10)
                }
            }
        } catch (e: kotlinx.coroutines.TimeoutCancellationException) {
            throw AssertionError(
                "wait timed out: status=${engine.status.value} conns=${connections.get()} relays=${liveRelays.map { it.frames.map { f -> f.t } }} outbox=${runBlocking { store.outboxCount() }}",
                e,
            )
        }
    }

    private fun <T> blocking(block: suspend () -> T): T = runBlocking { block() }

    @Test
    fun closingKeepsTheCachedBody() {
        serverTextRef = "kept after close"
        val handle = engine.open(note)
        waitUntil { handle.ready.value && handle.text.value.contains("kept after close") }
        engine.close(note)
        // Past the body debounce, the replica holds the text, not the
        // closed document's empty read.
        blocking { delay(200) }
        assertEquals("kept after close", store.bodies[note])
    }

    @Test
    fun subscribesWithStateVectorAndAppliesTheDelta() {
        serverTextRef = "hello from the server"
        val handle = engine.open(note)
        waitUntil { liveRelays.any { it.frames.any { f -> f.t == Rt.SUB } } }
        val sub = liveRelays.flatMap { it.frames }.first { it.t == Rt.SUB }
        assertEquals(note, sub.n)
        assertEquals("user:ada", sub.a)
        waitUntil { handle.ready.value && handle.text.value.contains("hello from the server") }
        waitUntil { engine.status.value == RtStatus.Live }
        engine.close(note)
    }

    @Test
    fun reconnectsAfterAKillAndLosesNothing() {
        // First relay takes the edit but never confirms it, then dies —
        // the server-killed-mid-typing case.
        val killer = FakeRelay(answerPings = false, closeAfterUpdate = true)
        relays.add(killer)
        val handle = engine.open(note)
        waitUntil { liveRelays.isNotEmpty() && killer.frames.any { it.t == Rt.SUB } }
        engine.edit(note, "typed while connected")
        waitUntil { killer.frames.any { it.t == Rt.UPD } }
        // The kill happens; the outbox row must survive it.
        waitUntil { connections.get() >= 2 }
        val second = liveRelays.drop(1).first()
        // On reconnect the unconfirmed update goes out again — the
        // duplicate is harmless, the loss would not be.
        waitUntil { second.frames.any { it.t == Rt.UPD && it.u?.decodeToString() == "typed while connected" } }
        waitUntil { store.outboxCount() == 0 }
        waitUntil { engine.status.value == RtStatus.Live }
        assertTrue(handle.text.value.contains("typed while connected"))
        engine.close(note)
    }

    @Test
    fun mergesBothDirectionsAfterOfflineEdits() {
        serverTextRef = "server side"
        engine.open(note)
        waitUntil { engine.status.value == RtStatus.Live }
        engine.shutdown()
        // The closed document's state write is queued behind the
        // shutdown; let every write from the first engine land before
        // the offline edits are seeded, so they cannot be overwritten.
        runBlocking { delay(500) }
        // Edits made while the socket is gone queue in the outbox.
        blocking { store.addOutboxUpdate(note, "offline edit".toByteArray()) }
        blocking { store.storeCrdtState(note, "offline edit".toByteArray()) }
        // A fresh engine (the app reopening) picks them up.
        val reopened = SyncEngine(
            client = YanaClient(MemorySessionStore(session())),
            store = store,
            transport = OkHttpRtTransport(OkHttpClient()),
            docs = docs,
            scope = scope,
            random = Random(7),
            timings = SyncEngine.Timings(
                flushMs = 10,
                backoffMinMs = 5,
                backoffMaxMs = 40,
                backoffJitterMs = 2,
                keepaliveMs = 60_000,
                persistDebounceMs = 5,
                bodyDebounceMs = 10,
                dialTimeoutMs = 5_000,
                backgroundTimeoutMs = 5_000,
            ),
        )
        try {
            val h2 = reopened.open(note)
            // The offline edit reaches the server...
            waitUntil {
                liveRelays.flatMap { it.frames }.any { it.t == Rt.UPD && it.u?.decodeToString() == "offline edit" }
            }
            // ...and the server's side arrives through the subscribe and merges.
            waitUntil { h2.text.value.contains("offline edit") && h2.text.value.contains("server side") }
            waitUntil { store.outboxCount() == 0 }
        } finally {
            reopened.shutdown()
        }
        engine.close(note)
    }

    @Test
    fun backgroundSyncDrainsTheOutboxWithoutAnOpenNote() {
        blocking { store.storeCrdtState(note, "queued work".toByteArray()) }
        blocking { store.addOutboxUpdate(note, "queued work".toByteArray()) }
        assertTrue(blocking { engine.runBackgroundSync() })
        waitUntil { liveRelays.any { r -> r.frames.any { it.t == Rt.UPD && it.u?.decodeToString() == "queued work" } } }
        waitUntil { store.outboxCount() == 0 }
    }

    @Test
    fun keepsStateAcrossAnEngineRestart() {
        val handle = engine.open(note)
        waitUntil { engine.status.value == RtStatus.Live }
        engine.edit(note, "persisted")
        waitUntil { handle.text.value.contains("persisted") }
        engine.close(note)
        waitUntil { store.states[note]?.decodeToString()?.contains("persisted") == true }
    }

    @Test
    fun editOpQueuesOneUpdateAndPublishesTheChange() {
        serverTextRef = "base"
        val handle = engine.open(note)
        waitUntil { engine.status.value == RtStatus.Live }
        engine.editOp(note, 4, 0, "!")
        waitUntil { store.outboxCount() == 0 }
        waitUntil { liveRelays.any { r -> r.frames.any { it.t == Rt.UPD && it.u?.decodeToString() == "e:4:0:!" } } }
        // The change arrives on the edits flow, local, with its hunks.
        waitUntil { handle.edits.value != null && handle.edits.value!!.second.local }
        val edit = handle.edits.value!!.second
        assertEquals("""[{"p":4,"d":0,"i":1}]""", edit.delta)
        assertEquals("base!", handle.text.value)
        waitUntil { handle.undoDepth.value == 1L }
        engine.close(note)
    }

    @Test
    fun editOpsCommitsSeveralHunksAsOneUpdateAndOneUndoStep() {
        serverTextRef = "one two three"
        val handle = engine.open(note)
        waitUntil { engine.status.value == RtStatus.Live }
        // The formatting bar's shape: two hunks, one transaction.
        engine.editOps(note, """[{"p":0,"d":0,"i":"**"},{"p":13,"d":0,"i":"**"}]""")
        waitUntil { store.outboxCount() == 0 }
        waitUntil { handle.text.value == "**one two three**" }
        // One undo step reverts the whole wrap at once.
        waitUntil { handle.undoDepth.value == 1L }
        engine.undo(note)
        waitUntil { handle.text.value == "one two three" }
        waitUntil { handle.undoDepth.value == 0L }
        engine.close(note)
    }

    @Test
    fun undoRevertsThisDevicesWorkOnly() {
        serverTextRef = "server text "
        val handle = engine.open(note)
        waitUntil { engine.status.value == RtStatus.Live }
        waitUntil { handle.text.value == "server text " }
        engine.editOp(note, 12, 0, "and mine")
        waitUntil { handle.text.value == "server text and mine" }
        engine.undo(note)
        // The server's text survives; only the local edit reverts.
        waitUntil { handle.text.value == "server text " }
        waitUntil { handle.edits.value != null && handle.edits.value!!.second.local }
        // The undo itself is an update the server gets.
        waitUntil { store.outboxCount() == 0 }
        engine.redo(note)
        waitUntil { handle.text.value == "server text and mine" }
        engine.close(note)
    }

    @Test
    fun aPeersCursorArrivesAndClears() {
        serverTextRef = "hello world"
        val handle = engine.open(note)
        waitUntil { engine.status.value == RtStatus.Live }
        val state = PresenceState.encode(
            presenceFor("wren"),
            """{"type":null,"tname":"body","item":{"client":42,"clock":2},"assoc":0}""".toByteArray(),
            """{"type":null,"tname":"body","item":{"client":42,"clock":4},"assoc":0}""".toByteArray(),
        )
        val payload = Awareness.encode(42, 1, state)
        val decoded = Awareness.decode(payload)
        check(decoded != null && decoded.single().clientID == 42L) { "codec self-test failed: $decoded" }
        liveRelays.first().push(ServerFrame(t = Rt.AW, n = note, p = payload))
        waitUntil { handle.presence.value.containsKey(42L) }
        val peer = handle.presence.value.getValue(42L)
        assertEquals("wren", peer.name)
        assertEquals(2, peer.anchor)
        assertEquals(4, peer.head)
        assertTrue(peer.selection)
        // The peer leaving is the null state with a higher clock.
        liveRelays.first().push(ServerFrame(t = Rt.AW, n = note, p = Awareness.encode(42, 2, null)))
        waitUntil { !handle.presence.value.containsKey(42L) }
        // A stale replay of the old clock is ignored.
        liveRelays.first().push(ServerFrame(t = Rt.AW, n = note, p = Awareness.encode(42, 1, state)))
        blocking { delay(300) }
        assertEquals(false, handle.presence.value.containsKey(42L))
        engine.close(note)
    }

    @Test
    fun ourCursorBroadcastsAndWithdraws() {
        serverTextRef = "hello"
        engine.open(note)
        waitUntil { engine.status.value == RtStatus.Live }
        engine.sendCursor(note, 1, 1)
        waitUntil { liveRelays.flatMap { it.frames }.any { it.t == Rt.AW } }
        val aw = liveRelays.flatMap { it.frames }.last { it.t == Rt.AW }
        val entry = Awareness.decode(aw.p!!)!!.single()
        assertEquals(1234L, entry.clientID)
        assertEquals(1L, entry.clock)
        val (user, _) = PresenceState.decode(entry.stateJson!!)!!
        assertEquals("ada", user.name)
        engine.sendCursorRemoval(note)
        waitUntil {
            liveRelays.flatMap { it.frames }.filter { it.t == Rt.AW }.any { f ->
                Awareness.decode(f.p!!)?.singleOrNull()?.stateJson == null
            }
        }
        engine.close(note)
    }

    @Test
    fun watchesSpacesWithoutANoteOpen() {
        // Collect on another thread: the engine's scope is single-threaded
        // and the event flow has no replay, so a collector queued behind
        // engine work would miss the frame.
        val seen = CopyOnWriteArrayList<RtEvent>()
        val collector = CoroutineScope(SupervisorJob() + Dispatchers.Default).launch {
            engine.events.collect { seen.add(it) }
        }
        try {
            // No note is open; the watch alone brings the connection up.
            engine.watchSpaces(setOf("work"))
            waitUntil { liveRelays.any { r -> r.frames.any { it.t == Rt.WATCH } } }
            val watch = liveRelays.flatMap { it.frames }.first { it.t == Rt.WATCH }
            assertEquals("work", watch.s)
            waitUntil { engine.status.value == RtStatus.Live }

            // A change signal in the watched space arrives as an event,
            // the listing screens' refetch trigger.
            liveRelays.last().push(ServerFrame(t = Rt.CHANGED, n = note, path = "work/todo.md"))
            waitUntil { seen.any { it is RtEvent.Changed && it.noteId == note && it.path == "work/todo.md" } }

            // The last watcher leaving with no note open puts the socket away.
            engine.watchSpaces(emptySet())
            waitUntil { engine.status.value == RtStatus.Offline }
        } finally {
            collector.cancel()
        }
    }

    @Test
    fun rewatchesAfterAReconnect() {
        engine.watchSpaces(setOf("work"))
        waitUntil { liveRelays.any { r -> r.frames.any { it.t == Rt.WATCH } } }
        // The connection dies; the watch outlives it and is asked for
        // again on the next one.
        liveRelays.last().ws?.close(1001, "gone")
        waitUntil { connections.get() >= 2 }
        waitUntil { liveRelays.drop(1).any { r -> r.frames.any { it.t == Rt.WATCH } } }
        engine.watchSpaces(emptySet())
        waitUntil { engine.status.value == RtStatus.Offline }
    }

    @Test
    fun stalePeersAreSwept() {
        var clock = 1_000_000L
        val e2 = SyncEngine(
            client = YanaClient(MemorySessionStore(session())),
            store = store,
            transport = OkHttpRtTransport(OkHttpClient()),
            docs = docs,
            scope = scope,
            random = Random(7),
            now = { clock },
            timings = SyncEngine.Timings(
                flushMs = 10,
                backoffMinMs = 5,
                backoffMaxMs = 40,
                backoffJitterMs = 2,
                keepaliveMs = 60_000,
                persistDebounceMs = 5,
                bodyDebounceMs = 10,
                dialTimeoutMs = 5_000,
                backgroundTimeoutMs = 5_000,
                presenceSweepMs = 25,
                presenceTimeoutMs = 100,
            ),
        )
        try {
            val handle = e2.open(note)
            waitUntil { e2.status.value == RtStatus.Live }
            liveRelays.last().push(
                ServerFrame(
                    t = Rt.AW,
                    n = note,
                    p = Awareness.encode(42, 1, PresenceState.encode(presenceFor("wren"), null, null)),
                ),
            )
            waitUntil { handle.presence.value.containsKey(42L) }
            clock += 200
            waitUntil { !handle.presence.value.containsKey(42L) }
        } finally {
            e2.shutdown()
        }
    }


    /** The relay: answers subs with the delta, pings with pongs, and can be told to die. */
    private class FakeRelay(
        var answerPings: Boolean = true,
        var closeAfterUpdate: Boolean = false,
    ) : WebSocketListener() {
        val frames = CopyOnWriteArrayList<ClientFrame>()
        var server: String = ""

        @Volatile
        var ws: WebSocket? = null

        override fun onOpen(webSocket: WebSocket, response: okhttp3.Response) {
            ws = webSocket
        }

        /** Answers the close handshake, the way a real server does. */
        override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
            webSocket.close(code, reason)
        }

        /** Pushes a frame to the client, as the server would. */
        fun push(f: ServerFrame) {
            ws?.send(encodeServerFrame(f).toByteString())
        }

        override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
            val f = decodeClientFrame(bytes.toByteArray()) ?: return
            frames.add(f)
            when (f.t) {
                Rt.SUB -> webSocket.send(
                    encodeServerFrame(ServerFrame(t = Rt.SUBD, n = f.n, u = server.toByteArray())).toByteString(),
                )
                Rt.PING -> if (answerPings) webSocket.send(encodeServerFrame(ServerFrame(t = Rt.PONG)).toByteString())
                Rt.UPD -> if (closeAfterUpdate) {
                    webSocket.close(1001, "killed")
                    closeAfterUpdate = false
                }
            }
        }
    }
}

/**
 * Documents where updates are appended text — enough for flow, not for
 * CRDT math. The editor surface (edit/undo/redo/observe/positions) is
 * real enough to drive the engine: a string with an op stack, text
 * events with hunks, and positions that are the index in disguise.
 */
private class FakeDoc(override val noteId: String, state: ByteArray) : RtDoc {
    var text = state.decodeToString()
    private val observers = CopyOnWriteArrayList<RtTextObserver>()
    private val undoOps = ArrayDeque<Pair<String, String>>() // text after -> text before
    private val redoOps = ArrayDeque<Pair<String, String>>()

    override fun text(): String = text

    override fun stateVector(): ByteArray = byteArrayOf(text.length.toByte())

    override fun state(): ByteArray = text.toByteArray()

    override fun apply(update: ByteArray): Boolean {
        if (update.isEmpty()) return false
        val before = text
        text += update.decodeToString()
        notify(before, local = false)
        return true
    }

    override fun replaceText(want: String): ByteArray? {
        if (want.length < text.length || want == text) return null
        val before = text
        val update = want.substring(text.length).toByteArray()
        text = want
        undoOps.addLast(want to before)
        notify(before, local = true)
        return update
    }

    override fun edit(pos: Int, del: Int, insert: String): ByteArray? {
        if (del == 0 && insert.isEmpty()) return null
        require(pos + del <= text.length) { "edit out of range" }
        val before = text
        text = text.substring(0, pos) + insert + text.substring(pos + del)
        undoOps.addLast(text to before)
        val update = "e:$pos:$del:$insert".toByteArray()
        notify(before, local = true)
        return update
    }

    override fun editOps(ops: String): ByteArray? {
        val parsed = kotlinx.serialization.json.Json.parseToJsonElement(ops)
        val hunks = parsed.jsonArray.map { h ->
            val o = h.jsonObject
            Triple(
                o.getValue("p").jsonPrimitive.int,
                o.getValue("d").jsonPrimitive.int,
                o.getValue("i").jsonPrimitive.content,
            )
        }.sortedByDescending { it.first }
        if (hunks.isEmpty()) return null
        val before = text
        val b = StringBuilder(text)
        for ((p, d, i) in hunks) {
            require(p >= 0 && p + d <= b.length) { "edit out of range" }
            b.replace(p, p + d, i)
        }
        if (b.toString() == before) return null
        text = b.toString()
        undoOps.addLast(text to before)
        val update = "m:$ops".toByteArray()
        notify(before, local = true)
        return update
    }

    override fun undo(): ByteArray? {
        val (after, before) = undoOps.removeLastOrNull() ?: return null
        text = before
        redoOps.addLast(before to after)
        notify(after, local = true)
        return "u".toByteArray()
    }

    override fun redo(): ByteArray? {
        val (before, after) = redoOps.removeLastOrNull() ?: return null
        text = after
        undoOps.addLast(after to before)
        notify(before, local = true)
        return "r".toByteArray()
    }

    override fun undoDepth(): Long = undoOps.size.toLong()

    override fun redoDepth(): Long = redoOps.size.toLong()

    override fun observe(observer: RtTextObserver): () -> Unit {
        observers.add(observer)
        return { observers.remove(observer) }
    }

    override fun clientID(): Long = 1234L

    // Positions are the real JSON shape, with the index smuggled into
    // the item's clock.
    override fun positionJSON(index: Int, assoc: Int): ByteArray =
        """{"type":null,"tname":"body","item":{"client":1234,"clock":$index},"assoc":0}""".toByteArray()

    override fun resolvePosition(json: ByteArray): Int = runCatching {
        val s = json.decodeToString()
        s.substringAfter("\"clock\":").substringBefore("}").toInt()
    }.getOrDefault(-1)

    // The Go document frees its memory on close and reads empty after.
    override fun close() {
        text = ""
    }

    private fun notify(before: String, local: Boolean) {
        val delta = hunksBetween(before, text)
        for (o in observers) o.onText(text, delta, local)
    }

    /** The one replacement region between two strings, as the real doc's hunks. */
    private fun hunksBetween(old: String, new: String): String? {
        if (old == new) return null
        var p = 0
        val minLen = minOf(old.length, new.length)
        while (p < minLen && old[p] == new[p]) p++
        var sfx = 0
        while (sfx < minLen - p && old[old.length - 1 - sfx] == new[new.length - 1 - sfx]) sfx++
        val del = old.length - p - sfx
        val ins = new.length - p - sfx
        return """[{"p":$p,"d":$del,"i":$ins}]"""
    }
}

private class FakeDocFactory : RtDocFactory {
    override fun open(noteId: String, state: ByteArray): RtDoc = FakeDoc(noteId, state)

    override fun merge(a: ByteArray, b: ByteArray): ByteArray = a + b
}

/** The store in memory: state blobs, the outbox, and the cached bodies. */
private class FakeRtStore : RtStore {
    val states = ConcurrentHashMap<String, ByteArray>()
    val bodies = ConcurrentHashMap<String, String>()

    private class Row(val noteId: String, val seq: Long, val payload: ByteArray)

    private val rows = ConcurrentLinkedQueue<Row>()
    private val seq = AtomicLong()
    private val opened = ConcurrentHashMap<String, Long>()

    override suspend fun crdtState(noteId: String): ByteArray? = states[noteId]

    override suspend fun storeCrdtState(noteId: String, state: ByteArray) {
        states[noteId] = state
    }

    override suspend fun touchOpened(noteId: String) {
        opened[noteId] = System.currentTimeMillis()
    }

    override suspend fun recentlyOpened(limit: Int): List<String> =
        opened.entries.sortedByDescending { it.value }.take(limit).map { it.key }

    override suspend fun addOutboxUpdate(noteId: String, update: ByteArray) {
        rows.add(Row(noteId, seq.incrementAndGet(), update))
    }

    override suspend fun outboxFor(noteId: String): List<OutboxRow> =
        rows.filter { it.noteId == noteId }.map { OutboxRow(it.seq, it.payload) }

    override suspend fun outboxNotes(): List<String> = rows.map { it.noteId }.distinct()

    override suspend fun dropOutboxTo(watermark: Long) {
        rows.removeIf { it.seq <= watermark }
    }

    override suspend fun outboxCount(): Int = rows.size

    override suspend fun noteKind(noteId: String): String? = "md"

    override suspend fun storeBody(noteId: String, kind: String, text: String) {
        bodies[noteId] = text
    }
}
