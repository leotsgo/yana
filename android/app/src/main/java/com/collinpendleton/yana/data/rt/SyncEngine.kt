package com.collinpendleton.yana.data.rt

import com.collinpendleton.yana.data.YanaClient
import com.collinpendleton.yana.data.normalizeServerUrl
import java.io.IOException
import kotlin.random.Random
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.channels.ClosedReceiveChannelException
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.withTimeoutOrNull

/** The connection's face in the UI: offline / syncing / live. */
enum class RtStatus { Offline, Syncing, Live }

/** What the relay tells the app beyond document bytes. */
sealed class RtEvent {
    data class Moved(val noteId: String, val path: String?) : RtEvent()

    data class Deleted(val noteId: String) : RtEvent()

    data class Error(val noteId: String?, val code: String, val reason: String) : RtEvent()

    /**
     * A note in a watched space changed (an edit, a move, a delete).
     * Carries no payload; a listing page refetches what it needs,
     * debounced — the tasks screen's live half.
     */
    data class Changed(val noteId: String, val path: String?) : RtEvent()

    /** A peer's awareness payload (cursor, presence), passed through unopened. */
    class Awareness(val noteId: String, val author: String?, val payload: ByteArray) : RtEvent()
}

/**
 * The realtime sync layer: one WebSocket per app, the note documents
 * subscribed over it, and the outbox that carries local edits to the
 * server no matter what happens to the process or the network.
 *
 * The shape follows the web client (web/src/sync.ts) and the protocol
 * in docs/realtime.md. Local edits queue in Room before they are sent,
 * leave the outbox only when a pong proves the server read the frame
 * that carried them, and are re-sent on reconnect when it never did —
 * CRDT idempotence makes the duplicate harmless. Reconnects back off
 * exponentially with jitter; on a healthy connection the send path
 * batches keystrokes on a 50ms timer, the same window the web client
 * uses.
 *
 * A note's document persists as one Room blob (`note_crdt.state`), the
 * compaction of everything applied or authored here; the outbox
 * (`crdt_outbox`) is the not-yet-confirmed tail. The two together mean
 * a day offline, a process death, or a server restart all converge
 * with nothing lost.
 *
 * Everything the engine owns runs on one thread (the scope it is
 * given is single-threaded); the few entry points called from elsewhere
 * touch shared state under a monitor that never suspends.
 */
class SyncEngine(
    private val client: YanaClient,
    private val store: RtStore,
    private val transport: RtTransport,
    private val docs: RtDocFactory,
    private val scope: CoroutineScope,
    private val random: Random = Random.Default,
    private val now: () -> Long = System::currentTimeMillis,
    private val timings: Timings = Timings(),
) {
    /**
     * The clockwork, overridable so the JVM tests run in milliseconds
     * instead of the production windows.
     */
    data class Timings(
        val flushMs: Long = 50,
        val backoffMinMs: Long = 500,
        val backoffMaxMs: Long = 8_000,
        val backoffJitterMs: Long = 250,
        val keepaliveMs: Long = 25_000,
        val persistDebounceMs: Long = 500,
        val bodyDebounceMs: Long = 1_000,
        val dialTimeoutMs: Long = 20_000,
        val backgroundTimeoutMs: Long = 90_000,
        /** How often the engine renews its own cursor and sweeps peers. */
        val presenceSweepMs: Long = 15_000,
        /** A peer silent for this long is pruned (the protocol's rule). */
        val presenceTimeoutMs: Long = 30_000,
    )

    private val _status = MutableStateFlow(RtStatus.Offline)
    val status: StateFlow<RtStatus> = _status.asStateFlow()

    private val _events = MutableSharedFlow<RtEvent>(extraBufferCapacity = 64)
    val events: SharedFlow<RtEvent> = _events.asSharedFlow()

    /** A document's text change, on its way from the doc thread to the engine's. */
    private class DocTextEvent(val noteId: String, val edit: TextEdit)

    /** Text changes arrive on the doc's drain thread and are applied in commit order here. */
    private val docTextEvents = Channel<DocTextEvent>(Channel.UNLIMITED)

    private var presenceJob: Job? = null

    init {
        scope.launch {
            for (e in docTextEvents) {
                val s = sync { sessions[e.noteId] } ?: continue
                s.onDocText(e.edit)
            }
        }
        // Presence has a 30-second life on receivers; renew ours and
        // prune theirs while any note is open.
        presenceJob = scope.launch {
            while (true) {
                delay(timings.presenceSweepMs)
                if (!wanted()) continue
                val nowMs = now()
                for (s in sync { sessions.values.toList() }) {
                    s.sweepPresence(nowMs)
                    if (s.awAnnounced) announcePresence(s)
                }
            }
        }
    }

    // Engine state, confined to the scope's single thread (or the monitor).
    private val sessions = LinkedHashMap<String, NoteSession>()
    private var sessionJob: Job? = null
    private var attempts = 0
    private var conn: RtTransport.Connection? = null
    private var connected = false
    private var lastSentAt = 0L
    private var maxSentSeq = -1L
    private val pingAcks = ArrayDeque<Long>()
    private val dirty = LinkedHashSet<String>()
    private var flushJob: Job? = null
    private var keepaliveJob: Job? = null

    /** The watched spaces, driving the connection alongside note sessions. */
    private val watches = LinkedHashSet<String>()

    /**
     * Opens (or joins) a note's live document. The handle's flows start
     * cold and fill in as the local state loads and the first
     * subscribe lands; `ready` says when the document's text — not a
     * cached REST copy — is what the screen should show.
     */
    fun open(noteId: String): NoteHandle {
        val s = sync {
            sessions[noteId]?.also { it.subscribers++ }
                ?: NoteSession(noteId).also { it.subscribers = 1; sessions[noteId] = it }
        }
        scope.launch {
            store.touchOpened(noteId)
            val loadedSession = loadSession(s)
            loadedSession?.let {
                maybeSubscribe(it)
                ensureSession()
            }
        }
        return NoteHandle(noteId, s.text, s.ready, s.everSynced, s.edits, s.presence, s.undoDepth, s.redoDepth)
    }

    /** Drops one reference to a note; the document closes when the last is gone. */
    fun close(noteId: String) {
        val s = sync { sessions[noteId] } ?: return
        s.subscribers--
        reap(noteId)
    }

    /**
     * Watches whole spaces for changes, the tasks screen's live half:
     * one `watch` frame per space on the engine's connection, and `chg`
     * frames back as [RtEvent.Changed] whenever a note in one of them
     * changes. The last watcher leaving with no note open puts the
     * connection away. Replaces the watched set, so the screens call it
     * with the spaces their filters name; an empty set stops watching.
     * The server caps a connection at 32 watched spaces, so beyond that
     * the first spaces (sorted) are watched and the rest wait for a
     * refresh.
     */
    fun watchSpaces(spaces: Set<String>) {
        val clean = spaces.filterTo(LinkedHashSet()) { it.isNotEmpty() }
        val (added, idle) = sync {
            val fresh = clean - watches
            watches.clear()
            watches.addAll(clean)
            fresh to !wanted()
        }
        scope.launch {
            if (added.isNotEmpty()) {
                for (s in added.sorted().take(MAX_WATCHED_SPACES)) send(ClientFrame(Rt.WATCH, s = s))
            }
            when {
                watches.isNotEmpty() -> ensureSession()
                idle -> conn?.close(1000, "idle")
            }
        }
    }

    /** The editor's edit path: mutate the body, queue the update, batch the send. */
    fun edit(noteId: String, want: String) {
        val s = sync { sessions[noteId] } ?: return
        scope.launch {
            val update = s.withDoc { it.replaceText(want) } ?: return@launch
            store.addOutboxUpdate(noteId, update)
            s.afterChange()
            dirty.add(noteId)
            scheduleFlush()
        }
    }

    /**
     * The editor's per-keystroke path: one replacement region — the
     * delete and the insert commit as one transaction, so it is one
     * update and one undo step.
     */
    fun editOp(noteId: String, pos: Int, del: Int, insert: String) {
        val s = sync { sessions[noteId] } ?: return
        scope.launch {
            val update = runCatching { s.withDoc { it.edit(pos, del, insert) } }.getOrNull() ?: return@launch
            if (update != null) store.addOutboxUpdate(noteId, update)
            s.afterChange()
            dirty.add(noteId)
            scheduleFlush()
        }
    }

    /** Reverts the most recent local edit. True when something was undone. */
    fun undo(noteId: String) {
        applyStackOp(noteId) { it.undo() }
    }

    /** Re-applies the most recently undone local edit. True when something was redone. */
    fun redo(noteId: String) {
        applyStackOp(noteId) { it.redo() }
    }

    private fun applyStackOp(noteId: String, op: (RtDoc) -> ByteArray?) {
        val s = sync { sessions[noteId] } ?: return
        scope.launch {
            val update = runCatching { s.withDoc { op(it) } }.getOrNull() ?: return@launch
            if (update != null) store.addOutboxUpdate(noteId, update)
            s.afterChange()
            dirty.add(noteId)
            scheduleFlush()
        }
    }

    /** Sends an awareness payload (presence, cursors) for a subscribed note. */
    fun sendAwareness(noteId: String, payload: ByteArray) {
        val s = sync { sessions[noteId] } ?: return
        scope.launch {
            if (!connected || !s.subdThisConnection || !s.isLoaded()) return@launch
            send(ClientFrame(Rt.AW, n = noteId, p = payload))
        }
    }

    /**
     * Publishes the local cursor for the note's room: the selection as
     * relative positions in the awareness state. The editor throttles
     * its calls; each one bumps the state clock the protocol requires.
     */
    fun sendCursor(noteId: String, anchor: Int, head: Int) {
        val s = sync { sessions[noteId] } ?: return
        scope.launch {
            val name = client.session.value?.username ?: return@launch
            val state = s.withDoc { d ->
                val a = d.positionJSON(anchor, 0) ?: return@withDoc null
                val h = d.positionJSON(head, 0) ?: return@withDoc null
                PresenceState.encode(presenceFor(name), a, h)
            } ?: return@launch
            s.awState = state
            s.awAnnounced = true
            announcePresence(s)
        }
    }

    /** Withdraws the local cursor — the null state a peer's view clears on. */
    fun sendCursorRemoval(noteId: String) {
        val s = sync { sessions[noteId] } ?: return
        scope.launch {
            if (!s.awAnnounced) return@launch
            s.awState = null
            announcePresence(s)
            s.awAnnounced = false
        }
    }

    /** Encodes and sends the session's awareness state, whatever it currently is. */
    private suspend fun announcePresence(s: NoteSession) {
        if (!connected || !s.subdThisConnection || !s.isLoaded()) return
        val payload = s.withDoc { d -> Awareness.encode(d.clientID(), ++s.awClock, s.awState) } ?: return
        send(ClientFrame(Rt.AW, n = s.noteId, p = payload))
    }

    /**
     * The background half: flush every unconfirmed update, then pull
     * deltas for the notes the person opened lately, updating the
     * replica and its search index. Returns false when it could not
     * finish in time; the caller retries on its own schedule.
     */
    suspend fun runBackgroundSync(timeoutMs: Long = timings.backgroundTimeoutMs): Boolean {
        val notes = (store.outboxNotes() + store.recentlyOpened(20)).distinct()
        if (notes.isEmpty()) return true
        val held = sync {
            notes.map { id ->
                val s = sessions[id] ?: NoteSession(id).also { sessions[id] = it }
                s.backgroundUse = true
                s
            }
        }
        held.forEach { s ->
            scope.launch {
                loadSession(s)?.let { maybeSubscribe(it) }
            }
        }
        try {
            ensureSession()
            return withTimeout(timeoutMs) {
                while (!(held.all { it.subdThisConnection } && store.outboxCount() == 0 && dirty.isEmpty())) {
                    if (!wanted()) break
                    delay(250)
                }
                true
            }
        } catch (_: kotlinx.coroutines.TimeoutCancellationException) {
            return false
        } finally {
            sync { held.forEach { it.backgroundUse = false } }
            held.forEach { reap(it.noteId) }
        }
    }

    /** Stops everything; documents persist on their way out. */
    fun shutdown() {
        val all = sync {
            val list = sessions.values.toList()
            sessions.clear()
            watches.clear()
            list
        }
        // The socket drops now, not on the engine's thread, so a
        // shutdown is not racing the process's exit. The next connect
        // resumes from state vectors, which is the design anyway.
        conn?.let { runCatching { it.cancel() } }
        conn = null
        connected = false
        keepaliveJob?.cancel()
        presenceJob?.cancel()
        _status.value = RtStatus.Offline
        scope.launch { all.forEach { it.closeDoc() } }
    }

    // --- sessions ------------------------------------------------------------

    /** One peer's awareness record: the gated state and when it was last seen. */
    private class PeerEntry(
        var clock: Long,
        var stateJson: String?,
        var lastSeen: Long,
        var user: PresenceUser? = null,
        var anchor: ByteArray? = null,
        var head: ByteArray? = null,
    )

    /**
     * One note: its document, its flows, and who wants it open. The
     * document loads from Room before anything else touches it, so the
     * first subscribe carries the state vector of everything this
     * device already holds — offline edits included.
     */
    private inner class NoteSession(val noteId: String) {
        @Volatile
        var subscribers = 0

        @Volatile
        var backgroundUse = false
        private var doc: RtDoc? = null
        private var loaded = false
        private var stopText: (() -> Unit)? = null

        /** The document's text, for the screen. */
        val text = MutableStateFlow("")

        /** True once the text is this device's document: loaded state or a synced subscribe. */
        val ready = MutableStateFlow(false)

        /** True once any connection completed this note's handshake. */
        val everSynced = MutableStateFlow(false)

        /** Every text change in commit order, for the editor; seq makes each one distinct. */
        val edits = MutableStateFlow<Pair<Long, TextEdit>?>(null)
        private var editSeq = 0L

        /** Other people in the note, cursors resolved to current offsets. */
        val presence = MutableStateFlow<Map<Long, PeerCursor>>(emptyMap())

        /** Undo and redo availability, for the editor's buttons. */
        val undoDepth = MutableStateFlow(0L)
        val redoDepth = MutableStateFlow(0L)

        @Volatile
        var subdThisConnection = false

        // Awareness state, confined to the engine's thread.
        var awClock = 0L
        var awState: String? = null
        var awAnnounced = false
        private val peers = LinkedHashMap<Long, PeerEntry>()

        private var persistJob: Job? = null
        private var bodyJob: Job? = null

        fun current(): Boolean = subscribers > 0 || backgroundUse

        fun isLoaded(): Boolean = loaded

        /** Loads (creating) the document, then publishes what it holds. */
        suspend fun load() {
            val state = store.crdtState(noteId)
            val d = docs.open(noteId, state ?: ByteArray(0))
            if (!sync { if (sessions[noteId] === this) { doc = d; loaded = true; true } else { false } }) {
                d.close()
                return
            }
            stopText = d.observe(
                object : RtTextObserver {
                    override fun onText(text: String, delta: String?, local: Boolean) {
                        docTextEvents.trySend(DocTextEvent(noteId, TextEdit(text, delta, local)))
                    }
                },
            )
            refreshDepths()
            if (state != null && state.size > 0) {
                text.value = d.text()
                ready.value = true
            }
        }

        /** A committed text change, already on the engine's thread and in order. */
        fun onDocText(e: TextEdit) {
            if (sync { sessions[noteId] !== this }) return
            text.value = e.text
            ready.value = true
            edits.value = ++editSeq to e
            resolvePresence()
            refreshDepths()
        }

        /** Runs [block] with the document when there is one and the session lives. */
        fun <T> withDoc(block: (RtDoc) -> T): T? {
            val d = sync { if (current()) doc else null } ?: return null
            return block(d)
        }

        private fun refreshDepths() {
            val d = sync { doc } ?: return
            runCatching {
                undoDepth.value = d.undoDepth()
                redoDepth.value = d.redoDepth()
            }
        }

        /** Applies one inbound awareness payload: clock gating, then re-resolution. */
        fun onAwareness(payload: ByteArray, nowMs: Long) {
            val entries = Awareness.decode(payload) ?: return
            val self = sync { doc }?.clientID()
            var changed = false
            for (e in entries) {
                if (e.clientID == self) continue
                val known = peers[e.clientID]
                if (known != null && e.clock < known.clock) continue
                if (e.clock == known?.clock && !(e.stateJson == null && known.stateJson != null)) continue
                val parsed = e.stateJson?.let(PresenceState::decode)
                val entry = known ?: PeerEntry(e.clock, e.stateJson, nowMs).also { peers[e.clientID] = it }
                entry.clock = e.clock
                entry.stateJson = e.stateJson
                entry.lastSeen = nowMs
                entry.user = parsed?.first
                entry.anchor = parsed?.second?.first
                entry.head = parsed?.second?.second
                changed = true
            }
            if (changed) resolvePresence()
        }

        /** Drops peers gone quiet; the receiver side of the 30-second rule. */
        fun sweepPresence(nowMs: Long) {
            val before = peers.size
            peers.entries.removeIf { nowMs - it.value.lastSeen >= timings.presenceTimeoutMs }
            if (peers.size != before) resolvePresence()
        }

        /** Rebuilds the presence map from the table against the current text. */
        private fun resolvePresence() {
            val d = sync { doc } ?: return
            val out = LinkedHashMap<Long, PeerCursor>()
            for ((client, p) in peers) {
                if (p.stateJson == null || p.user == null) continue
                fun at(bytes: ByteArray?): Int? =
                    bytes?.let { runCatching { d.resolvePosition(it) }.getOrDefault(-1) }?.takeIf { it >= 0 }
                out[client] =
                    PeerCursor(
                        clientID = client,
                        name = p.user?.name,
                        color = p.user?.color ?: "#666666",
                        colorLight = p.user?.colorLight ?: "#66666633",
                        anchor = at(p.anchor),
                        head = at(p.head),
                    )
            }
            presence.value = out
        }

        /** Publishes text and schedules persistence after any document change. */
        fun afterChange() {
            val d = sync { doc } ?: return
            text.value = d.text()
            ready.value = true
            persistJob?.cancel()
            persistJob = scope.launch {
                delay(timings.persistDebounceMs)
                runCatching { store.storeCrdtState(noteId, d.state()) }
            }
            bodyJob?.cancel()
            bodyJob = scope.launch {
                delay(timings.bodyDebounceMs)
                refreshReplicaBody(noteId, d.text())
            }
        }

        fun markSynced() {
            subdThisConnection = true
            everSynced.value = true
            afterChange()
            // A new connection starts blank everywhere: say again where
            // we are, as the web client re-announces on subscribe.
            if (awAnnounced) {
                scope.launch { announcePresence(this@NoteSession) }
            }
        }

        fun closeDoc() {
            val d = sync {
                stopText?.also { stopText = null }
                val had = doc
                doc = null
                loaded = false
                had
            } ?: return
            // Read the text now: the persist job closes the document, and
            // a closed one reads empty, which would blank the cached body.
            val last = d.text()
            persistJob?.cancel()
            persistJob = scope.launch {
                runCatching { store.storeCrdtState(noteId, d.state()) }
                d.close()
            }
            bodyJob?.cancel()
            bodyJob = scope.launch {
                delay(timings.bodyDebounceMs)
                refreshReplicaBody(noteId, last)
            }
        }

        /** Releases a document nothing could use, without writing state back. */
        fun discard() {
            val d = sync {
                stopText?.also { stopText = null }
                val had = doc
                doc = null
                loaded = false
                had
            } ?: return
            persistJob?.cancel()
            bodyJob?.cancel()
            d.close()
        }
    }

    /** What [open] hands the screen. */
    class NoteHandle(
        val noteId: String,
        val text: StateFlow<String>,
        val ready: StateFlow<Boolean>,
        val everSynced: StateFlow<Boolean>,
        /** Text changes in commit order (a sequence number paired with the edit). */
        val edits: StateFlow<Pair<Long, TextEdit>?> = MutableStateFlow(null),
        /** The other people editing, cursors at current offsets. */
        val presence: StateFlow<Map<Long, PeerCursor>> = MutableStateFlow(emptyMap()),
        val undoDepth: StateFlow<Long> = MutableStateFlow(0L),
        val redoDepth: StateFlow<Long> = MutableStateFlow(0L),
    )

    /** Loads a session's document, dropping it when the state will not load. */
    private suspend fun loadSession(s: NoteSession): NoteSession? = try {
        s.load()
        s
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        // A state that will not load drops the session rather than
        // wedging the note; the next open starts over.
        sync { if (sessions[s.noteId] === s) sessions.remove(s.noteId) }
        s.discard()
        null
    }

    /** Drops sessions nobody wants, flushing first whatever was never confirmed. */
    private fun reap(noteId: String) {
        val s = sync { sessions[noteId] } ?: return
        if (s.current()) return
        scope.launch {
            flushOne(noteId)
            // Leaving a room withdraws the cursor, as the web's editor
            // does when it closes.
            if (s.awAnnounced) {
                s.awState = null
                announcePresence(s)
                s.awAnnounced = false
            }
            if (connected) send(ClientFrame(Rt.UNSUB, n = noteId))
            sync { if (!s.current() && sessions[noteId] === s) sessions.remove(noteId) }
            refreshStatus()
            s.closeDoc()
        }
    }

    // --- the connection ------------------------------------------------------

    private fun wanted(): Boolean = sync { sessions.values.any { it.current() } || watches.isNotEmpty() }

    private fun ensureSession() {
        sync {
            if (sessionJob?.isActive == true) return
            sessionJob = scope.launch { sessionLoop() }
        }
    }

    private suspend fun sessionLoop() {
        while (true) {
            if (!wanted()) break
            val session = client.session.value ?: break
            _status.value = RtStatus.Syncing
            resetConnection()

            val token = try {
                client.auth.validAccessToken()
            } catch (_: IOException) {
                null
            }
            val base = normalizeServerUrl(session.server)
            if (token == null || base == null) {
                if (!backoff()) break
                continue
            }

            val frames = Channel<ByteArray>(Channel.UNLIMITED)
            val open = Channel<Unit>(1)
            val closed = Channel<CloseReason>(1)
            val c = transport.connect(
                wsUrl(base, token),
                listener = object : RtTransport.Listener {
                    override fun onOpen() {
                        open.trySend(Unit)
                    }

                    override fun onFrame(bytes: ByteArray) {
                        frames.trySend(bytes)
                    }

                    override fun onClosed(code: Int, reason: String) {
                        settle(CloseReason(code, reason, null))
                    }

                    override fun onFailure(t: Throwable?, httpCode: Int?) {
                        settle(CloseReason(1006, t?.message ?: "connection failed", httpCode))
                    }

                    fun settle(r: CloseReason) {
                        frames.close()
                        open.close()
                        closed.trySend(r)
                    }
                },
            )
            conn = c
            val opened = withTimeoutOrNull(timings.dialTimeoutMs) {
                try {
                    open.receive()
                    true
                } catch (_: ClosedReceiveChannelException) {
                    false
                }
            }
            if (opened == true) {
                onConnected()
                for (bytes in frames) {
                    handleFrame(bytes)
                }
            } else {
                c.close(1000, "dial timeout")
            }
            val reason = closed.maybeReceive() ?: CloseReason(1006, "connection ended", null)
            conn = null
            onDisconnected(reason)
            if (!wanted()) break
            if (!backoff()) break
        }
        resetConnection()
        val rest = sync {
            val list = sessions.values.toList()
            sessions.clear()
            list
        }
        rest.forEach { it.closeDoc() }
        _status.value = RtStatus.Offline
    }

    private class CloseReason(val code: Int, val message: String?, val httpCode: Int?)

    /** One connection's fresh start: nothing sent, nothing confirmed. */
    private fun resetConnection() {
        conn?.let { runCatching { it.close(1000, "reset") } }
        conn = null
        connected = false
        lastSentAt = 0L
        maxSentSeq = -1L
        pingAcks.clear()
        dirty.clear()
        keepaliveJob?.cancel()
        sync { sessions.values.forEach { it.subdThisConnection = false } }
    }

    private suspend fun onConnected() {
        connected = true
        refreshStatus()
        for (s in sync { sessions.values.filter { it.current() } }) {
            maybeSubscribe(s)
        }
        // A fresh connection watches nothing: the spaces the screens
        // asked for are asked for again, the way sessions resubscribe.
        for (space in sync { watches.toList() }.sorted().take(MAX_WATCHED_SPACES)) {
            send(ClientFrame(Rt.WATCH, s = space))
        }
        keepaliveJob = scope.launch {
            while (connected) {
                delay(timings.keepaliveMs)
                if (connected && now() - lastSentAt >= timings.keepaliveMs) sendPing()
            }
        }
    }

    /** Subscribes a loaded session on the live connection, once per connection. */
    private suspend fun maybeSubscribe(s: NoteSession) {
        if (!connected || s.subdThisConnection || !s.current() || !s.isLoaded()) return
        val sv = s.withDoc { it.stateVector() }
        if (!send(ClientFrame(Rt.SUB, n = s.noteId, sv = sv?.takeIf { it.isNotEmpty() }, a = author()))) return
        if (store.outboxFor(s.noteId).isNotEmpty()) {
            dirty.add(s.noteId)
            scheduleFlush()
        }
    }

    private fun onDisconnected(reason: CloseReason) {
        connected = false
        keepaliveJob?.cancel()
        _status.value = RtStatus.Offline
        // A rejection before any handshake is often an expired token;
        // minting a fresh one arms the next attempt. A refresh the
        // server refuses clears the session and the shell signs out.
        if (reason.httpCode == 401 || (!everSubdThisConnection() && reason.code != 1000)) {
            try {
                client.auth.validAccessToken()
            } catch (_: IOException) {
                // Unreachable; the reconnect loop keeps trying.
            } catch (_: Exception) {
            }
        }
    }

    /** Handles one inbound frame. Malformed frames are dropped, not fatal: the server closes those itself. */
    private suspend fun handleFrame(bytes: ByteArray) {
        val frame = decodeServerFrame(bytes) ?: return
        when (frame.t) {
            Rt.SUBD -> {
                val n = frame.n ?: return
                val s = sync { sessions[n] } ?: return
                frame.u?.let { u -> if (u.isNotEmpty()) s.withDoc { it.apply(u) } }
                attempts = 0
                s.markSynced()
                refreshStatus()
                dirty.add(n)
                flushOne(n)
            }
            Rt.UPD -> {
                val n = frame.n ?: return
                val u = frame.u ?: return
                if (u.isEmpty()) return
                val s = sync { sessions[n] } ?: return
                if (s.withDoc { it.apply(u) } == true) s.afterChange()
            }
            Rt.AW -> {
                val n = frame.n ?: return
                val p = frame.p ?: return
                _events.tryEmit(RtEvent.Awareness(n, frame.a, p))
                sync { sessions[n] }?.onAwareness(p, now())
            }
            Rt.PONG -> {
                val watermark = if (pingAcks.isEmpty()) null else pingAcks.removeFirst()
                if (watermark != null && watermark >= 0) store.dropOutboxTo(watermark)
                refreshStatus()
            }
            Rt.MOVED -> _events.tryEmit(RtEvent.Moved(frame.n ?: "", frame.path))
            Rt.DELETED -> _events.tryEmit(RtEvent.Deleted(frame.n ?: ""))
            Rt.ERR -> _events.tryEmit(RtEvent.Error(frame.n, frame.c ?: "error", frame.r ?: ""))
            // A watched space's change signal: the listing screens
            // refetch what they need, debounced. The own tick arrives
            // here too, which is what confirms the row's new state.
            Rt.CHANGED -> _events.tryEmit(RtEvent.Changed(frame.n ?: "", frame.path))
            Rt.WATCHED -> {}
        }
    }

    // --- sending -------------------------------------------------------------

    private fun send(f: ClientFrame): Boolean {
        val c = conn ?: return false
        val ok = c.send(encodeClientFrame(f))
        if (ok) lastSentAt = now()
        return ok
    }

    /**
     * Sends a ping and records the watermark it confirms: the server
     * reads frames in order, so its pong proves it applied every
     * update sent before the ping.
     */
    private fun sendPing() {
        val watermark = maxSentSeq
        if (send(ClientFrame(Rt.PING))) pingAcks.addLast(watermark)
    }

    /** Queues the 50ms batch window; one engine-wide timer serves every note. */
    private fun scheduleFlush() {
        if (flushJob?.isActive == true) return
        flushJob = scope.launch {
            delay(timings.flushMs)
            val notes = dirty.toList()
            for (n in notes) flushOne(n)
        }
    }

    /**
     * Sends one note's unconfirmed updates as a single merged payload,
     * then the ping that confirms them. The rows stay until the pong;
     * a connection that dies before then leaves them for the next one.
     */
    private suspend fun flushOne(noteId: String) {
        if (!connected) return
        val s = sync { sessions[noteId] } ?: return
        if (!s.subdThisConnection) return
        val rows = store.outboxFor(noteId)
        if (rows.isEmpty()) {
            dirty.remove(noteId)
            return
        }
        val merged = docs.mergeAll(rows.map { it.payload }) ?: return
        if (send(ClientFrame(Rt.UPD, n = noteId, u = merged, a = author()))) {
            maxSentSeq = maxOf(maxSentSeq, rows.maxOf { it.seq })
            dirty.remove(noteId)
            sendPing()
            refreshStatus()
        }
    }

    /** Live when everything this connection wants has settled. */
    private suspend fun refreshStatus() {
        if (!connected) return
        val quiet = sync { sessions.values.all { !it.current() || it.subdThisConnection } } &&
            dirty.isEmpty() &&
            store.outboxCount() == 0
        _status.value = if (quiet) RtStatus.Live else RtStatus.Syncing
    }

    private fun everSubdThisConnection(): Boolean = sync { sessions.values.any { it.subdThisConnection } }

    private fun author(): String? = client.session.value?.let { "user:" + it.username }

    /** Waits out the backoff window. */
    private suspend fun backoff(): Boolean {
        _status.value = RtStatus.Offline
        delay(backoffDelay(attempts, random, timings.backoffMinMs, timings.backoffMaxMs, timings.backoffJitterMs))
        attempts++
        return true
    }

    // --- replica refresh -------------------------------------------------------

    /** Keeps the offline replica and its search index in step with the document. */
    private suspend fun refreshReplicaBody(noteId: String, text: String) {
        val kind = if (store.noteKind(noteId) == "html") "html" else "md"
        store.storeBody(noteId, kind, text)
    }

    // --- small helpers --------------------------------------------------------

    private inline fun <T> sync(block: () -> T): T = synchronized(this, block)

    private suspend fun <T> Channel<T>.maybeReceive(): T? = try {
        receive()
    } catch (_: ClosedReceiveChannelException) {
        null
    }
}

/** The reconnect delay: 500ms doubling to 8s, plus jitter, matching the web client. */
fun backoffDelay(attempts: Int, random: Random, minMs: Long = 500, maxMs: Long = 8_000, jitterMs: Long = 250): Long =
    minOf(minMs shl attempts.coerceAtMost(10), maxMs) + (random.nextDouble() * jitterMs).toLong()

/** The relay's cap on watched spaces per connection (Hub.MaxSpacesPerConn). */
internal const val MAX_WATCHED_SPACES = 32
