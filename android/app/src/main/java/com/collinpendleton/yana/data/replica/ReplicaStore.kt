package com.collinpendleton.yana.data.replica

import android.content.Context
import androidx.room.Room
import androidx.sqlite.db.SimpleSQLiteQuery
import androidx.sqlite.driver.bundled.BundledSQLiteDriver
import com.collinpendleton.yana.data.NoteMeta
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.SpaceTree
import com.collinpendleton.yana.data.TagCount
import com.collinpendleton.yana.data.TaskNote
import com.collinpendleton.yana.data.TaskRow
import com.collinpendleton.yana.data.TreeNode
import com.collinpendleton.yana.data.YanaJson
import com.collinpendleton.yana.data.indexText
import com.collinpendleton.yana.data.parseInstant
import com.collinpendleton.yana.data.search.SearchQuery
import com.collinpendleton.yana.data.search.buildSearchQuery
import com.collinpendleton.yana.data.rt.OutboxRow
import java.time.Instant
import kotlinx.coroutines.flow.map
import kotlinx.serialization.Serializable

/** The JSON payloads of the offline operations. */
@Serializable
data class CreateOp(
    val space: String,
    val path: String,
    val content: String,
    /**
     * The note's id when the create was composed offline: a ULID this
     * device minted, carried in the content's frontmatter so the
     * server's create keeps it. Empty for the queued creates of
     * versions before capture, which wait for the server's id.
     */
    val id: String = "",
)

@Serializable
data class AppendOp(val noteId: String, val text: String)

@Serializable
data class MoveOp(val noteId: String, val toPath: String)

/** One task tick: the line is the body line the rendered box carries. */
@Serializable
data class TaskOp(val noteId: String, val line: Int, val done: Boolean)

/**
 * One queued asset upload: the bytes sit in a staged file on disk (the
 * queue's payloads are JSON), the PUT names [path] under the note's
 * `_assets/`, and [noteId] names the note the link went into so a
 * replay that lands under another name can fix the link.
 */
@Serializable
data class UploadOp(
    val path: String,
    val name: String,
    val noteId: String,
    val file: String,
)

@Serializable
sealed interface OpPayload {
    @Serializable
    data class Create(val op: CreateOp) : OpPayload

    @Serializable
    data class Append(val op: AppendOp) : OpPayload

    @Serializable
    data class Move(val op: MoveOp) : OpPayload

    @Serializable
    data class Task(val op: TaskOp) : OpPayload

    @Serializable
    data class Upload(val op: UploadOp) : OpPayload
}

/** One pending operation, decoded. */
data class PendingOp(val seq: Long, val createdAt: Long, val op: OpPayload)

/**
 * The replica itself: everything the screens read when the server is out
 * of reach, kept in Room and answered with the server's own query
 * semantics. Network lives one layer up, in NoteRepository; this class
 * only stores, derives, and searches.
 */
class ReplicaStore(private val db: ReplicaDatabase) : com.collinpendleton.yana.data.rt.RtStore {
    private val dao = db.dao()

    companion object {
        /**
         * Opens (creating) the replica. The bundled SQLite driver puts
         * the same SQLite build on every device, which is what carries
         * the FTS5 trigram tokenizer below the API levels that ship it.
         */
        fun open(context: Context): ReplicaStore {
            val db = Room.databaseBuilder(context, ReplicaDatabase::class.java, "replica.db")
                .setDriver(BundledSQLiteDriver())
                .addMigrations(ReplicaDatabase.MIGRATION_1_2, ReplicaDatabase.MIGRATION_2_3)
                .addCallback(object : androidx.room.RoomDatabase.Callback() {
                    // onOpen re-runs the DDL; every statement is IF NOT
                    // EXISTS, so a database that somehow arrived without
                    // its index (a restored backup, a skipped callback)
                    // heals on the next open.
                    override fun onCreate(db: androidx.sqlite.db.SupportSQLiteDatabase) {
                        for (ddl in ReplicaDatabase.FTS_DDL) db.execSQL(ddl)
                    }

                    override fun onOpen(db: androidx.sqlite.db.SupportSQLiteDatabase) {
                        for (ddl in ReplicaDatabase.FTS_DDL) db.execSQL(ddl)
                    }
                })
                .build()
            return ReplicaStore(db)
        }
    }

    /** Whose replica this is: server URL and user id, or null when none. */
    suspend fun owner(): String? = dao.meta("owner")

    /** Binds the replica to an account, wiping it first when that changes. */
    suspend fun bind(server: String, userId: String) {
        val stamp = "$server|$userId"
        if (dao.meta("owner") != stamp) {
            dao.wipe()
            dao.setMeta(ReplicaMetaEntity("owner", stamp))
        }
    }

    /** Drops every row; what a sign-out does. */
    suspend fun wipe() = dao.wipe()

    /** Applies one full metadata sync: spaces, flat notes, nested tree. */
    suspend fun applySync(spaces: List<Space>, notes: List<NoteMeta>, tree: List<SpaceTree>) {
        dao.applySync(
            spaces.map { SpaceEntity(it.name, it.label, it.notes) },
            notes.map { n ->
                NoteEntity(
                    id = n.id,
                    space = n.space,
                    relPath = n.path,
                    title = n.title,
                    preview = n.preview,
                    kind = n.kind,
                    contentHash = n.contentHash,
                    created = epochNanos(n.created),
                    updatedAt = epochNanos(n.updatedAt),
                ) to n.tags
            },
            flattenTree(tree),
            pendingCreateIds(),
        )
    }

    /** Caches a note's content after the user opened it. */
    override suspend fun storeBody(noteId: String, kind: String, content: String) {
        val (body, raw) = indexText(kind, content)
        dao.storeBody(noteId, body, raw)
    }

    /** The cached spaces, or null when the replica has none. */
    suspend fun spaces(): List<Space>? =
        dao.spaces().ifEmpty { null }?.map { Space(it.name, it.label, it.notes) }

    /** One space's folder tree from the cache, or null when absent. */
    suspend fun tree(space: String): List<TreeNode>? {
        val rows = dao.treeOf(space)
        if (rows.isEmpty()) return null
        return nestTree(rows)
    }

    /**
     * Every folder path the tree holds, the space included, in the
     * server's order — the new-note picker's and the move picker's
     * pool, answered offline.
     */
    suspend fun dirPaths(): List<String> = dao.dirPaths()

    /**
     * Moves one note's rows to [toPath] (a full path, the space
     * included) so the tree and the lists read the move offline,
     * before the replay lands it on the server. Nothing happens when
     * the note is unknown here.
     */
    suspend fun moveLocal(noteId: String, toPath: String) {
        val row = dao.noteById(noteId) ?: return
        val from = if (row.space.isEmpty()) row.relPath else "${row.space}/${row.relPath}"
        val space = toPath.substringBefore('/', "")
        val rel = if (space.isEmpty()) toPath else toPath.substringAfter('/')
        val name = toPath.substringAfterLast('/')
        val parent = toPath.substringBeforeLast('/', "")
        dao.moveNoteLocal(noteId, space, rel, from, toPath, parent, name)
    }

    /** One cached note, with its tags, or null. */
    suspend fun note(id: String): NoteWithTags? = dao.noteWithTags(id)

    /** The cached note at a path, with its tags, or null; Today's offline lookup. */
    suspend fun noteByPath(path: String): NoteWithTags? = dao.noteByPath(path)

    /**
     * The notes this device opened lately, freshest first — the recent
     * list the share target picks from, with titles.
     */
    suspend fun recentlyOpenedNotes(limit: Int): List<RecentNoteRow> = dao.recentlyOpenedNotes(limit)

    /** The notes the replica heard from last, newest first — the same list's fallback. */
    suspend fun newestNotes(limit: Int): List<RecentNoteRow> = dao.newestNotes(limit)

    /**
     * The note file names already sitting directly in one folder of a
     * space, for the next free name. The folder is a full path (the
     * space included) — the shape the notes table's rel_path keeps —
     * and notes in deeper folders do not count.
     */
    suspend fun namesIn(space: String, folder: String): List<String> =
        dao.pathsIn(space, folder)
            .filter { !it.removePrefix("$folder/").contains('/') }
            .map { it.substringAfterLast('/') }

    /**
     * A note composed offline: its row and its first body land in the
     * replica now, under the id this device minted, and stay until a
     * sync replaces them with the server's own row for the same id.
     */
    suspend fun putLocalNote(id: String, space: String, path: String, title: String, kind: String, body: String, nowMs: Long) {
        dao.upsertNote(
            NoteEntity(
                id = id,
                space = space,
                relPath = path,
                title = title,
                preview = body.take(200),
                kind = kind,
                created = nowMs * 1_000_000L,
                updatedAt = nowMs * 1_000_000L,
            ),
        )
        storeBody(id, kind, body)
    }

    /** One cached note's raw text (markdown body or HTML source), or null. */
    suspend fun rawBody(id: String): String? = dao.bodyOf(id)?.rawBody

    /**
     * Flips a task box's `[ ]` / `[x]` character on one line of the
     * cached body, the same minimal edit the server's tick makes, so an
     * offline tick reads back ticked. Lines are counted from the start
     * of the body, the numbering the rendered checkbox carries. Returns
     * the flipped body, or null when there is nothing to flip (no cached
     * body, no line, no marker, or already in the asked state).
     */
    suspend fun flipCachedTask(noteId: String, line: Int, done: Boolean): String? {
        val row = dao.noteById(noteId) ?: return null
        val raw = dao.bodyOf(noteId)?.rawBody ?: return null
        val next = flipTaskLine(raw, line, done) ?: return null
        val (body, rawNext) = indexText(row.kind, next)
        dao.storeBody(noteId, body, rawNext)
        return rawNext
    }

    // --- the tasks cache ------------------------------------------------------

    /** Caches one filter scope's listing with its fetch time. */
    suspend fun cacheTasks(scope: String, rows: List<TaskRow>) {
        dao.replaceTasks(
            scope,
            rows.mapIndexed { i, t ->
                TaskCacheEntity(
                    scope = scope,
                    noteId = t.note.id,
                    line = t.line,
                    indent = t.indent,
                    text = t.text,
                    done = t.done,
                    doneAt = t.doneAt,
                    heading = t.heading,
                    ord = i,
                    space = t.note.space,
                    path = t.note.path,
                    title = t.note.title,
                    kind = t.note.kind,
                )
            },
            System.currentTimeMillis(),
        )
    }

    /** The cached listing of one scope with its fetch time, or null when absent. */
    suspend fun cachedTasks(scope: String): Pair<List<TaskRow>, Long>? {
        val at = dao.taskFetchTime(scope) ?: return null
        val rows = dao.tasksOf(scope)
        if (rows.isEmpty()) return null
        return rows.map { it.toRow() } to at
    }

    /** Flips one box in every cached listing it appears in, the listing's half of an offline tick. */
    suspend fun flipCachedTaskRows(noteId: String, line: Int, done: Boolean) = dao.flipTaskRow(noteId, line, done)

    private fun TaskCacheEntity.toRow() = TaskRow(
        note = TaskNote(id = noteId, space = space, path = path, title = title, kind = kind),
        line = line,
        indent = indent,
        text = text,
        done = done,
        doneAt = doneAt,
        heading = heading,
    )

    // --- the open-task count --------------------------------------------------

    /** Caches the open count and when it was read, the home screen's badge. */
    suspend fun cacheTaskCount(count: Int) {
        dao.setMeta(ReplicaMetaEntity("task_count", count.toString()))
        dao.setMeta(ReplicaMetaEntity("task_count_at", System.currentTimeMillis().toString()))
    }

    /** The cached open count with its read time, or null when never fetched. */
    suspend fun cachedTaskCount(): Pair<Int, Long>? {
        val count = dao.meta("task_count")?.toIntOrNull() ?: return null
        val at = dao.meta("task_count_at")?.toLongOrNull() ?: return null
        return count to at
    }

    /** The replica's tag counts, the tag filter's offline answer. */
    suspend fun tagCounts(): List<TagCount> = dao.tagCounts().map { TagCount(it.tag, it.count) }

    /** The notes carrying one tag, in path order — the tag page offline. */
    suspend fun notesForTag(tag: String): List<NoteWithTags> = dao.notesForTag(tag)

    /** Every note with its tags — the switcher's pool. */
    suspend fun allNotes(): List<NoteWithTags> = dao.allNotesWithTags()

    /** Every note of one space, the refs wikilink resolution runs over. */
    suspend fun spaceNoteRefs(space: String): List<com.collinpendleton.yana.data.NoteRef> =
        dao.notesIn(space).map { com.collinpendleton.yana.data.NoteRef(it.id, it.relPath) }

    /** Runs a parsed query against the local index, with the server's semantics. */
    suspend fun search(query: SearchQuery, space: String?, limit: Int = 50): List<LocalHit> {
        val built = buildSearchQuery(query, space, limit)
        val rows = dao.rawSearch(SimpleSQLiteQuery(built.sql, built.args.toTypedArray()))
        return rows.map { r ->
            LocalHit(
                id = r.id,
                space = r.space,
                path = r.relPath,
                title = r.title,
                preview = r.preview,
                kind = r.kind,
                updatedAt = r.updatedAt,
                snippet = r.snippet.ifEmpty { r.preview },
                rank = r.rank,
            )
        }
    }

    // --- pending operations ------------------------------------------------

    val pendingCount: kotlinx.coroutines.flow.Flow<Int> get() = dao.pendingCount()

    suspend fun enqueueCreate(op: CreateOp) = enqueue("create", YanaJson.encodeToString(OpPayload.serializer(), OpPayload.Create(op)))
    suspend fun enqueueAppend(op: AppendOp) = enqueue("append", YanaJson.encodeToString(OpPayload.serializer(), OpPayload.Append(op)))
    suspend fun enqueueMove(op: MoveOp) = enqueue("move", YanaJson.encodeToString(OpPayload.serializer(), OpPayload.Move(op)))
    suspend fun enqueueTask(op: TaskOp) = enqueue("task", YanaJson.encodeToString(OpPayload.serializer(), OpPayload.Task(op)))
    suspend fun enqueueUpload(op: UploadOp) = enqueue("upload", YanaJson.encodeToString(OpPayload.serializer(), OpPayload.Upload(op)))

    private suspend fun enqueue(type: String, payload: String) {
        dao.addPendingOp(PendingOpEntity(type = type, payload = payload, createdAt = System.currentTimeMillis()))
    }

    /** The queue, oldest first. */
    suspend fun pendingOps(): List<PendingOp> = dao.pendingOps().mapNotNull { e ->
        val op = runCatching { YanaJson.decodeFromString(OpPayload.serializer(), e.payload) }.getOrNull() ?: return@mapNotNull null
        PendingOp(e.seq, e.createdAt, op)
    }

    /** Removes one op after a successful replay (or a permanent refusal). */
    suspend fun dropOp(seq: Long) = dao.dropPendingOp(seq)

    /**
     * The ids of notes composed offline whose create is still queued:
     * a sync keeps their rows, because the server has not answered for
     * them yet and the replica is the only place they exist.
     */
    suspend fun pendingCreateIds(): List<String> =
        dao.createOpPayloads().mapNotNull { payload ->
            runCatching { YanaJson.decodeFromString(CreateOp.serializer(), payload).id }.getOrNull()
        }.filter { it.isNotEmpty() }

    /** The queued uploads, decoded, oldest first. */
    suspend fun pendingUploads(): List<UploadOp> =
        dao.uploadOpPayloads().mapNotNull { payload ->
            runCatching { YanaJson.decodeFromString(UploadOp.serializer(), payload) }.getOrNull()
        }

    /** The `_assets/` paths whose upload still waits, for the reader's placeholders. */
    fun pendingUploadPaths(): kotlinx.coroutines.flow.Flow<Set<String>> =
        dao.uploadOpPayloadsFlow().map { payloads ->
            payloads.mapNotNull { payload ->
                runCatching { YanaJson.decodeFromString(UploadOp.serializer(), payload).path }.getOrNull()
            }.toSet()
        }

    // --- CRDT state and outbox (the sync engine's RtStore) --------------------

    override suspend fun crdtState(noteId: String): ByteArray? = dao.crdtState(noteId)?.state

    override suspend fun storeCrdtState(noteId: String, state: ByteArray) {
        val row = dao.crdtState(noteId)
        val now = System.currentTimeMillis()
        dao.upsertCrdtState(NoteCrdtEntity(noteId, state, row?.openedAt ?: now, now))
    }

    override suspend fun touchOpened(noteId: String) {
        val row = dao.crdtState(noteId)
        val now = System.currentTimeMillis()
        dao.upsertCrdtState(NoteCrdtEntity(noteId, row?.state ?: ByteArray(0), now, row?.updatedAt ?: now))
    }

    override suspend fun recentlyOpened(limit: Int): List<String> = dao.recentlyOpenedCrdt(limit)

    override suspend fun addOutboxUpdate(noteId: String, update: ByteArray) {
        dao.addOutboxRow(CrdtOutboxEntity(noteId = noteId, payload = update, createdAt = System.currentTimeMillis()))
    }

    override suspend fun outboxFor(noteId: String): List<OutboxRow> = dao.outboxFor(noteId).map { OutboxRow(it.seq, it.payload) }

    override suspend fun outboxNotes(): List<String> = dao.outboxNotes()

    override suspend fun dropOutboxTo(seq: Long) = dao.dropOutboxTo(seq)

    override suspend fun outboxCount(): Int = dao.outboxCount()

    override suspend fun noteKind(noteId: String): String? = dao.noteById(noteId)?.kind

    val outboxCountFlow: kotlinx.coroutines.flow.Flow<Int> get() = dao.outboxCountFlow()
}

/** One local search result: the same fields a server hit carries. */
data class LocalHit(
    val id: String,
    val space: String,
    val path: String,
    val title: String,
    val preview: String,
    val kind: String,
    val updatedAt: Long,
    val snippet: String,
    val rank: Double,
)

/** A server timestamp to epoch nanoseconds, the server's ordering unit. */
internal fun epochNanos(s: String): Long {
    val t: Instant = parseInstant(s) ?: return 0
    return t.epochSecond * 1_000_000_000L + t.nano
}

private val taskBox = Regex("""\[([ xX])]""")

/**
 * Sets the box on [line] (counted from the body's first line, the
 * numbering the rendered checkbox's data-line carries) to [done], the
 * server's flipTaskLine: the minimal edit, x for a tick and a space for
 * an untick. Null when the line holds no box or already sits in the
 * asked state.
 */
internal fun flipTaskLine(body: String, line: Int, done: Boolean): String? {
    val lines = body.split('\n')
    if (line < 0 || line >= lines.size) return null
    val text = lines[line]
    val m = taskBox.find(text) ?: return null
    val was = m.groupValues[1] != " "
    if (was == done) return null
    val next = text.replaceRange(m.range, if (done) "[x]" else "[ ]")
    return (lines.subList(0, line) + next + lines.subList(line + 1, lines.size)).joinToString("\n")
}
