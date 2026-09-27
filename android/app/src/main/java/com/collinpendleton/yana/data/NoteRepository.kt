package com.collinpendleton.yana.data

import com.collinpendleton.yana.data.replica.AppendOp
import com.collinpendleton.yana.data.replica.CreateOp
import com.collinpendleton.yana.data.replica.LocalHit
import com.collinpendleton.yana.data.replica.MoveOp
import com.collinpendleton.yana.data.replica.OpPayload
import com.collinpendleton.yana.data.replica.ReplicaStore
import com.collinpendleton.yana.data.replica.TaskOp
import com.collinpendleton.yana.data.replica.UploadOp
import com.collinpendleton.yana.data.search.parseQuery
import java.io.IOException
import java.time.Instant
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.Flow
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.RequestBody.Companion.toRequestBody
import retrofit2.HttpException

/**
 * The one door the screens go through for notes: the server when it can
 * be reached, the replica when it cannot. Nothing below the UI talks to
 * Room or REST directly — the editor and the capture features join the
 * navigation shell here.
 */
interface NoteRepository {
    /** Pulls spaces, the note list, and the tree; replays pending ops. */
    suspend fun sync()

    /** The account's spaces, from the replica after a sync when offline. */
    suspend fun spaces(): List<Space>

    /** One space's folder tree, from the replica when offline. */
    suspend fun tree(space: String): List<TreeNode>

    /**
     * One note: fetched and its body cached when online, read from the
     * cache when the server is out of reach.
     */
    suspend fun note(id: String): Note

    /**
     * The note as this device last cached it, without the network, or
     * null if its body was never fetched (or is empty). A screen shows it at once and
     * replaces it when [note] answers.
     */
    suspend fun cachedNote(id: String): Note?

    /**
     * The signed content-origin URL an HTML note renders in. Tokens
     * live minutes, so every open mints a fresh one; offline there is
     * no rendered view and the caller shows the source as text.
     */
    suspend fun noteView(id: String): NoteView

    /** Saves an HTML note's source, whole-file and last-write-wins. */
    suspend fun saveSource(id: String, source: String, baseHash: String): SaveSourceResponse

    /**
     * Full-text search with the operator grammar: the server's index
     * when online, the replica's identical one when offline. A query the
     * replica cannot answer (author:, is:task, has:) says so instead of
     * guessing.
     */
    suspend fun search(query: String, space: String?): List<SearchResult>

    /** Queues an offline action; replay happens on the next sync. */
    suspend fun enqueueCreate(space: String, path: String, content: String)
    suspend fun enqueueAppend(noteId: String, text: String)
    suspend fun enqueueMove(noteId: String, toPath: String)

    /**
     * PUT one file under an `_assets` directory (`PUT /api/files/`),
     * the same endpoint the web editor uploads through; the server
     * picks a free name and the answer carries it. Throws [IOException]
     * when the network is gone and [retrofit2.HttpException] when the
     * server refuses (over the asset limit, a viewer's space).
     */
    suspend fun uploadAsset(path: String, bytes: ByteArray, mime: String): UploadResponse

    /** Queues an upload whose bytes sit in a staged file; replay on the next sync. */
    suspend fun enqueueUpload(op: UploadOp)

    /** The `_assets/` paths whose upload still waits, for the reader's placeholders. */
    val pendingUploadPaths: Flow<Set<String>>

    /**
     * Rewrites a note's `![](_assets/...)` link when a queued upload
     * lands under another name than the one asked for; wired to the
     * realtime engine by the app, so the fix rides the document.
     */
    val fixUploadLink: suspend (noteId: String, askedFor: String, written: String) -> Unit

    /**
     * Ticks a task box while reading: through the server's endpoint when
     * it can be reached, queued as a pending op (and flipped in the
     * cached body, so it reads back ticked) when it cannot.
     */
    suspend fun tickTask(noteId: String, line: Int, done: Boolean): TickOutcome

    /**
     * One tasks listing over `GET /api/tasks`: the rows the filters
     * name, from the server when it answers and from the cache (with its
     * age) when it cannot. Nothing cached rethrows, so the screen can
     * say why it has no list.
     */
    suspend fun tasks(scope: TaskScope): TasksResult

    /** The account's tags with note counts; the replica's when offline. */
    suspend fun tags(): List<TagCount>

    /** The open count across every space, with when it was read; null when never known. */
    suspend fun openTaskCount(): TaskCount?

    /**
     * The note a dashed wikilink creates, made now when the server is
     * reachable; offline the create queues and returns null — there is
     * no id to open until the next sync replays it.
     */
    suspend fun createNoteAt(path: String): String?

    /**
     * Today's daily note, opened or made on the server. Offline this
     * throws; capture's local path (the replica plus a queued create)
     * is the caller's fallback.
     */
    suspend fun openDaily(space: String, date: String): DailyNoteResponse

    /** The server's daily-note pattern, or null when the server will not say. */
    suspend fun dailyPattern(): String?

    /**
     * The note's wikilinks resolved against the body the reader is
     * rendering: the payload's rows when the server sent them, the
     * replica's own resolution otherwise, so links work in airplane
     * mode too.
     */
    suspend fun resolveLinks(note: Note, body: String): List<ResolvedLink>

    /**
     * The note's revisions. History lives on the server, so these four
     * are online-only: a failure says why instead of answering from a
     * cache that does not exist.
     */
    suspend fun noteHistory(id: String): List<HistoryEntry>

    /** The note's diff between two revisions, as unified diff text. */
    suspend fun noteHistoryDiff(id: String, from: String, to: String): String

    /** Writes a revision's old text back as an edit; open editors converge on it. */
    suspend fun restoreRevision(id: String, revision: String, path: String)

    /** One space's feed page, the server's folding of its history. */
    suspend fun activity(
        space: String,
        path: String? = null,
        since: String? = null,
        author: String? = null,
        cursor: String? = null,
        limit: Int = 50,
    ): ActivityResponse

    /** What restoring the tree, or one space, to a commit would do. */
    suspend fun pitPreview(commit: String, space: String): PitPreview

    /** Runs the restore; the server tags what stood before it first. */
    suspend fun pitRestore(commit: String, space: String): RestoreSummary

    /** Every deleted note the account may bring back. */
    suspend fun deletedNotes(): List<DeletedNoteRow>

    /** Brings one deleted note back: from the trash, or the history. */
    suspend fun restoreDeleted(id: String): DeletedRestoreResult

    /**
     * Every conflict copy in the caller's spaces, with the note it
     * belongs to while that survives. Resolving is a server action,
     * so these four are online-only; the server's own messages
     * (a copy whose survivor is gone, a server without the
     * reconciliation loop) surface as they are.
     */
    suspend fun conflicts(): List<ConflictEntry>

    /** The conflict copies parked beside one note. */
    suspend fun noteConflicts(id: String): List<NoteMeta>

    /** The diff between one copy and its survivor, as unified text. */
    suspend fun conflictDiff(id: String): ConflictDiffResponse

    /** Settles one copy — keep mine, keep theirs, or keep both — as one commit. */
    suspend fun resolveConflict(id: String, action: ConflictAction): ConflictResolveResponse

    /** How many offline actions wait for the network. */
    val pendingCount: Flow<Int>
}

/** What a tick did. */
sealed interface TickOutcome {
    /** The server took it (or the box already sat that way). */
    data object Done : TickOutcome

    /**
     * The server was out of reach; the op is queued and the cached body
     * flipped, so the box reads back ticked. [body] is that flipped
     * body, when a cached one existed.
     */
    data class Queued(val body: String?) : TickOutcome

    /**
     * The server refused it (a viewer's space, the note gone, the line
     * moved). [status] carries the HTTP code — 409 says the line is not
     * an unticked box anymore and the list catches up on its own.
     */
    data class Refused(val message: String, val status: Int? = null) : TickOutcome
}

/** The tasks page's filters. [key] is the cache's scope string. */
data class TaskScope(
    val space: String = "",
    val tag: String = "",
    val path: String = "",
    /** True lists completed tasks from the last 30 days instead of open ones. */
    val done: Boolean = false,
) {
    val key: String get() = (if (done) "d" else "o") + "\u0000" + space + "\u0000" + tag + "\u0000" + path
}

/** One tasks listing: the rows, where they came from, and when (epoch ms). */
data class TasksResult(
    val rows: List<TaskRow>,
    val fromServer: Boolean,
    val fetchedAt: Long?,
)

/** The open count across every space, with when it was read. */
data class TaskCount(val count: Int, val fetchedAt: Long)

/** One search result, the same shape whether the server or the replica produced it. */
data class SearchResult(
    val id: String,
    val title: String,
    val path: String,
    val space: String,
    val kind: String,
    val updatedAt: String,
    val snippet: String,
    val fromServer: Boolean,
)

/**
 * The history endpoints answer 501 when the server runs without the
 * git layer; the message says it plainly instead of naming a status
 * code.
 */
internal fun Throwable.asHistoryError(): Throwable =
    if (this is HttpException && code() == 501) IllegalStateException("History is off on this server.") else this

/** The repository over one server and its replica. */
class YanaNoteRepository(
    private val client: YanaClient,
    private val store: ReplicaStore,
    private val uploads: PendingUploadStore,
    override val fixUploadLink: suspend (noteId: String, askedFor: String, written: String) -> Unit = { _, _, _ -> },
) : NoteRepository {

    override val pendingCount: Flow<Int> get() = store.pendingCount

    override val pendingUploadPaths: Flow<Set<String>> get() = store.pendingUploadPaths()

    override suspend fun sync() {
        bind()
        pull()
        val replayed = replayPending()
        if (replayed > 0) pull()
    }

    private suspend fun pull() {
        val api = client.api()
        store.applySync(api.spaces().spaces, api.notes().notes, api.tree().spaces)
    }

    override suspend fun spaces(): List<Space> {
        bind()
        store.spaces()?.let { return it }
        pull()
        return store.spaces() ?: emptyList()
    }

    override suspend fun tree(space: String): List<TreeNode> {
        bind()
        store.tree(space)?.let { return it }
        pull()
        return store.tree(space) ?: emptyList()
    }

    override suspend fun note(id: String): Note {
        bind()
        return try {
            val n = client.api().note(id)
            val content = n.markdown ?: n.source ?: ""
            store.storeBody(id, n.kind, content)
            n
        } catch (e: YanaClient.NotSignedIn) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            storedNote(id) ?: throw e
        }
    }

    override suspend fun noteView(id: String): NoteView {
        bind()
        return client.api().noteView(id)
    }

    override suspend fun saveSource(id: String, source: String, baseHash: String): SaveSourceResponse {
        bind()
        val res = client.api().saveSource(id, SaveSourceRequest(source, baseHash))
        store.storeBody(id, "html", source)
        return res
    }

    override suspend fun search(query: String, space: String?): List<SearchResult> {
        if (query.isBlank()) return emptyList()
        bind()
        return try {
            client.api().search(query.trim().take(512), space).hits.map { it.toResult() }
        } catch (e: YanaClient.NotSignedIn) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            // The server is out of reach: the replica answers, unless the
            // query asks for data only the server keeps.
            store.search(parseQuery(query), space).map { it.toResult() }
        }
    }

    override suspend fun enqueueCreate(space: String, path: String, content: String) =
        store.enqueueCreate(CreateOp(space, path, content))

    override suspend fun enqueueAppend(noteId: String, text: String) =
        store.enqueueAppend(AppendOp(noteId, text))

    override suspend fun enqueueMove(noteId: String, toPath: String) =
        store.enqueueMove(MoveOp(noteId, toPath))

    override suspend fun uploadAsset(path: String, bytes: ByteArray, mime: String): UploadResponse {
        bind()
        val type = mime.takeIf { it.isNotBlank() }?.toMediaTypeOrNull()
        return client.api().uploadFile(encodeAssetPath(path), bytes.toRequestBody(type))
    }

    override suspend fun enqueueUpload(op: UploadOp) = store.enqueueUpload(op)

    override suspend fun tickTask(noteId: String, line: Int, done: Boolean): TickOutcome {
        bind()
        return try {
            client.api().tickTask(TaskTickRequest(note = noteId, line = line, done = done))
            TickOutcome.Done
        } catch (e: YanaClient.NotSignedIn) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            // Offline: the tick rides with the next sync and the cached
            // body flips now, so the box reads back ticked — in the note
            // and in every cached listing that shows the row.
            store.enqueueTask(TaskOp(noteId, line, done))
            store.flipCachedTaskRows(noteId, line, done)
            val flipped = store.flipCachedTask(noteId, line, done)
            TickOutcome.Queued(flipped)
        } catch (e: HttpException) {
            TickOutcome.Refused(e.userMessage(), e.code())
        }
    }

    override suspend fun tasks(scope: TaskScope): TasksResult {
        bind()
        return try {
            val res = client.api().tasks(
                space = scope.space.ifEmpty { null },
                done = if (scope.done) true else null,
                tag = scope.tag.ifEmpty { null },
                path = scope.path.ifEmpty { null },
            )
            store.cacheTasks(scope.key, res.tasks)
            TasksResult(res.tasks, fromServer = true, fetchedAt = System.currentTimeMillis())
        } catch (e: YanaClient.NotSignedIn) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            val cached = store.cachedTasks(scope.key) ?: throw e
            TasksResult(cached.first, fromServer = false, fetchedAt = cached.second)
        }
    }

    override suspend fun tags(): List<TagCount> {
        bind()
        return try {
            client.api().tags().tags
        } catch (e: YanaClient.NotSignedIn) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            store.tagCounts()
        } catch (e: HttpException) {
            store.tagCounts()
        }
    }

    override suspend fun openTaskCount(): TaskCount? {
        bind()
        return try {
            val count = client.api().taskCount().count
            store.cacheTaskCount(count)
            TaskCount(count, System.currentTimeMillis())
        } catch (e: YanaClient.NotSignedIn) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            store.cachedTaskCount()?.let { TaskCount(it.first, it.second) }
        } catch (e: HttpException) {
            store.cachedTaskCount()?.let { TaskCount(it.first, it.second) }
        }
    }

    override suspend fun createNoteAt(path: String): String? {
        bind()
        return try {
            client.api().createNote(CreateNoteRequest(path = path, content = "")).id.ifEmpty { null }
        } catch (e: YanaClient.NotSignedIn) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (e: IOException) {
            val space = path.substringBefore('/', "")
            store.enqueueCreate(CreateOp(space, path, ""))
            null
        } catch (_: HttpException) {
            null
        }
    }

    override suspend fun openDaily(space: String, date: String): DailyNoteResponse {
        bind()
        return client.api().daily(DailyNoteRequest(space, date))
    }

    override suspend fun dailyPattern(): String? {
        bind()
        return try {
            client.api().status().daily.pattern.ifEmpty { null }
        } catch (e: YanaClient.NotSignedIn) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            null
        }
    }

    override suspend fun resolveLinks(note: Note, body: String): List<ResolvedLink> {
        // The server's rows are authoritative when the payload carried
        // them; a note read from the replica resolves against the
        // replica itself, with the same rules the server applies.
        if (note.links.isNotEmpty()) {
            return note.links.map { ResolvedLink(it.rawTarget, it.toId?.ifEmpty { null }, it.resolved) }
        }
        val raws = GoRender.wikiLinks(body)
        if (raws.isEmpty()) return emptyList()
        val resolver = WikiResolver(note.space, store.spaceNoteRefs(note.space))
        return raws.map { resolver.resolve(it, note.path) }
    }

    override suspend fun noteHistory(id: String): List<HistoryEntry> {
        bind()
        return try {
            client.api().noteHistory(id).entries
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            throw e.asHistoryError()
        }
    }

    override suspend fun noteHistoryDiff(id: String, from: String, to: String): String {
        bind()
        return try {
            client.api().noteHistoryDiff(id, from, to).diff
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            throw e.asHistoryError()
        }
    }

    override suspend fun restoreRevision(id: String, revision: String, path: String) {
        bind()
        client.api().restoreNote(id, RestoreNoteRequest(revision, path))
    }

    override suspend fun activity(
        space: String,
        path: String?,
        since: String?,
        author: String?,
        cursor: String?,
        limit: Int,
    ): ActivityResponse {
        bind()
        return try {
            client.api().activity(space = space, path = path, since = since, author = author, limit = limit, cursor = cursor)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            throw e.asHistoryError()
        }
    }

    override suspend fun pitPreview(commit: String, space: String): PitPreview {
        bind()
        val res = client.api().pitPreview(PitRestoreRequest(commit, space))
        return res.preview ?: PitPreview(commit = commit, space = space)
    }

    override suspend fun pitRestore(commit: String, space: String): RestoreSummary =
        client.api().pitRestore(PitRestoreRequest(commit, space))

    override suspend fun deletedNotes(): List<DeletedNoteRow> {
        bind()
        return try {
            client.api().deletedNotes().entries
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            throw e.asHistoryError()
        }
    }

    override suspend fun restoreDeleted(id: String): DeletedRestoreResult {
        bind()
        return client.api().restoreDeleted(id)
    }

    override suspend fun conflicts(): List<ConflictEntry> {
        bind()
        return client.api().conflicts().conflicts
    }

    override suspend fun noteConflicts(id: String): List<NoteMeta> {
        bind()
        return client.api().noteConflicts(id).conflicts
    }

    override suspend fun conflictDiff(id: String): ConflictDiffResponse {
        bind()
        return client.api().conflictDiff(id)
    }

    override suspend fun resolveConflict(id: String, action: ConflictAction): ConflictResolveResponse {
        bind()
        return client.api().resolveConflict(id, ConflictResolveRequest(action.value))
    }

    /**
     * Replays the queue oldest first. A network failure stops the pass
     * with the op kept for the next one; a server refusal drops the op,
     * because repeating the same refused request cannot start working.
     * Appending to a markdown note has no transport until the editor's
     * CRDT path lands, so those ops wait here; everything else replays
     * today.
     */
    private suspend fun replayPending(): Int {
        var replayed = 0
        for (p in store.pendingOps()) {
            val settled = when (val op = p.op) {
                is OpPayload.Create -> try {
                    // An offline-composed note carries its own id in the
                    // frontmatter, so the server's create keeps it and
                    // the note arrives everywhere with the ULID this
                    // device minted.
                    val content =
                        if (op.op.id.isNotEmpty()) "---\nid: ${op.op.id}\n---\n" + op.op.content else op.op.content
                    client.api().createNote(CreateNoteRequest(path = op.op.path, content = content))
                    true
                } catch (e: Exception) {
                    if (e is CancellationException) throw e
                    !(e is HttpException && permanent(e.code()))
                }
                is OpPayload.Move -> try {
                    client.api().moveNote(op.op.noteId, MoveRequest(op.op.toPath)).let { r ->
                        r.isSuccessful || permanent(r.code())
                    }
                } catch (e: Exception) {
                    if (e is CancellationException) throw e
                    !(e is HttpException && permanent(e.code()))
                }
                is OpPayload.Append -> try {
                    val cached = store.note(op.op.noteId)
                    if (cached?.kind != "html") {
                        false // markdown appends ride with the editor
                    } else {
                        // The cached base hash means a diverged disk version
                        // is parked as a conflict copy, not overwritten.
                        val src = store.rawBody(op.op.noteId).orEmpty()
                        val res = client.api().saveSource(
                            op.op.noteId,
                            SaveSourceRequest(src + op.op.text, cached.contentHash),
                        )
                        res.ok
                    }
                } catch (e: Exception) {
                    if (e is CancellationException) throw e
                    !(e is HttpException && permanent(e.code()))
                }
                is OpPayload.Task -> try {
                    client.api().tickTask(TaskTickRequest(op.op.noteId, op.op.line, op.op.done))
                    true
                } catch (e: Exception) {
                    if (e is CancellationException) throw e
                    !(e is HttpException && permanent(e.code()))
                }
                is OpPayload.Upload -> try {
                    val bytes = uploads.read(op.op.file)
                    if (bytes == null) {
                        // The staged bytes are gone (a wiped queue left the
                        // file behind, or vice versa): the op can never send.
                        true
                    } else {
                        val res = client.api().uploadFile(encodeAssetPath(op.op.path), bytes.toRequestBody(null))
                        uploads.delete(op.op.file)
                        if (res.name.isNotEmpty() && res.name != op.op.name) {
                            // The name was taken; the server picked another,
                            // so the note's link is fixed through the document.
                            fixUploadLink(op.op.noteId, op.op.name, res.name)
                        }
                        true
                    }
                } catch (e: Exception) {
                    if (e is CancellationException) throw e
                    !(e is HttpException && permanent(e.code()))
                }
            }
            if (settled) {
                store.dropOp(p.seq)
                replayed++
            } else {
                break
            }
        }
        return replayed
    }

    /** A refused request the server will keep refusing. */
    private fun permanent(code: Int): Boolean = code >= 400 && code < 500 && code != 408 && code != 429    /** Ties the replica to the signed-in account, wiping it on a change. */
    private suspend fun bind() {
        val s = client.session.value ?: return
        val server = normalizeServerUrl(s.server)?.toString() ?: s.server
        store.bind(server, s.userId)
    }

    /** One note assembled from the replica for offline reading. */
    override suspend fun cachedNote(id: String): Note? {
        bind()
        // A sync seeds every note with an empty body row; only one this
        // device fetched holds text.
        return storedNote(id)?.takeIf { !(it.markdown ?: it.source).isNullOrEmpty() }
    }

    private suspend fun storedNote(id: String): Note? {
        val row = store.note(id) ?: return null
        val raw = store.rawBody(id)
        return Note(
            id = row.id,
            space = row.space,
            path = row.relPath,
            title = row.title,
            preview = row.preview,
            kind = row.kind,
            contentHash = row.contentHash,
            created = formatEpoch(row.created),
            updatedAt = formatEpoch(row.updatedAt),            tags = row.tags?.split(',')?.filter { it.isNotEmpty() } ?: emptyList(),
            role = "",
            markdown = if (row.kind != "html") raw else null,
            source = if (row.kind == "html") raw else null,
        )
    }

    private fun SearchHit.toResult() = SearchResult(
        id = note.id,
        title = note.title,
        path = note.path,
        space = note.space,
        kind = note.kind,
        updatedAt = note.updatedAt,
        snippet = snippet.stripMarks(),
        fromServer = true,
    )

    private fun LocalHit.toResult() = SearchResult(
        id = id,
        title = title,
        path = path,
        space = space,
        kind = kind,
        updatedAt = formatEpoch(updatedAt),
        snippet = snippet.stripMarks(),
        fromServer = false,
    )

    private fun String.stripMarks(): String =
        replace("<mark>", "").replace("</mark>", "")

    private fun formatEpoch(nanos: Long): String =
        Instant.ofEpochSecond(nanos / 1_000_000_000L, nanos % 1_000_000_000L).toString()
}
