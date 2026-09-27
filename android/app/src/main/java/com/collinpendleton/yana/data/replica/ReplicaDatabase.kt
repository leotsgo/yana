package com.collinpendleton.yana.data.replica

import androidx.room.Dao
import androidx.room.ColumnInfo
import androidx.room.Database
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import androidx.room.RawQuery
import androidx.room.RoomDatabase
import androidx.room.Transaction
import androidx.room.Upsert
import androidx.room.migration.Migration
import androidx.sqlite.db.SupportSQLiteQuery

/** One row of a local search, the same fields the server's hits carry. */
data class LocalHitRow(
    val id: String,
    val space: String,
    @ColumnInfo(name = "rel_path") val relPath: String,
    val title: String,
    val preview: String,
    val kind: String,
    val created: Long,
    @ColumnInfo(name = "updated_at") val updatedAt: Long,
    val snippet: String,
    val rank: Double,
)

/** One cached note with its tags, for building trees and note views. */
data class NoteWithTags(
    val id: String,
    val space: String,
    @ColumnInfo(name = "rel_path") val relPath: String,
    val title: String,
    val preview: String,
    val kind: String,
    @ColumnInfo(name = "content_hash") val contentHash: String,
    val created: Long,
    @ColumnInfo(name = "updated_at") val updatedAt: Long,
    val tags: String?,
)

/** One tag and how many cached notes carry it. */
data class TagCountRow(
    val tag: String,
    val count: Int,
)

/** One note of a recent list: enough to show it and to open it. */
data class RecentNoteRow(
    val id: String,
    val title: String,
    @ColumnInfo(name = "rel_path") val path: String,
    val kind: String,
)

@Dao
interface ReplicaDao {
    @Query("SELECT * FROM spaces ORDER BY name")
    suspend fun spaces(): List<SpaceEntity>

    @Query("SELECT * FROM notes WHERE space = :space ORDER BY rel_path")
    suspend fun notesIn(space: String): List<NoteEntity>

    @Query("SELECT * FROM notes WHERE id = :id")
    suspend fun noteById(id: String): NoteEntity?

    @Query(
        """
        SELECT n.*, (SELECT group_concat(t.tag, ',') FROM tags t WHERE t.note_id = n.id ORDER BY t.tag) AS tags
        FROM notes n WHERE n.id = :id
        """,
    )
    suspend fun noteWithTags(id: String): NoteWithTags?

    @Query(
        """
        SELECT n.*, (SELECT group_concat(t.tag, ',') FROM tags t WHERE t.note_id = n.id ORDER BY t.tag) AS tags
        FROM notes n WHERE n.rel_path = :path
        """,
    )
    suspend fun noteByPath(path: String): NoteWithTags?

    /** The notes opened lately (the CRDT table's opened_at), freshest first. */
    @Query(
        """
        SELECT n.id AS id, n.title AS title, n.rel_path AS rel_path, n.kind AS kind
        FROM notes n INNER JOIN note_crdt c ON c.note_id = n.id
        ORDER BY c.opened_at DESC LIMIT :limit
        """,
    )
    suspend fun recentlyOpenedNotes(limit: Int): List<RecentNoteRow>

    /** The notes the replica heard from last, newest first. */
    @Query(
        """
        SELECT id AS id, title AS title, rel_path AS rel_path, kind AS kind
        FROM notes ORDER BY updated_at DESC LIMIT :limit
        """,
    )
    suspend fun newestNotes(limit: Int): List<RecentNoteRow>

    /** The paths of one folder's notes in a space. */
    @Query(
        """
        SELECT rel_path FROM notes WHERE space = :space AND rel_path LIKE :folder || '/%'
        """,
    )
    suspend fun pathsIn(space: String, folder: String): List<String>

    @Query("SELECT * FROM note_bodies WHERE note_rowid = (SELECT rowid FROM notes WHERE id = :id)")
    suspend fun bodyOf(id: String): NoteBodyEntity?

    @Query("SELECT COUNT(*) FROM notes")
    suspend fun noteCount(): Int

    @Query("SELECT * FROM tree_nodes WHERE space = :space ORDER BY position")
    suspend fun treeOf(space: String): List<TreeNodeEntity>

    @Query("SELECT COUNT(*) FROM tree_nodes")
    suspend fun treeCount(): Int

    @Query("SELECT value FROM replica_meta WHERE key = :key")
    suspend fun meta(key: String): String?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun setMeta(entry: ReplicaMetaEntity)

    @Query("SELECT * FROM pending_ops ORDER BY seq")
    suspend fun pendingOps(): List<PendingOpEntity>

    @Insert
    suspend fun addPendingOp(op: PendingOpEntity): Long

    @Query("DELETE FROM pending_ops WHERE seq = :seq")
    suspend fun dropPendingOp(seq: Long)

    @Query("DELETE FROM pending_ops")
    suspend fun clearPendingOps()

    @Query("SELECT COUNT(*) FROM pending_ops")
    fun pendingCount(): kotlinx.coroutines.flow.Flow<Int>

    /** The JSON payloads of the queued creates, for the ids they carry. */
    @Query("SELECT payload FROM pending_ops WHERE type = 'create'")
    suspend fun createOpPayloads(): List<String>

    /** The JSON payloads of the queued uploads, replayed oldest first. */
    @Query("SELECT payload FROM pending_ops WHERE type = 'upload' ORDER BY seq")
    suspend fun uploadOpPayloads(): List<String>

    /** The same payloads, observed, for the reader's pending placeholders. */
    @Query("SELECT payload FROM pending_ops WHERE type = 'upload' ORDER BY seq")
    fun uploadOpPayloadsFlow(): kotlinx.coroutines.flow.Flow<List<String>>

    // --- CRDT state and outbox ---------------------------------------------

    @Query("SELECT * FROM note_crdt WHERE note_id = :id")
    suspend fun crdtState(id: String): NoteCrdtEntity?

    @Upsert
    suspend fun upsertCrdtState(e: NoteCrdtEntity)

    @Query("UPDATE note_crdt SET opened_at = :at WHERE note_id = :id")
    suspend fun touchCrdtOpened(id: String, at: Long)

    /** The notes the person opened most recently, freshest first. */
    @Query("SELECT note_id FROM note_crdt ORDER BY opened_at DESC LIMIT :limit")
    suspend fun recentlyOpenedCrdt(limit: Int): List<String>

    @Insert
    suspend fun addOutboxRow(e: CrdtOutboxEntity): Long

    @Query("SELECT * FROM crdt_outbox WHERE note_id = :noteId ORDER BY seq")
    suspend fun outboxFor(noteId: String): List<CrdtOutboxEntity>

    /** Rows the server has confirmed; seq is the watermark a pong carries. */
    @Query("DELETE FROM crdt_outbox WHERE seq <= :seq")
    suspend fun dropOutboxTo(seq: Long)

    @Query("SELECT COUNT(*) FROM crdt_outbox")
    suspend fun outboxCount(): Int

    @Query("SELECT COUNT(*) FROM crdt_outbox")
    fun outboxCountFlow(): kotlinx.coroutines.flow.Flow<Int>

    @Query("SELECT DISTINCT note_id FROM crdt_outbox")
    suspend fun outboxNotes(): List<String>

    // --- the tasks cache ------------------------------------------------------

    /** The cached listing of one filter scope, in the server's row order. */
    @Query("SELECT * FROM tasks_cache WHERE scope = :scope ORDER BY ord")
    suspend fun tasksOf(scope: String): List<TaskCacheEntity>

    /** When the scope was last fetched, or null when it never was. */
    @Query("SELECT fetched_at FROM task_fetches WHERE scope = :scope")
    suspend fun taskFetchTime(scope: String): Long?

    /** Swaps one scope's cached listing and stamps its fetch time. */
    @Transaction
    suspend fun replaceTasks(scope: String, rows: List<TaskCacheEntity>, fetchedAt: Long) {
        clearTasksOf(scope)
        for (r in rows) insertTask(r)
        insertTaskFetch(TaskFetchEntity(scope, fetchedAt))
    }

    @Query("DELETE FROM tasks_cache WHERE scope = :scope")
    suspend fun clearTasksOf(scope: String)

    @Insert
    suspend fun insertTask(row: TaskCacheEntity)

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun insertTaskFetch(entry: TaskFetchEntity)

    /**
     * Flips one box in every cached listing it appears in, so an
     * offline tick reads back ticked wherever the row shows.
     */
    @Query("UPDATE tasks_cache SET done = :done WHERE note_id = :noteId AND line = :line")
    suspend fun flipTaskRow(noteId: String, line: Int, done: Boolean)

    @Query("DELETE FROM tasks_cache")
    suspend fun clearTasks()

    @Query("DELETE FROM task_fetches")
    suspend fun clearTaskFetches()

    /** The replica's tag counts, the tag filter's offline answer. */
    @Query("SELECT tag, COUNT(*) as count FROM tags GROUP BY tag ORDER BY tag")
    suspend fun tagCounts(): List<TagCountRow>

    /**
     * One whole sync: notes and tags replaced, spaces replaced, bodies
     * kept for surviving notes and seeded (title only) for new ones,
     * the tree replaced. Notes whose offline create is still queued
     * ([keep]) survive the replace: the server has not answered for
     * them, so the replica is the only place they exist. The triggers
     * declared beside the database keep the FTS index in step with
     * note_bodies.
     */
    @Transaction
    suspend fun applySync(
        spaces: List<SpaceEntity>,
        notes: List<Pair<NoteEntity, List<String>>>,
        tree: List<TreeNodeEntity>,
        keep: List<String> = emptyList(),
    ) {
        for ((n, tags) in notes) {
            upsertNote(n)
            clearTags(n.id)
            for (t in tags) addTag(n.id, t)
        }
        for (s in spaces) upsertSpace(s)
        dropMissingSpaces(spaces.map { it.name })
        // Gone is computed in Kotlin and deleted in positive batches:
        // a NOT IN batch per chunk would delete every note outside that
        // one chunk, which is every note once the list passes one.
        val staying = notes.map { it.first.id }.toSet() + keep.toSet()
        val gone = allNoteIds().filter { it !in staying }
        for (batch in gone.chunked(500)) deleteNotes(batch)
        seedBodies()
        syncBodyTitles()
        replaceTree(tree)
    }

    @Query("SELECT id FROM notes")
    suspend fun allNoteIds(): List<String>

    @Query("DELETE FROM notes WHERE id IN (:ids)")
    suspend fun deleteNotes(ids: List<String>)

    /**
     * Insert-or-update that keeps a surviving note's rowid, so the body
     * and FTS rows keyed by it stay attached across syncs.
     */
    @Upsert
    suspend fun upsertNote(n: NoteEntity)

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun upsertSpace(s: SpaceEntity)

    @Query("DELETE FROM tags WHERE note_id = :noteId")
    suspend fun clearTags(noteId: String)

    @Query("INSERT OR IGNORE INTO tags (note_id, tag) VALUES (:noteId, :tag)")
    suspend fun addTag(noteId: String, tag: String)

    @Query("DELETE FROM spaces WHERE name NOT IN (:names)")
    suspend fun dropMissingSpaces(names: List<String>)

    /** A body row for every note, so every title is in the FTS index. */
    @Query(
        """
        INSERT INTO note_bodies (note_rowid, title, body, raw_body)
        SELECT n.rowid, n.title, '', '' FROM notes n
        LEFT JOIN note_bodies b ON b.note_rowid = n.rowid
        WHERE b.note_rowid IS NULL
        """,
    )
    suspend fun seedBodies()

    /** Title changes ride into the index through the update trigger. */
    @Query(
        """
        UPDATE note_bodies SET title = (SELECT n.title FROM notes n WHERE n.rowid = note_bodies.note_rowid)
        WHERE title <> (SELECT n.title FROM notes n WHERE n.rowid = note_bodies.note_rowid)
        """,
    )
    suspend fun syncBodyTitles()

    @Query("DELETE FROM tree_nodes")
    suspend fun clearTree()

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun insertTree(nodes: List<TreeNodeEntity>)

    @Transaction
    suspend fun replaceTree(nodes: List<TreeNodeEntity>) {
        clearTree()
        insertTree(nodes)
    }

    @Query(
        """
        INSERT INTO note_bodies (note_rowid, title, body, raw_body)
        VALUES (
            (SELECT rowid FROM notes WHERE id = :noteId),
            (SELECT title FROM notes WHERE id = :noteId),
            :body, :raw)
        ON CONFLICT(note_rowid) DO UPDATE SET
            title = excluded.title, body = excluded.body, raw_body = excluded.raw_body
        """,
    )
    suspend fun storeBody(noteId: String, body: String, raw: String)

    /** A whole-replica reset: what a change of account does. */
    @Transaction
    suspend fun wipe() {
        clearTagsForAll()
        clearNotes()
        clearSpaces()
        clearTree()
        clearPendingOps()
        clearBodies()
        clearMeta()
        clearCrdtState()
        clearOutbox()
        clearTasks()
        clearTaskFetches()
    }

    @Query("DELETE FROM tags")
    suspend fun clearTagsForAll()

    @Query("DELETE FROM notes")
    suspend fun clearNotes()

    @Query("DELETE FROM spaces")
    suspend fun clearSpaces()

    @Query("DELETE FROM note_bodies")
    suspend fun clearBodies()

    @Query("DELETE FROM replica_meta")
    suspend fun clearMeta()

    @Query("DELETE FROM note_crdt")
    suspend fun clearCrdtState()

    @Query("DELETE FROM crdt_outbox")
    suspend fun clearOutbox()

    @RawQuery(observedEntities = [NoteEntity::class, TagEntity::class, NoteBodyEntity::class])
    suspend fun rawSearch(query: SupportSQLiteQuery): List<LocalHitRow>
}

/**
 * The offline replica: the same cache tables the server keeps, in Room,
 * with the same FTS5 trigram index over note titles and bodies. The FTS
 * table and its triggers are raw SQL declared in onCreate because Room's
 * annotations cover FTS4 only; the DDL is the server's
 * (internal/index/migrations/001_initial.sql and 006) word for word, so
 * tokenization and ranking behave identically.
 */
@Database(
    entities = [
        SpaceEntity::class,
        NoteEntity::class,
        TagEntity::class,
        NoteBodyEntity::class,
        TreeNodeEntity::class,
        PendingOpEntity::class,
        ReplicaMetaEntity::class,
        NoteCrdtEntity::class,
        CrdtOutboxEntity::class,
        TaskCacheEntity::class,
        TaskFetchEntity::class,
    ],
    version = 3,
    exportSchema = false,
)
abstract class ReplicaDatabase : RoomDatabase() {
    abstract fun dao(): ReplicaDao

    companion object {
        /**
         * Version 2 adds the CRDT tables: one note-state row per note
         * the editor has opened, and the outbox of updates the server
         * has not confirmed. The DDL matches what Room generates from
         * the entities above.
         */
        val MIGRATION_1_2 = object : Migration(1, 2) {
            override fun migrate(db: androidx.sqlite.SQLiteConnection) {
                fun sql(stmt: String) = db.prepare(stmt).use { it.step() }
                sql("CREATE TABLE IF NOT EXISTS `note_crdt` (`note_id` TEXT NOT NULL PRIMARY KEY, `state` BLOB NOT NULL, `opened_at` INTEGER NOT NULL, `updated_at` INTEGER NOT NULL)")
                sql("CREATE TABLE IF NOT EXISTS `crdt_outbox` (`seq` INTEGER PRIMARY KEY AUTOINCREMENT, `note_id` TEXT NOT NULL, `payload` BLOB NOT NULL, `created_at` INTEGER NOT NULL)")
                sql("CREATE INDEX IF NOT EXISTS `index_crdt_outbox_note_id` ON `crdt_outbox` (`note_id`)")
            }
        }

        /**
         * Version 3 adds the tasks cache: the last fetched listing per
         * filter scope, with its fetch time. The DDL matches what Room
         * generates from the entities above.
         */
        val MIGRATION_2_3 = object : Migration(2, 3) {
            override fun migrate(db: androidx.sqlite.SQLiteConnection) {
                fun sql(stmt: String) = db.prepare(stmt).use { it.step() }
                sql(
                    "CREATE TABLE IF NOT EXISTS `tasks_cache` (`scope` TEXT NOT NULL, `note_id` TEXT NOT NULL, `line` INTEGER NOT NULL, `indent` INTEGER NOT NULL, `text` TEXT NOT NULL, `done` INTEGER NOT NULL, `done_at` TEXT, `heading` TEXT NOT NULL, `ord` INTEGER NOT NULL, `space` TEXT NOT NULL, `path` TEXT NOT NULL, `title` TEXT NOT NULL, `kind` TEXT NOT NULL, PRIMARY KEY(`scope`, `note_id`, `line`))",
                )
                sql("CREATE INDEX IF NOT EXISTS `index_tasks_cache_scope` ON `tasks_cache` (`scope`)")
                sql("CREATE TABLE IF NOT EXISTS `task_fetches` (`scope` TEXT NOT NULL PRIMARY KEY, `fetched_at` INTEGER NOT NULL)")
            }
        }

        /** The FTS5 index and the triggers that keep it in step. */
        val FTS_DDL = listOf(
            """
            CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
                title, body,
                content = 'note_bodies',
                content_rowid = 'note_rowid',
                tokenize = 'trigram'
            )
            """.trimIndent(),
            """
            CREATE TRIGGER IF NOT EXISTS note_bodies_ai AFTER INSERT ON note_bodies BEGIN
                INSERT INTO notes_fts(rowid, title, body) VALUES (new.note_rowid, new.title, new.body);
            END
            """.trimIndent(),
            """
            CREATE TRIGGER IF NOT EXISTS note_bodies_ad AFTER DELETE ON note_bodies BEGIN
                INSERT INTO notes_fts(notes_fts, rowid, title, body) VALUES ('delete', old.note_rowid, old.title, old.body);
            END
            """.trimIndent(),
            """
            CREATE TRIGGER IF NOT EXISTS note_bodies_au AFTER UPDATE ON note_bodies BEGIN
                INSERT INTO notes_fts(notes_fts, rowid, title, body) VALUES ('delete', old.note_rowid, old.title, old.body);
                INSERT INTO notes_fts(rowid, title, body) VALUES (new.note_rowid, new.title, new.body);
            END
            """.trimIndent(),
            """
            CREATE TRIGGER IF NOT EXISTS notes_ad AFTER DELETE ON notes BEGIN
                DELETE FROM note_bodies WHERE note_rowid = old.rowid;
            END
            """.trimIndent(),
        )
    }
}
