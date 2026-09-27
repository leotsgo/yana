package com.collinpendleton.yana.data

import com.collinpendleton.yana.Prefs
import com.collinpendleton.yana.data.replica.CreateOp
import com.collinpendleton.yana.data.replica.RecentNoteRow
import com.collinpendleton.yana.data.replica.ReplicaStore
import com.collinpendleton.yana.data.rt.RtDocFactory
import com.collinpendleton.yana.data.rt.SyncEngine
import java.io.IOException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import retrofit2.HttpException

/** A note capture just made, offline or on. */
data class CapturedNote(
    val id: String,
    val path: String,
    val title: String,
    /** True when the note exists only in the replica so far. */
    val local: Boolean,
)

/** What a capture line or an append ended in. */
sealed interface AppendOutcome {
    /** The block is in the note's document; [local] when the server has not seen it yet. */
    data class Done(val local: Boolean) : AppendOutcome

    /** The note is not on this device as a document; it must be opened once first. */
    data object NotOnDevice : AppendOutcome

    /** No note with that id/path is known. */
    data object NotFound : AppendOutcome
}

/** Today's note, opened or made: the id to show, and where it came from. */
data class TodayNote(
    val id: String,
    val path: String,
    /** True when this device just made it (offline, in the replica). */
    val local: Boolean,
)

/**
 * Capture speed's engine room: the paths every fast entry point — the
 * share target, the tiles, the widget, Today and Capture — ends in.
 *
 * An offline compose never waits for the network. The note is minted
 * here with a ULID, lands in the replica with an empty document, and a
 * create op joins the queue; the editor opens on the local document
 * immediately. The content stays out of the create on purpose: the
 * server seeds a note's document from the file on first sight, so a
 * create that arrived with text would meet the same text authored again
 * by the editor's document, doubled. Empty creates converge; everything
 * the person wrote rides the document's own outbox.
 */
class CaptureNotes(
    private val repo: YanaNoteRepository,
    private val store: ReplicaStore,
    private val engine: SyncEngine,
    private val docs: RtDocFactory,
    private val prefs: Prefs,
    private val onOutboxGrew: () -> Unit = {},
    private val now: () -> Long = System::currentTimeMillis,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    /**
     * The space capture works in: the daily-space preference when it
     * names a real space, else the first one. Today, Capture, and the
     * inbox all follow it, the way the web's defaults follow its
     * preference.
     */
    suspend fun captureSpace(): String {
        val pref = prefs.dailySpace()
        val spaces = store.spaces().orEmpty()
        if (pref.isNotEmpty() && spaces.any { it.name == pref }) return pref
        return spaces.firstOrNull()?.name ?: ""
    }

    /**
     * The directory a new inbox note lands in — where shared photos
     * upload before their note exists.
     */
    suspend fun inboxBase(): String {
        val space = captureSpace()
        if (space.isEmpty()) return ""
        val folder = prefs.inboxFolder(space)
        return if (folder.isEmpty()) space else "$space/$folder"
    }

    /**
     * A new note in the space's inbox folder, created in the replica
     * with a client-minted ULID and an empty document that is ready to
     * type into right now. [initial] (a share's block) becomes the
     * document's first text as a local edit, so it merges rather than
     * meeting the server's own seeding. Returns null when there is no
     * space to capture into.
     */
    suspend fun newInboxNote(initial: String = ""): CapturedNote? {
        val base = inboxBase()
        if (base.isEmpty()) return null
        val space = captureSpace()
        val folder = prefs.inboxFolder(space)
        val taken = store.namesIn(space, folder)
        val name = CaptureKit.nextInboxName(taken)
        val path = "$base/$name.md"
        val id = CaptureKit.newUlid(now())
        createLocalNote(id, space, path, name, initial)
        // The network, when it is there, makes the note real for
        // everyone else; when it is not, the queue holds it.
        scope.launch { runCatching { repo.sync() } }
        return CapturedNote(id, path, name, local = true)
    }

    /**
     * Today's note, opened or made. Online the server's daily endpoint
     * answers (and seeds the template); offline the path comes from the
     * cached pattern and the note is made in the replica with a queued
     * create, the id stable across both.
     */
    suspend fun todayNote(): TodayNote? {
        val space = captureSpace()
        if (space.isEmpty()) return null
        val date = CaptureKit.today(now())
        try {
            val res = repo.openDaily(space, date)
            scope.launch { refreshPattern() }
            return TodayNote(res.id, res.path, local = false)
        } catch (e: YanaClient.NotSignedIn) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (e: HttpException) {
            throw e
        } catch (e: IOException) {
            // Offline: the cached pattern says where today's note lives.
            val path = CaptureKit.dailyPath(space, prefs.dailyPattern(), date)
            val existing = store.noteByPath(path)
            if (existing != null) return TodayNote(existing.id, existing.relPath, local = false)
            val name = path.substringAfterLast('/').removeSuffix(".md")
            val id = CaptureKit.newUlid(now())
            createLocalNote(id, space, path, name, "")
            scope.launch { runCatching { repo.sync() } }
            return TodayNote(id, path, local = true)
        }
    }

    /**
     * Capture proper: one line onto the end of today's note without
     * opening it, online or off.
     */
    suspend fun captureLine(text: String): AppendOutcome {
        val block = CaptureKit.captureBlock(text)
        val today = todayNote() ?: return AppendOutcome.NotFound
        return appendBlock(today.id, block)
    }

    /**
     * A block onto the end of a note's document — the web's
     * appendToNote. Opens the note's session, waits for its document
     * (the local state, or a first sync), appends as one local edit,
     * and closes; the outbox carries it to the server on its own
     * schedule. A note this device never opened as a document cannot be
     * appended to offline, exactly the web's rule.
     */
    suspend fun appendBlock(noteId: String, block: String): AppendOutcome {
        if (store.note(noteId) == null && store.crdtState(noteId) == null) return AppendOutcome.NotFound
        val handle = engine.open(noteId)
        try {
            val ready = withTimeoutOrNull(8_000) { handle.ready.first { it } }
            if (ready != true) return AppendOutcome.NotOnDevice
            val want = CaptureKit.appendBlock(handle.text.value, block)
            engine.edit(noteId, want)
            onOutboxGrew()
            // Give the reap-and-flush path its beat, then whatever is
            // still queued is genuinely waiting for the network.
            settle()
            return AppendOutcome.Done(local = store.outboxFor(noteId).isNotEmpty())
        } finally {
            engine.close(noteId)
        }
    }

    /** The notes the share target offers first: opened here lately, else touched last. */
    suspend fun recentNotes(limit: Int = 12): List<RecentNoteRow> =
        store.recentlyOpenedNotes(limit).ifEmpty { store.newestNotes(limit) }

    /** Where a note lives, for the word that says where a block landed. */
    suspend fun storePathOf(noteId: String): String? = store.note(noteId)?.relPath

    /** Brings the cached daily pattern home whenever the server is reachable. */
    suspend fun refreshPattern() {
        try {
            repo.dailyPattern()?.let { if (it.isNotEmpty()) prefs.setDailyPattern(it) }
        } catch (_: Exception) {
        }
    }

    /**
     * One note born local: the replica row, an empty document ready for
     * the editor (state a fresh document encoded, so "ready" is true
     * with nothing on the wire), the first text as a local edit, and
     * the create op that will carry the id to the server.
     */
    private suspend fun createLocalNote(id: String, space: String, path: String, title: String, initial: String) {
        val at = now()
        store.putLocalNote(id, space, path, title, "md", initial, at)
        store.enqueueCreate(CreateOp(space, path, "", id))
        val doc = docs.open(id, ByteArray(0))
        try {
            if (initial.isNotEmpty()) {
                val update = runCatching { doc.edit(0, 0, initial) }.getOrNull()
                if (update != null) store.addOutboxUpdate(id, update)
            }
            store.storeCrdtState(id, doc.state())
            store.touchOpened(id)
        } finally {
            doc.close()
        }
    }

    /** A beat for the reap-and-flush path to pick the append up before the caller looks away. */
    suspend fun settle() {
        delay(150)
    }
}
