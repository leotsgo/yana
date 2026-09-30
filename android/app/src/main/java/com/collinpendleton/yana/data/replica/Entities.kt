package com.collinpendleton.yana.data.replica

import androidx.room.ColumnInfo
import androidx.room.Entity
import androidx.room.ForeignKey
import androidx.room.Index
import androidx.room.PrimaryKey

/**
 * The replica's tables mirror the server's SQLite cache (the tables the
 * client needs): spaces, notes, tags, note_bodies, plus a materialised
 * folder tree and the pending_ops queue. Everything here arrived in a
 * server response for the signed-in account; a note from any other space
 * never enters the replica, so offline search cannot leak it.
 */

/** One space the account belongs to. */
@Entity(tableName = "spaces")
data class SpaceEntity(
    @PrimaryKey val name: String,
    val label: String,
    val notes: Int,
)

/**
 * One note row. Timestamps are epoch nanoseconds, the unit the server
 * orders by, so offline date filters and newest-first ordering sort
 * exactly as the server's do. The content hash rides along so an HTML
 * source save can say what it was based on and a diverged version on
 * the server is parked, not overwritten.
 */
@Entity(
    tableName = "notes",
    indices = [Index(value = ["rel_path"], unique = true)],
)
data class NoteEntity(
    @PrimaryKey val id: String,
    val space: String,
    @ColumnInfo(name = "rel_path") val relPath: String,
    val title: String,
    val preview: String,
    val kind: String,
    @ColumnInfo(name = "content_hash") val contentHash: String = "",
    val created: Long,
    @ColumnInfo(name = "updated_at") val updatedAt: Long,
)

/** A note's tags, mirroring the server's tags table. */
@Entity(
    tableName = "tags",
    primaryKeys = ["note_id", "tag"],
    foreignKeys = [
        ForeignKey(entity = NoteEntity::class, parentColumns = ["id"], childColumns = ["note_id"], onDelete = ForeignKey.CASCADE),
    ],
    indices = [Index("note_id")],
)
data class TagEntity(
    @ColumnInfo(name = "note_id") val noteId: String,
    val tag: String,
)

/**
 * The text of a note the user has opened, mirroring the server's
 * note_bodies: title and body feed the FTS index (body is what search
 * reads — markdown without frontmatter, or tag-stripped HTML), raw_body
 * is what offline reading shows. Notes never opened have a body row with
 * empty text so their titles stay searchable; the CRDT sync fills the
 * bodies in.
 */
@Entity(tableName = "note_bodies")
data class NoteBodyEntity(
    @PrimaryKey @ColumnInfo(name = "note_rowid") val noteRowid: Long,
    val title: String,
    val body: String,
    @ColumnInfo(name = "raw_body") val rawBody: String,
)

/**
 * One flattened node of the folder tree, as the server sent it: the
 * nesting (parent), the server's sort order (position), and every flag
 * the tree response carries (public links, conflict copies) survive a
 * round trip offline.
 */
@Entity(tableName = "tree_nodes")
data class TreeNodeEntity(
    @PrimaryKey val path: String,
    val space: String,
    /** Parent node's path; "" for a space's top level. */
    val parent: String,
    val type: String,
    val name: String,
    val id: String? = null,
    val title: String? = null,
    val kind: String? = null,
    val order: Int? = null,
    /** Tags joined with ","; the grammar does not allow commas in tags. */
    val tags: String = "",
    val public: Boolean = false,
    val conflict: Boolean = false,
    @ColumnInfo(name = "conflict_of") val conflictOf: String? = null,
    val position: Int = 0,
)

/**
 * One offline action waiting for the network: create a note, append to
 * one, move one, or tick a task box. Payload is the op's JSON; replay
 * drains the queue in order.
 */
@Entity(tableName = "pending_ops")
data class PendingOpEntity(
    @PrimaryKey(autoGenerate = true) val seq: Long = 0,
    val type: String,
    val payload: String,
    @ColumnInfo(name = "created_at") val createdAt: Long,
)

/**
 * One note's local CRDT document: the compacted state of every update
 * this device has applied or authored, so a note edited offline or a
 * process death loses nothing. `opened_at` drives the background sync's
 * idea of "recently opened"; `updated_at` is when the state last
 * changed. A Room blob rather than a file: one database means one wipe
 * path and the outbox below can commit beside it.
 */
@Entity(tableName = "note_crdt")
data class NoteCrdtEntity(
    @PrimaryKey @ColumnInfo(name = "note_id") val noteId: String,
    val state: ByteArray,
    @ColumnInfo(name = "opened_at") val openedAt: Long,
    @ColumnInfo(name = "updated_at") val updatedAt: Long,
)

/**
 * One encoded local update the server has not confirmed. Rows leave
 * only after a pong that proves the server read the frame that carried
 * them; until then a reconnect re-sends them, and CRDT idempotence
 * makes the duplicate harmless.
 */
@Entity(
    tableName = "crdt_outbox",
    indices = [Index("note_id")],
)
data class CrdtOutboxEntity(
    @PrimaryKey(autoGenerate = true) val seq: Long = 0,
    @ColumnInfo(name = "note_id") val noteId: String,
    val payload: ByteArray,
    @ColumnInfo(name = "created_at") val createdAt: Long,
)

/** Replica bookkeeping: whose notes these are (server and user id). */
@Entity(tableName = "replica_meta")
data class ReplicaMetaEntity(
    @PrimaryKey val key: String,
    val value: String,
)

/**
 * One row of the last fetched tasks listing, cached per filter scope so
 * airplane mode shows the list it last had. `ord` keeps the server's
 * row order (notes newest-updated first, file order within a note); the
 * task's own fields ride along beside the trimmed note they belong to.
 */
@Entity(
    tableName = "tasks_cache",
    primaryKeys = ["scope", "note_id", "line"],
    indices = [Index("scope")],
)
data class TaskCacheEntity(
    val scope: String,
    @ColumnInfo(name = "note_id") val noteId: String,
    val line: Int,
    val indent: Int = 0,
    val text: String = "",
    val done: Boolean = false,
    @ColumnInfo(name = "done_at") val doneAt: String? = null,
    val heading: String = "",
    val ord: Int = 0,
    val space: String = "",
    val path: String = "",
    val title: String = "",
    val kind: String = "md",
)

/** When a filter scope's task list was last fetched, for its age offline. */
@Entity(tableName = "task_fetches")
data class TaskFetchEntity(
    @PrimaryKey val scope: String,
    @ColumnInfo(name = "fetched_at") val fetchedAt: Long,
)
