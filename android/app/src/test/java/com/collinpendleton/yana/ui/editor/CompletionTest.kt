package com.collinpendleton.yana.ui.editor

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The completion triggers and picks, the web editor's sources: what
 * opens the list, what closes it, and the exact text a pick writes —
 * the caret left inside the brackets so `|` can start an alias.
 */
class CompletionTest {
    @Test
    fun typingBracketsOpensTheQuery() {
        val q = Completion.linkAt("See [[", 6)
        assertEquals(6, q?.start)
        assertEquals("", q?.typed)
    }

    @Test
    fun typedTextIsTheQuery() {
        val q = Completion.linkAt("See [[sta", 9)
        assertEquals(6, q?.start)
        assertEquals("sta", q?.typed)
    }

    @Test
    fun bracketsOnlyOnTheCaretLine() {
        val q = Completion.linkAt("[[one\nplain", 11)
        assertNull(q)
    }

    @Test
    fun aBarOrBracketClosesTheQuery() {
        // An alias is being typed; the offer is done.
        assertNull(Completion.linkAt("See [[Start here|", 17))
        // The link closed; nothing to finish.
        assertNull(Completion.linkAt("See [[Start here]]", 18))
        assertNull(Completion.linkAt("See [[Start here]]", 17))
    }

    @Test
    fun aQuerySurvivesSpaces() {
        val q = Completion.linkAt("[[start here", 12)
        assertEquals("start here", q?.typed)
    }

    @Test
    fun theHashOpensATagQuery() {
        val q = Completion.tagAt("a #ho", 5)
        assertEquals(3, q?.start)
        assertEquals("ho", q?.typed)
    }

    @Test
    fun theHashNeedsAWordStart() {
        assertNull(Completion.tagAt("abc#no", 6))
        assertEquals("", Completion.tagAt("#", 1)?.typed)
        assertNull(Completion.tagAt("a #x!", 5))
    }

    @Test
    fun pickingALinkClosesTheBracketsWithTheCaretAfterThem() {
        val text = "See [[sta"
        val q = Completion.linkAt(text, 9)!!
        val r = Completion.applyLink(text, q, 9, "Start here")
        assertEquals("See [[Start here]]", Format.apply(text, r.ops))
        // After the finished link, the web's anchor.
        assertEquals(18, r.selStart)
        assertEquals(18, r.selEnd)
    }

    @Test
    fun pickingIntoBracketsThatAlreadyClosedDoesNotDoubleThem() {
        val text = "See [[sta]]"
        // The caret still inside the typed text: "sta" at 6..9.
        val q = Completion.Query(6, "sta")
        val r = Completion.applyLink(text, q, 9, "Start here")
        assertEquals("See [[Start here]]", Format.apply(text, r.ops))
        assertEquals(16, r.selStart)
    }

    @Test
    fun pickingALinkIsOneHunkThatReplacesTheTypedText() {
        val text = "See [[sta"
        val q = Completion.linkAt(text, 9)!!
        val r = Completion.applyLink(text, q, 9, "Start here")
        assertEquals(1, r.ops.size)
        assertEquals(6, r.ops[0].p)
        assertEquals(3, r.ops[0].d)
    }

    @Test
    fun pickingATagReplacesTheTypedWord() {
        val text = "a #ho"
        val q = Completion.tagAt(text, 5)!!
        val r = Completion.applyTag(q, 5, "home")
        assertEquals("a #home", Format.apply(text, r.ops))
        assertEquals(7, r.selStart)
    }
}
