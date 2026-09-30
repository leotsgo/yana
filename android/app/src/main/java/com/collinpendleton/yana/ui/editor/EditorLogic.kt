package com.collinpendleton.yana.ui.editor

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.int
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive

/**
 * The diff-to-ops shim: what one `onValueChange` means for the document.
 * A text field reports its whole new value; the change from the old one
 * is one contiguous region — a common prefix and suffix around it — which
 * is exactly one replacement: the delete and the insert of one
 * transaction, one update, one undo step.
 *
 * Every index is a UTF-16 code unit, the unit Kotlin strings, the text
 * field's selections, and the CRDT's offsets all share. The prefix and
 * suffix walks cannot split a surrogate pair when both strings are valid
 * and differ in one region; the guards keep a pair whole regardless.
 */
object TextFieldDiff {
    /** One replacement: [del] units at [pos] become [insert]. Null when the text did not change. */
    data class Op(val pos: Int, val del: Int, val insert: String)

    fun diff(old: String, new: String): Op? {
        if (old == new) return null
        val minLen = minOf(old.length, new.length)
        var p = 0
        while (p < minLen && old[p] == new[p]) p++
        var sfx = 0
        while (sfx < minLen - p && old[old.length - 1 - sfx] == new[new.length - 1 - sfx]) sfx++

        // Keep surrogate pairs out of the region's boundary.
        if (p > 0 && p < old.length && Character.isHighSurrogate(old[p - 1]) && Character.isLowSurrogate(old[p])) p--
        val b = old.length - sfx
        if (sfx > 0 && b > 0 && b < old.length && Character.isHighSurrogate(old[b - 1]) && Character.isLowSurrogate(old[b])) sfx++

        val del = old.length - p - sfx
        val insert = new.substring(p, new.length - sfx)
        if (del == 0 && insert.isEmpty()) return null
        return Op(p, del, insert)
    }
}

/**
 * Maps a selection through the replacement hunks of a committed change,
 * so remote edits, undo, and redo move the cursor instead of resetting
 * it. The rule matches the collaborative editors' convention: an
 * endpoint before a hunk stays put; one at or inside a replaced range
 * lands after the inserted text; one past it shifts by the difference.
 */
object SelectionMapper {
    /** One replacement region: [d] UTF-16 units at [p] became [i] units. */
    data class Hunk(val p: Int, val d: Int, val i: Int)

    /** Parses the hunks JSON the document delivers; empty when absent or malformed. */
    fun parse(delta: String?): List<Hunk> {
        if (delta.isNullOrEmpty()) return emptyList()
        return try {
            Json.parseToJsonElement(delta).jsonArray.map { el ->
                val o = el.jsonObject
                Hunk(
                    o.getValue("p").jsonPrimitive.int,
                    o.getValue("d").jsonPrimitive.int,
                    o.getValue("i").jsonPrimitive.int,
                )
            }
        } catch (_: Exception) {
            emptyList()
        }
    }

    /** Maps one endpoint through the hunks, clamped to the new text's length. */
    fun mapIndex(hunks: List<Hunk>, index: Int, newLength: Int): Int {
        var a = 0
        for (h in hunks) {
            if (index < h.p) break
            if (index >= h.p + h.d) {
                a += h.i - h.d
            } else {
                return clamp(h.p + h.i + a, newLength)
            }
        }
        return clamp(index + a, newLength)
    }

    /** Maps a selection's two endpoints; the result is ordered. */
    fun mapSelection(hunks: List<Hunk>, start: Int, end: Int, newLength: Int): Pair<Int, Int> {
        val s = mapIndex(hunks, start, newLength)
        val e = mapIndex(hunks, end, newLength)
        return minOf(s, e) to maxOf(s, e)
    }

    private fun clamp(v: Int, len: Int): Int = v.coerceIn(0, len)
}

/**
 * Undo grouping: the engine's stack is one step per edit, so a burst of
 * keystrokes is undone together by remembering where the pauses were.
 * Typing that follows a pause starts a new burst and clears the redo
 * plan, the same rule every editor uses.
 */
class UndoBursts(private val windowMs: Long = 700, private val now: () -> Long = System::currentTimeMillis) {
    private val undo = ArrayDeque<Int>()
    private val redo = ArrayDeque<Int>()
    private var lastEditAt = 0L
    private var breakNext = false

    /** Records one local edit op. */
    fun onEdit() {
        val t = now()
        if (breakNext || undo.isEmpty() || t - lastEditAt > windowMs) {
            undo.addLast(1)
        } else {
            undo.addLast(undo.removeLast() + 1)
        }
        breakNext = false
        lastEditAt = t
        redo.clear()
    }

    /**
     * One op the person sees as a single thing — a formatting button,
     * a completion pick — that undoes alone: it never joins the typing
     * before it, and the typing after it starts a burst of its own.
     */
    fun onAlone() {
        breakNext = true
        onEdit()
        breakNext = true
    }

    /** How many steps the next undo covers. */
    fun nextUndoSteps(): Int = undo.lastOrNull() ?: 0

    /** How many steps the next redo covers. */
    fun nextRedoSteps(): Int = redo.lastOrNull() ?: 0

    /** Called after an undo fired its steps. */
    fun onUndone() {
        val n = undo.removeLastOrNull() ?: return
        redo.addLast(n)
    }

    /** Called after a redo fired its steps. */
    fun onRedone() {
        val n = redo.removeLastOrNull() ?: return
        undo.addLast(n)
    }
}

/**
 * A trailing-edge throttle for cursor broadcasts: at most one send per
 * window, always delivering the latest position, so the final resting
 * place of the cursor always goes out. [offer] returns the position to
 * send at once or queues it; [due] hands the queue to a ticker once the
 * window has passed.
 */
class CursorThrottle(private val windowMs: Long = 50, private val now: () -> Long = System::currentTimeMillis) {
    private var lastSentAt = Long.MIN_VALUE
    private var pending: IntArray? = null

    /** Returns the (anchor, head) to send now, or null when it is queued. */
    fun offer(anchor: Int, head: Int): IntArray? {
        pending = intArrayOf(anchor, head)
        return if (lastSentAt == Long.MIN_VALUE || now() - lastSentAt >= windowMs) take() else null
    }

    /** The queued (anchor, head) once the window has passed, or null. */
    fun due(): IntArray? {
        if (pending == null || lastSentAt == Long.MIN_VALUE) return null
        return if (now() - lastSentAt >= windowMs) take() else null
    }

    private fun take(): IntArray {
        lastSentAt = now()
        val p = pending!!
        pending = null
        return p
    }
}
