package com.collinpendleton.yana.ui.editor

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The editor's pure pieces: the diff that turns one field change into
 * one document op, the selection mapping through committed hunks, undo
 * bursts, and the cursor throttle. The cursor rule (an insert exactly at
 * the cursor pushes it after; a delete over it collapses it to the
 * replacement) is the one the reference resolved in the cross-language
 * fixtures.
 */
class EditorLogicTest {
    // --- the diff shim ------------------------------------------------------

    @Test
    fun diffFindsTheSingleRegion() {
        assertEquals(TextFieldDiff.Op(0, 0, "hello"), TextFieldDiff.diff("", "hello"))
        assertEquals(TextFieldDiff.Op(0, 5, ""), TextFieldDiff.diff("hello", ""))
        assertEquals(TextFieldDiff.Op(5, 0, " world"), TextFieldDiff.diff("hello", "hello world"))
        assertEquals(TextFieldDiff.Op(2, 2, "XY"), TextFieldDiff.diff("hello", "heXYo"))
        assertNull(TextFieldDiff.diff("same", "same"))
        // A selection replaced by longer text: one region.
        assertEquals(TextFieldDiff.Op(6, 5, "there"), TextFieldDiff.diff("hello world", "hello there"))
    }

    @Test
    fun diffCountsInUtf16Units() {
        // "a😀b" is 4 units; the emoji is 2.
        val text = "a😀b"
        assertEquals(TextFieldDiff.Op(3, 0, "X"), TextFieldDiff.diff(text, "a😀Xb"))
        // Deleting the emoji removes both units at once.
        assertEquals(TextFieldDiff.Op(1, 2, ""), TextFieldDiff.diff(text, "ab"))
        // Typing INTO the middle of the emoji is not something a text
        // field produces; a combining character next to one is.
        assertEquals(TextFieldDiff.Op(3, 0, "\u0301"), TextFieldDiff.diff(text, "a😀\u0301b"))
        // A paste with emoji and combining characters over a range.
        val from = "x 😀 y"
        val to = "x 👩‍🔧 y"
        val op = TextFieldDiff.diff(from, to)!!
        // Round-trips: applying the op reproduces the new text.
        assertEquals(to, from.substring(0, op.pos) + op.insert + from.substring(op.pos + op.del))
        // And the region does not split the emoji it replaces: "😀" is
        // 2 units, "👩‍🔧" is 5.
        assertEquals(2, op.pos)
        assertEquals(2, op.del)
        assertEquals(5, op.insert.length)
    }

    // --- selection mapping --------------------------------------------------

    @Test
    fun mappingMatchesTheCollaborativeRule() {
        // A remote insert exactly at the cursor pushes it after — the
        // behavior the reference fixture pinned (position at index 1
        // with an insert at 1 resolves to 3).
        val insertAt1 = listOf(SelectionMapper.Hunk(1, 0, 2))
        assertEquals(3, SelectionMapper.mapIndex(insertAt1, 1, 10))
        assertEquals(0, SelectionMapper.mapIndex(insertAt1, 0, 10))
        assertEquals(5, SelectionMapper.mapIndex(insertAt1, 3, 10))

        // A remote delete before the cursor shifts it back; over the
        // cursor it collapses to the deletion point.
        val del = listOf(SelectionMapper.Hunk(2, 4, 0))
        assertEquals(0, SelectionMapper.mapIndex(del, 0, 10))
        assertEquals(2, SelectionMapper.mapIndex(del, 2, 10))
        assertEquals(2, SelectionMapper.mapIndex(del, 4, 10))
        assertEquals(4, SelectionMapper.mapIndex(del, 8, 10))

        // A replacement: endpoints inside land at the end of the
        // inserted text, endpoints before stay, endpoints after shift.
        val repl = listOf(SelectionMapper.Hunk(3, 3, 5))
        assertEquals(1, SelectionMapper.mapIndex(repl, 1, 10))
        assertEquals(8, SelectionMapper.mapIndex(repl, 3, 10))
        assertEquals(8, SelectionMapper.mapIndex(repl, 5, 10))
        assertEquals(10, SelectionMapper.mapIndex(repl, 8, 10))

        // Several hunks apply in one pass. "0123456789" with "XY"
        // inserted at 1 and [6,8) deleted reads "0XY1234589"; a cursor
        // at old 3 lands at 5, one at old 6 (the deleted 6) collapses
        // to the deletion point at 8.
        val two = listOf(SelectionMapper.Hunk(1, 0, 2), SelectionMapper.Hunk(6, 2, 0))
        assertEquals(5, SelectionMapper.mapIndex(two, 3, 10))
        assertEquals(8, SelectionMapper.mapIndex(two, 6, 10))
    }

    @Test
    fun mappingParsesTheDocumentHunks() {
        val hunks = SelectionMapper.parse("""[{"p":3,"d":0,"i":5},{"p":10,"d":2,"i":0}]""")
        assertEquals(listOf(SelectionMapper.Hunk(3, 0, 5), SelectionMapper.Hunk(10, 2, 0)), hunks)
        assertEquals(0, SelectionMapper.parse(null).size)
        assertEquals(0, SelectionMapper.parse("not json").size)
        // A cursor past a shrunk text clamps.
        assertEquals(5, SelectionMapper.mapIndex(emptyList(), 9, 5))
    }

    @Test
    fun mappingASelectionKeepsItOrdered() {
        val del = listOf(SelectionMapper.Hunk(0, 4, 0))
        val (s, e) = SelectionMapper.mapSelection(del, 2, 6, 10)
        assertEquals(0, s)
        assertEquals(2, e)
    }

    // --- undo bursts ----------------------------------------------------------

    @Test
    fun typingBurstsUndoTogether() {
        var t = 0L
        val b = UndoBursts(700) { t }
        repeat(5) { b.onEdit() } // one burst, no pause
        assertEquals(5, b.nextUndoSteps())
        b.onUndone()
        assertEquals(0, b.nextUndoSteps())
        assertEquals(5, b.nextRedoSteps())
        b.onRedone()
        assertEquals(5, b.nextUndoSteps())
    }

    @Test
    fun aPauseStartsANewBurstAndTypingClearsRedo() {
        var t = 0L
        val b = UndoBursts(700) { t }
        repeat(3) { b.onEdit() }
        t += 800
        repeat(2) { b.onEdit() }
        assertEquals(2, b.nextUndoSteps())
        b.onUndone()
        assertEquals(3, b.nextUndoSteps())
        assertEquals(2, b.nextRedoSteps())
        t += 800
        b.onEdit() // typing clears the redo plan
        assertEquals(0, b.nextRedoSteps())
        assertEquals(1, b.nextUndoSteps())
    }

    @Test
    fun aFormattingButtonUndoesAlone() {
        var t = 0L
        val b = UndoBursts(700) { t }
        repeat(4) { b.onEdit() } // typing right up to the button
        b.onAlone() // the button's own op
        // Undo takes the button alone, not the typing with it.
        assertEquals(1, b.nextUndoSteps())
        b.onUndone()
        assertEquals(4, b.nextUndoSteps())
        // And the typing after the button starts a burst of its own,
        // still inside the window.
        b.onEdit()
        assertEquals(1, b.nextUndoSteps())
        b.onUndone()
        assertEquals(4, b.nextUndoSteps())
        // A button right after a button is still one step each.
        b.onAlone()
        b.onAlone()
        assertEquals(1, b.nextUndoSteps())
        b.onUndone()
        assertEquals(1, b.nextUndoSteps())
    }

    // --- the cursor throttle ----------------------------------------------------

    @Test
    fun throttleSendsAtMostOnePerWindowAndAlwaysTheLast() {
        var t = 0L
        val th = CursorThrottle(50) { t }
        assertArrayEquals(intArrayOf(0, 0), th.offer(0, 0)) // first goes at once
        assertNull(th.offer(10, 10)) // within the window: queued
        assertNull(th.offer(20, 20)) // queued again; the latest wins
        t += 50
        assertArrayEquals(intArrayOf(20, 20), th.due()) // trailing edge
        assertNull(th.due())
    }

    // --- the keystroke budget ---------------------------------------------------

    /**
     * The 16ms frame budget for a keystroke in a 100KB note, measured on
     * the editor's share of the frame: the diff shim, the hunk mapping
     * of the cursor, and the hunk JSON parse. The document op itself is
     * under 0.5ms there per the Phase 0 harness (docs/crdt-decision.md).
     * The assertion is generous — this is a manual-check stand-in for a
     * macrobenchmark, which needs a device.
     */
    @Test
    fun aKeystrokeInA100KbNoteStaysInBudget() {
        val builder = StringBuilder()
        while (builder.length < 100 * 1024) {
            builder.append("The quick brown fox jumps over the lazy dog. 🦊\n")
        }
        val base = builder.toString()
        check(base.length >= 100 * 1024)

        // Warm the JIT, then measure 200 appends at the very end — the
        // worst case for the prefix scan.
        var total = 0L
        var old = base
        repeat(50) { TextFieldDiff.diff(old, old + "x"); old += "x" }
        old = base
        repeat(200) { i ->
            val newText = old + "x"
            val start = System.nanoTime()
            val op = TextFieldDiff.diff(old, newText)!!
            val hunks = SelectionMapper.parse("""[{"p":${op.pos},"d":${op.del},"i":1}]""")
            SelectionMapper.mapSelection(hunks, op.pos, op.pos, newText.length)
            total += System.nanoTime() - start
            old = newText
        }
        val medianMs = total / 200 / 1_000_000.0
        // The budget is 16ms; the shim should sit orders of magnitude
        // under it even on a JVM, and a device JIT does no worse here.
        assertEquals(true, medianMs < 8)
    }
}
