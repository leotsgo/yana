package com.collinpendleton.yana

import android.app.Application
import android.content.Context
import androidx.core.content.edit
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import com.collinpendleton.yana.data.CaptureKit
import com.collinpendleton.yana.data.CaptureNotes
import com.collinpendleton.yana.data.EncryptedSessionStore
import com.collinpendleton.yana.data.ImageAssets
import com.collinpendleton.yana.data.PendingUploadStore
import com.collinpendleton.yana.data.SyncScheduler
import com.collinpendleton.yana.data.YanaClient
import com.collinpendleton.yana.data.YanaNoteRepository
import com.collinpendleton.yana.data.replica.ReplicaStore
import com.collinpendleton.yana.data.rt.CrdtSyncScheduler
import com.collinpendleton.yana.data.rt.GoDocFactory
import com.collinpendleton.yana.data.rt.OkHttpRtTransport
import com.collinpendleton.yana.data.rt.RtStatus
import com.collinpendleton.yana.data.rt.SyncEngine
import com.collinpendleton.yana.ui.theme.ThemeMode
import java.io.File
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * Holds the app's single client, its offline replica, its realtime
 * sync engine, and its plain (non-secret) preferences. The replica
 * belongs to the signed-in account: a different account (or server)
 * arriving wipes it, and so does signing out.
 */
class YanaApp : Application() {
    lateinit var client: YanaClient
        private set
    lateinit var store: ReplicaStore
        private set
    lateinit var repo: YanaNoteRepository
        private set
    lateinit var syncEngine: SyncEngine
        private set
    lateinit var capture: CaptureNotes
        private set
    lateinit var images: ImageAssets
        private set
    lateinit var prefs: Prefs
        private set

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val syncScope = CoroutineScope(SupervisorJob() + Dispatchers.IO.limitedParallelism(1))

    /** The app-wide scope entry points launch capture work on. */
    val appScope: CoroutineScope get() = scope

    override fun onCreate() {
        super.onCreate()
        client = YanaClient(EncryptedSessionStore(this))
        store = ReplicaStore.open(this)
        syncEngine = SyncEngine(
            client = client,
            store = store,
            transport = OkHttpRtTransport(client.socketClient()),
            docs = GoDocFactory(),
            scope = syncScope,
        )
        val pendingUploads = PendingUploadStore(File(getFilesDir(), "pending-uploads"))
        repo = YanaNoteRepository(
            client,
            store,
            uploads = pendingUploads,
            fixUploadLink = { noteId, askedFor, written ->
                com.collinpendleton.yana.data.fixUploadLink(syncEngine, noteId, askedFor, written)
            },
        )
        images = ImageAssets(contentResolver, pendingUploads)
        prefs = Prefs(this)
        capture = CaptureNotes(
            repo = repo,
            store = store,
            engine = syncEngine,
            docs = GoDocFactory(),
            prefs = prefs,
            onOutboxGrew = { CrdtSyncScheduler.flushIfPending(this, true) },
        )
        scope.launch {
            client.session.collect { s ->
                if (s == null) {
                    prefs.clearRecents()
                    store.wipe()
                    syncEngine.shutdown()
                }
            }
        }
        // A connection coming back replays whatever the offline queue
        // holds — a tick from airplane mode, a create, a move — the
        // catch-up the web's outbox does on reconnect. The engine's
        // own outbox (capture lines, offline edits) follows the same
        // beat, so a note captured offline lands without waiting for a
        // worker window.
        scope.launch {
            var prev: RtStatus? = null
            syncEngine.status.collect { s ->
                val wasOffline = prev == RtStatus.Offline
                prev = s
                if (wasOffline && s != RtStatus.Offline && client.session.value != null) {
                    runCatching { repo.sync() }
                    runCatching { syncEngine.runBackgroundSync() }
                }
            }
        }
        // Edits queued by an earlier life (a crash, a force stop) head
        // out as soon as the network allows, without a note being opened.
        scope.launch {
            CrdtSyncScheduler.flushIfPending(this@YanaApp, store.outboxCount() > 0)
        }
        // Going to the background with unconfirmed edits hands them to
        // the expedited worker; the process may not come back for them.
        ProcessLifecycleOwner.get().lifecycle.addObserver(
            object : DefaultLifecycleObserver {
                override fun onStop(owner: LifecycleOwner) {
                    scope.launch {
                        CrdtSyncScheduler.flushIfPending(this@YanaApp, store.outboxCount() > 0)
                    }
                }
            },
        )
        SyncScheduler.schedule(this)
        CrdtSyncScheduler.schedule(this)
        // Staged uploads no queued op names (a wiped queue, a crash
        // between the two writes) leave with the next start.
        scope.launch {
            runCatching { images.staged.sweep(store.pendingUploads().map { it.file }.toSet()) }
        }
        // The daily-note pattern drifts rarely; whenever the server is
        // reachable at all, today's offline path knows where to look.
        scope.launch { capture.refreshPattern() }
    }
}

/** Device preferences that are not secrets. */
class Prefs(context: Context) {
    private val sp = context.getSharedPreferences("yana_prefs", Context.MODE_PRIVATE)
    private val theme = MutableStateFlow(
        runCatching { ThemeMode.valueOf(sp.getString("theme", null) ?: "") }.getOrDefault(ThemeMode.System),
    )
    val themeMode: StateFlow<ThemeMode> = theme.asStateFlow()

    fun setThemeMode(mode: ThemeMode) {
        sp.edit { putString("theme", mode.name) }
        theme.value = mode
    }

    /**
     * When this device last looked at the activity feed, in epoch
     * milliseconds; null when it never has. Kept per device, the way
     * the web keeps it per browser, so "since I last looked" is this
     * screen's own news.
     */
    fun activitySeen(): Long? = sp.getLong("activity.seen", 0L).takeIf { it > 0 }

    /** Opening the feed is the marker: everything before now is old news next time. */
    fun touchActivitySeen() {
        sp.edit { putLong("activity.seen", System.currentTimeMillis()) }
    }

    // --- capture -----------------------------------------------------------------

    /**
     * The space Today, Capture, and fast new notes work in; empty
     * follows the first space, the web's defaultSpace fallback.
     */
    fun dailySpace(): String = sp.getString("daily.space", null) ?: ""

    fun setDailySpace(space: String) {
        sp.edit { putString("daily.space", space) }
    }

    /**
     * The folder a fast new note lands in under one space; empty means
     * the space's top level. The default is `inbox`.
     */
    fun inboxFolder(space: String): String =
        sp.getString("inbox.$space", null) ?: CaptureKit.DEFAULT_INBOX

    fun setInboxFolder(space: String, folder: String) {
        sp.edit { putString("inbox.$space", folder.trim().trim('/')) }
    }

    /**
     * The server's daily-note pattern, cached so an offline Today knows
     * the path; the server's own default until it has answered.
     */
    fun dailyPattern(): String = sp.getString("daily.pattern", null) ?: CaptureKit.DEFAULT_DAILY_PATTERN

    fun setDailyPattern(pattern: String) {
        if (pattern.isNotEmpty()) sp.edit { putString("daily.pattern", pattern) }
    }

    // --- getting back to notes ---------------------------------------------------

    private val recentList = MutableStateFlow(readRecents())

    /** The notes this device opened last, newest first; home lists them. */
    val recents: StateFlow<List<RecentNote>> = recentList.asStateFlow()

    /** Puts a note at the top of the recents, dropping the oldest past [MAX_RECENTS]. */
    fun touchRecent(id: String, title: String) {
        val clean = title.replace('\t', ' ').replace('\n', ' ').trim()
        val next = (listOf(RecentNote(id, clean)) + recentList.value.filter { it.id != id }).take(MAX_RECENTS)
        if (next == recentList.value) return
        sp.edit { putString("recents", next.joinToString("\n") { it.id + "\t" + it.title }) }
        recentList.value = next
    }

    /** Forgets the recents; a sign-out, since they name the account's notes. */
    fun clearRecents() {
        sp.edit { remove("recents") }
        recentList.value = emptyList()
    }

    private fun readRecents(): List<RecentNote> =
        sp.getString("recents", null).orEmpty().lines().mapNotNull { line ->
            val tab = line.indexOf('\t')
            if (tab <= 0) null else RecentNote(line.substring(0, tab), line.substring(tab + 1))
        }

    /** The folders left open in one space's tree, so it reopens as it was left. */
    fun openFolders(space: String): Set<String> = sp.getStringSet("open.$space", null)?.toSet() ?: emptySet()

    fun setOpenFolders(space: String, paths: Set<String>) {
        sp.edit { putStringSet("open.$space", paths) }
    }

    companion object {
        const val MAX_RECENTS = 8
    }
}

/** A recently opened note: enough to list it and open it again. */
data class RecentNote(val id: String, val title: String)

val Context.yana: YanaApp get() = applicationContext as YanaApp
