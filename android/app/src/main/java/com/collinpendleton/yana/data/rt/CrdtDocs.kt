package com.collinpendleton.yana.data.rt

import com.collinpendleton.yana.crdt.crdt.Crdt
import com.collinpendleton.yana.crdt.crdt.TextObserver
import com.collinpendleton.yana.crdt.crdt.UndoManager

/**
 * One text change a document committed: the text after it, the change as
 * replacement hunks — a JSON array like [{"p":3,"d":0,"i":5}], null when
 * the change carried none — and whether this device authored it. Hunks
 * count UTF-16 code units, the unit the text field's offsets use.
 */
data class TextEdit(val text: String, val delta: String?, val local: Boolean)

/** The document's text-change feed. Implement and pass to [RtDoc.observe]. */
interface RtTextObserver {
    /**
     * One notification per committed transaction that changed the text.
     * Runs on a background thread in commit order; hop to the caller's
     * dispatcher and return promptly.
     */
    fun onText(text: String, delta: String?, local: Boolean)
}

/**
 * The document surface the sync engine needs from one note: the same
 * operations the AAR's bind package exposes, narrowed to what sync
 * calls. The production implementation wraps the AAR; the JVM test
 * suite substitutes a fake, because the AAR's native libraries only
 * load on a device. Every method is blocking; the engine calls them on
 * its IO dispatcher only.
 */
interface RtDoc {
    val noteId: String

    /** The body as text; every replica converges on the same one. */
    fun text(): String

    /** The document's clock, sent with a subscribe. */
    fun stateVector(): ByteArray

    /** The whole document as one update: the persisted snapshot. */
    fun state(): ByteArray

    /** Integrates a peer's update; true when the document gained anything. */
    fun apply(update: ByteArray): Boolean

    /**
     * Mutates the body so it reads as `want` — the whole-text diff
     * path. Returns the update to forward, or null when the body
     * already read as `want`.
     */
    fun replaceText(want: String): ByteArray?

    /**
     * Replaces [del] UTF-16 units at [pos] with [insert] in one
     * transaction — one update to forward, one undo step. The shape a
     * text field's diff produces for one keystroke.
     */
    fun edit(pos: Int, del: Int, insert: String): ByteArray?

    /**
     * Applies several replacement hunks in one transaction, so a
     * formatting button is one update and one undo step. [ops] is the
     * JSON the bind package takes: an array of {"p":pos,"d":del,"i":ins}
     * hunks measured against the text as it stands, not overlapping.
     */
    fun editOps(ops: String): ByteArray?

    /** Reverts the most recent local edit; the update to forward, or null when there is nothing to undo. */
    fun undo(): ByteArray?

    /** Re-applies the most recently undone local edit; the update to forward, or null when there is nothing to redo. */
    fun redo(): ByteArray?

    /** Local undo steps still on the stack. */
    fun undoDepth(): Long

    /** Redo steps still on the stack. */
    fun redoDepth(): Long

    /** Subscribes to text changes; returns the unsubscribe function. */
    fun observe(observer: RtTextObserver): () -> Unit

    /** The document's client id, the key awareness states use. */
    fun clientID(): Long

    /** A cursor at UTF-16 [index] as the JSON relative position the web's awareness carries, or null on a closed document. */
    fun positionJSON(index: Int, assoc: Int): ByteArray?

    /** Resolves such a JSON position to a UTF-16 index, or -1 when it cannot be resolved. */
    fun resolvePosition(json: ByteArray): Int

    fun close()
}

/**
 * Opens documents and merges updates. One interface so the JVM test
 * suite can stand in for the AAR, whose native libraries only load on a
 * device.
 */
interface RtDocFactory {
    fun open(noteId: String, state: ByteArray): RtDoc

    /** Combines two updates into one a fresh replica loads directly. */
    fun merge(a: ByteArray, b: ByteArray): ByteArray
}

/** Folds the factory's pairwise merge over a batch. */
fun RtDocFactory.mergeAll(updates: List<ByteArray>): ByteArray? {
    if (updates.isEmpty()) return null
    var merged = updates[0]
    for (i in 1 until updates.size) merged = merge(merged, updates[i])
    return merged
}

/** Opens documents in the Go CRDT engine (the AAR `make android-crdt` builds). */
class GoDocFactory : RtDocFactory {
    override fun open(noteId: String, state: ByteArray): RtDoc = GoDoc(noteId, state)

    override fun merge(a: ByteArray, b: ByteArray): ByteArray = Crdt.mergeUpdates(a, b)
}

/** One note's document in the Go CRDT engine. */
private class GoDoc(override val noteId: String, state: ByteArray) : RtDoc {
    private val doc = Crdt.loadDoc(state)
    private val undoManager = Crdt.newUndoManager(doc)

    override fun text(): String = doc.text()

    override fun stateVector(): ByteArray = doc.stateVector()

    override fun state(): ByteArray = doc.state()

    override fun apply(update: ByteArray): Boolean {
        val fresh = doc.applyUpdate(update)
        return fresh != null && fresh.isNotEmpty()
    }

    override fun replaceText(want: String): ByteArray? = doc.replaceText(want)

    override fun edit(pos: Int, del: Int, insert: String): ByteArray? = doc.edit(pos.toLong(), del.toLong(), insert)

    override fun editOps(ops: String): ByteArray? = doc.editMany(ops)

    override fun undo(): ByteArray? = undoManager.undo()

    override fun redo(): ByteArray? = undoManager.redo()

    override fun undoDepth(): Long = undoManager.undoStackSize()

    override fun redoDepth(): Long = undoManager.redoStackSize()

    override fun observe(observer: RtTextObserver): () -> Unit {
        val sub = doc.observeText(
            object : TextObserver {
                override fun onText(text: String, delta: String?, local: Boolean) {
                    observer.onText(text, if (delta.isNullOrEmpty()) null else delta, local)
                }
            },
        )
        return { sub.close() }
    }

    override fun clientID(): Long = doc.clientID()

    override fun positionJSON(index: Int, assoc: Int): ByteArray? = doc.relativePositionJSON(index.toLong(), assoc.toLong())

    override fun resolvePosition(json: ByteArray): Int = doc.resolveRelativePositionJSON(json).toInt()

    override fun close() {
        undoManager.destroy()
        doc.close()
    }
}
