package com.collinpendleton.yana.data.rt

/**
 * The persistence the sync engine needs: one state blob per note and
 * the outbox of unconfirmed updates. The replica's Room store
 * implements it; the JVM test suite supplies an in-memory one.
 */
interface RtStore {
    /** The note's persisted document state, or null when there is none. */
    suspend fun crdtState(noteId: String): ByteArray?

    /** Persists the note's document state. */
    suspend fun storeCrdtState(noteId: String, state: ByteArray)

    /** Marks a note opened now, seeding an empty state row when it is new. */
    suspend fun touchOpened(noteId: String)

    /** The notes opened most recently, freshest first. */
    suspend fun recentlyOpened(limit: Int): List<String>

    /** Queues one encoded local update the server has not confirmed. */
    suspend fun addOutboxUpdate(noteId: String, update: ByteArray)

    /** The note's unconfirmed updates, oldest first. */
    suspend fun outboxFor(noteId: String): List<OutboxRow>

    /** Notes with unconfirmed updates. */
    suspend fun outboxNotes(): List<String>

    /** Drops rows the server has confirmed; the argument is a pong's watermark. */
    suspend fun dropOutboxTo(seq: Long)

    suspend fun outboxCount(): Int

    /** The note's kind from the replica, or null when it is not cached. */
    suspend fun noteKind(noteId: String): String?

    /** Caches a body so offline reading and search see the document's text. */
    suspend fun storeBody(noteId: String, kind: String, text: String)
}

/** One unconfirmed update, with the sequence number its pong confirms. */
data class OutboxRow(val seq: Long, val payload: ByteArray)
