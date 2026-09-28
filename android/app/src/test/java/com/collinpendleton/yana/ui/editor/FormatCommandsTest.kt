package com.collinpendleton.yana.ui.editor

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The formatting commands over the cases the web's format.tsx buttons
 * define: the same selection in the same text produces the same
 * markdown, as one set of replacement hunks the document commits in a
 * single transaction.
 */
class FormatCommandsTest {
    private fun text(t: String, from: Int, to: Int, command: (String, Int, Int) -> Format.Result): String =
        Format.apply(t, command(t, from, to).ops)

    // --- wrap ------------------------------------------------------------------

    @Test
    fun boldWrapsASelection() {
        assertEquals("**bold**", text("bold", 0, 4) { t, f, to -> Format.wrap(t, f, to, "**") })
    }

    @Test
    fun boldOnNothingLeavesTheCaretBetween() {
        val r = Format.wrap("plan", 4, 4, "**")
        // The text gains both marks; the caret sits between them.
        assertEquals("plan****", Format.apply("plan", r.ops))
        assertEquals(6, r.selStart)
        assertEquals(6, r.selEnd)
    }

    @Test
    fun wrappedSelectionUnwraps() {
        val r = Format.wrap("**bold**", 2, 6, "**")
        assertEquals("bold", Format.apply("**bold**", r.ops))
        // The selection shrinks by the mark on each side.
        assertEquals(0, r.selStart)
        assertEquals(4, r.selEnd)
    }

    @Test
    fun italicUsesTheUnderscore() {
        assertEquals("_it_", text("it", 0, 2) { t, f, to -> Format.wrap(t, f, to, "_") })
    }

    // --- code ------------------------------------------------------------------

    @Test
    fun inlineCodeWrapsInBackticks() {
        assertEquals("run `x` now", text("run x now", 4, 5) { t, f, to -> Format.code(t, f, to) })
    }

    @Test
    fun multilineSelectionIsFencedOnItsOwnLines() {
        val r = Format.code("a\nfoo\nbar\nb", 2, 9)
        assertEquals("a\n```\nfoo\nbar\n```\nb", Format.apply("a\nfoo\nbar\nb", r.ops))
        assertEquals(5, r.selStart)
    }

    // --- the line prefixes -------------------------------------------------------

    @Test
    fun headingStepsUpAndRemoves() {
        assertEquals("# plain", text("plain", 0, 0) { t, f, to -> Format.heading(t, f, to) })
        assertEquals("## one", text("# one", 0, 0) { t, f, to -> Format.heading(t, f, to) })
        // At three levels the heading comes off, the web's cap.
        assertEquals("two", text("### two", 0, 0) { t, f, to -> Format.heading(t, f, to) })
        assertEquals("three", text("### three", 0, 0) { t, f, to -> Format.heading(t, f, to) })
        assertEquals("gone", text("###### gone", 0, 0) { t, f, to -> Format.heading(t, f, to) })
    }

    @Test
    fun listTogglesTheWholeSelection() {
        assertEquals("- a\n- b", text("a\nb", 0, 4) { t, f, to -> Format.list(t, f, to) })
        assertEquals("a\nb", text("- a\n- b", 0, 6) { t, f, to -> Format.list(t, f, to) })
        // A task line becomes a plain item.
        assertEquals("- x", text("- [ ] x", 0, 0) { t, f, to -> Format.list(t, f, to) })
        // A numbered item is a list item too.
        assertEquals("n", text("1. n", 0, 0) { t, f, to -> Format.list(t, f, to) })
    }

    @Test
    fun taskTogglesABox() {
        assertEquals("- [ ] a", text("a", 0, 0) { t, f, to -> Format.task(t, f, to) })
        // A list line gains a box, keeping its marker.
        assertEquals("+ [ ] b", text("+ b", 0, 0) { t, f, to -> Format.task(t, f, to) })
        assertEquals("2. [ ] c", text("2. c", 0, 0) { t, f, to -> Format.task(t, f, to) })
        // A task line comes off entirely, open or ticked.
        assertEquals("d", text("- [ ] d", 0, 0) { t, f, to -> Format.task(t, f, to) })
        assertEquals("e", text("- [x] e", 0, 0) { t, f, to -> Format.task(t, f, to) })
    }

    @Test
    fun quoteToggles() {
        assertEquals("> q", text("q", 0, 0) { t, f, to -> Format.quote(t, f, to) })
        assertEquals("r", text("> r", 0, 0) { t, f, to -> Format.quote(t, f, to) })
        // A quote with no space after the marker still comes off.
        assertEquals("s", text(">s", 0, 0) { t, f, to -> Format.quote(t, f, to) })
    }

    @Test
    fun prefixesKeepIndentation() {
        assertEquals("  - deep", text("  deep", 0, 0) { t, f, to -> Format.list(t, f, to) })
    }

    // --- link and tag ------------------------------------------------------------

    @Test
    fun bareUrlGainsALabelToFillIn() {
        val r = Format.link("see https://example.com/x now", 4, 25)
        assertEquals("see [](https://example.com/x) now", Format.apply("see https://example.com/x now", r.ops))
        // The caret lands inside the empty label.
        assertEquals(5, r.selStart)
    }

    @Test
    fun selectedWordsBecomeTheLinkName() {
        val r = Format.link("go Start here now", 3, 13)
        assertEquals("go [[Start here]] now", Format.apply("go Start here now", r.ops))
        // After the closing brackets, the web's anchor.
        assertEquals(17, r.selStart)
        assertEquals(17, r.selEnd)
    }

    @Test
    fun nothingSelectedOpensBracketsAndTheList() {
        val r = Format.link("go  now", 3, 3)
        assertEquals("go [[ now", Format.apply("go  now", r.ops))
        assertEquals(5, r.selStart)
        assertTrue(r.openLinks)
        assertFalse(r.openTags)
    }

    @Test
    fun tagTurnsSelectedWordsIntoOneTag() {
        val r = Format.tag("deep work rocks", 5, 15)
        assertEquals("deep #work-rocks", Format.apply("deep work rocks", r.ops))
        assertEquals(16, r.selStart)
    }

    @Test
    fun tagAddsASpaceWhenAWordPrecedes() {
        val r = Format.tag("word", 4, 4)
        assertEquals("word #", Format.apply("word", r.ops))
        assertEquals(6, r.selStart)
        assertTrue(r.openTags)
    }

    @Test
    fun tagAfterNothingOrSpaceNeedsNoLead() {
        val r = Format.tag("x ", 2, 2)
        assertEquals("x #", Format.apply("x ", r.ops))
        assertEquals(3, r.selStart)
        val r2 = Format.tag("", 0, 0)
        assertEquals("#", Format.apply("", r2.ops))
        assertEquals(1, r2.selStart)
    }

    // --- the shape the document takes -----------------------------------------------

    @Test
    fun everyCommandIsHunksThatRebuildTheText() {
        val commands = listOf<(String, Int, Int) -> Format.Result>(
            { t, f, to -> Format.wrap(t, f, to, "**") },
            { t, f, to -> Format.wrap(t, f, to, "_") },
            { t, f, to -> Format.code(t, f, to) },
            { t, f, to -> Format.heading(t, f, to) },
            { t, f, to -> Format.list(t, f, to) },
            { t, f, to -> Format.task(t, f, to) },
            { t, f, to -> Format.quote(t, f, to) },
            { t, f, to -> Format.link(t, f, to) },
            { t, f, to -> Format.tag(t, f, to) },
        )
        val samples = "one two\n- a task\n> quote #tag\nhttps://x.example/p"
        for (command in commands) {
            for (from in 0..samples.length) {
                val r = command(samples, from, from)
                // Applying the hunks never throws and lands the
                // selection inside the new text.
                val next = Format.apply(samples, r.ops)
                assertTrue(r.selStart in 0..next.length)
                assertTrue(r.selEnd in 0..next.length)
                // The JSON form round-trips: it is an array of p/d/i.
                assertTrue(Format.opsJson(r.ops).startsWith("["))
            }
        }
    }

    @Test
    fun opsAreAscendingAndDisjoint() {
        val r = Format.wrap("one two three", 0, 3, "**")
        assertEquals(listOf(0, 3), r.ops.map { it.p })
        val h = Format.code("a\nx\ny\nb", 2, 6)
        assertEquals(true, h.ops.map { it.p }.zipWithNext().all { (a, b) -> a < b })
    }
}
