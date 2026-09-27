package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The switcher's matcher, ported from web/src/fuzzy.ts with the cases
 * its comments name: the look-ahead fallback, the boundary preference,
 * the 24-char window, and the tie-breaks.
 */
class FuzzyTest {
    /** The match that must exist; a null fails the test with the NPE. */
    private fun match(q: String, t: String): FuzzyMatch = fuzzy(q, t)!!

    @Test
    fun `empty query scores zero`() {
        val m = fuzzy("", "anything")
        assertEquals(0.0, m!!.score, 0.0)
        assertTrue(m.positions.isEmpty())
    }

    @Test
    fun `a missing character fails the match`() {
        assertNull(fuzzy("z", "ab"))
        assertNull(fuzzy("abc", "a-c"))
    }

    @Test
    fun `matching is case-insensitive`() {
        val lower = fuzzy("abc", "abc")!!
        val upper = fuzzy("ABC", "aBc")!!
        assertEquals(lower.positions, upper.positions)
        assertEquals(lower.score, upper.score, 1e-9)
    }

    @Test
    fun `the look-ahead skips past the only run that works`() {
        // The look-ahead takes the i of "in" over the i of "picture" and
        // then finds no c; the plain pass is the fallback that matches.
        val m = match("picture", "put a picture in a note")
        assertEquals(listOf(0, 7, 8, 9, 10, 11, 12), m.positions)
    }

    @Test
    fun `a boundary occurrence wins when it is close`() {
        // The j of "ajoyful" is mid-word and the look-ahead moves it to
        // the journal/ run, where jou then lands consecutively.
        val m = match("jou", "ajoyful journal/x")
        assertEquals(listOf(8, 9, 10), m.positions)
    }

    @Test
    fun `the look-ahead window is 24 characters`() {
        // The boundary o five past the first one is taken; the same o 33
        // past is too far, so the plain occurrence stands.
        assertEquals(listOf(6), match("o", "aob c-o").positions)
        assertEquals(listOf(1), match("o", "ao" + "x".repeat(30) + " o").positions)
    }

    @Test
    fun `consecutive runs and boundaries score higher`() {
        val run = match("ab", "ab")
        val scattered = match("ab", "axb")
        assertTrue(run.score > scattered.score)
        val boundary = match("b", "a b")
        val midWord = match("b", "ab")
        assertTrue(boundary.score > midWord.score)
    }

    @Test
    fun `shorter targets win ties`() {
        val short = match("ab", "ab")
        val long = match("ab", "ab" + " ".repeat(20))
        assertTrue(short.score > long.score)
    }

    @Test
    fun `earlier matches win ties`() {
        val early = match("a", "ax")
        val late = match("a", "xa")
        assertTrue(early.score > late.score)
    }

    @Test
    fun `the tie-break is position and length`() {
        // a at index 0 of a 2-char string: 1 + 2 boundary - 0*0.05 - 2*0.01.
        assertEquals(1 + 2 - 0.02, match("a", "ax").score, 1e-9)
    }
}
