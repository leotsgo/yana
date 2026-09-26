package com.collinpendleton.yana.data

import java.util.Random
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** The pure half of capture, held to the web's semantics. */
class CaptureKitTest {
    // --- ULID ------------------------------------------------------------------

    @Test
    fun `a new ULID has the server's shape`() {
        for (i in 1..100) {
            val id = CaptureKit.newUlid(1_700_000_000_000L)
            assertEquals(26, id.length)
            assertTrue(id.all { it in '0'..'9' || it in 'A'..'Z' })
            // No I, L, O, or U: Crockford's base 32.
            assertTrue(id.none { it in "ILOU" })
        }
    }

    @Test
    fun `two ULIDs differ`() {
        assertNotEquals(CaptureKit.newUlid(), CaptureKit.newUlid())
    }

    @Test
    fun `the time prefix orders ULIDs`() {
        val early = CaptureKit.newUlid(1_700_000_000_000L)
        val late = CaptureKit.newUlid(1_700_000_001_000L)
        assertTrue(early < late)
    }

    @Test
    fun `the same millisecond still varies`() {
        val a = CaptureKit.newUlid(1_700_000_000_000L, Random(7))
        val b = CaptureKit.newUlid(1_700_000_000_000L, Random(8))
        assertEquals(a.substring(0, 10), b.substring(0, 10))
        assertNotEquals(a, b)
    }
    // --- the share block ---------------------------------------------------------

    @Test
    fun `a URL becomes a link line`() {
        assertEquals(
            "- [A page](https://example.com/a)",
            CaptureKit.shareBlock("A page", "", "https://example.com/a"),
        )
    }

    @Test
    fun `text with a URL keeps the text under the link`() {
        assertEquals(
            "- [A page](https://example.com/a)\n  first line\n  second line",
            CaptureKit.shareBlock("A page", "first line\nsecond line", "https://example.com/a"),
        )
    }

    @Test
    fun `the first text line stands in for a missing title`() {
        assertEquals(
            "- [first](https://example.com/a)\n  second",
            CaptureKit.shareBlock("", "first\nsecond", "https://example.com/a"),
        )
    }

    @Test
    fun `plain text becomes dashed lines`() {
        assertEquals(
            "- one\n- two",
            CaptureKit.shareBlock("", "one\ntwo", ""),
        )
    }

    @Test
    fun `a title with text joins them`() {
        assertEquals(
            "- A page — the words",
            CaptureKit.shareBlock("A page", "the words", ""),
        )
    }

    @Test
    fun `star list text indents under the dash`() {
        // [*-+] is a range (* to +) in the web's regex too, so a dash
        // line does not read as a list; a star line does. Parity.
        assertEquals(
            "- lead\n  * already a list",
            CaptureKit.shareBlock("", "lead\n* already a list", ""),
        )
    }

    @Test
    fun `brackets in a label escape`() {
        assertEquals(
            "- [a \\[b\\] c](https://example.com/a)",
            CaptureKit.shareBlock("a [b] c", "", "https://example.com/a"),
        )
    }

    // --- capture and append ------------------------------------------------------

    @Test
    fun `a captured line is one dashed line`() {
        assertEquals("- buy milk", CaptureKit.captureBlock("buy milk"))
        assertEquals("- one\n- two", CaptureKit.captureBlock("one\ntwo"))
    }

    @Test
    fun `a timestamped line carries the time`() {
        val cal = java.util.Calendar.getInstance()
        cal.set(2026, java.util.Calendar.SEPTEMBER, 26, 14, 32, 0)
        assertEquals("- 14:32 the words", CaptureKit.timestampedLine("the words", cal.timeInMillis))
    }

    @Test
    fun `a timestamped line stamps a dash line like any other`() {
        // The web's [*-+] class is a range (* to +), so a dash line
        // does not read as a list there; parity keeps that.
        val line = CaptureKit.timestampedLine("- a list", 0)
        assertTrue(line.startsWith("- "))
        assertTrue(line.endsWith("- a list"))
    }

    @Test
    fun `appendBlock joins with a newline when the text lacks one`() {
        assertEquals("one\n- two", CaptureKit.appendBlock("one", "- two"))
    }

    @Test
    fun `appendBlock does not double the newline`() {
        assertEquals("one\n- two", CaptureKit.appendBlock("one\n", "- two"))
    }

    @Test
    fun `appendBlock fills an empty note with the block alone`() {
        assertEquals("- first", CaptureKit.appendBlock("", "- first"))
    }

    // --- the daily path ------------------------------------------------------------

    @Test
    fun `the pattern expands to today's path`() {
        assertEquals(
            "main/journal/2026/09/2026-09-26.md",
            CaptureKit.dailyPath("main", "journal/{YYYY}/{MM}/{YYYY}-{MM}-{DD}.md", "2026-09-26"),
        )
    }

    @Test
    fun `the date token expands too`() {
        assertEquals(
            "main/day/2026-09-26.md",
            CaptureKit.dailyPath("main", "day/{date}.md", "2026-09-26"),
        )
    }

    @Test
    fun `an empty space leaves the root path`() {
        assertEquals(
            "journal/2026/09/2026-09-26.md",
            CaptureKit.dailyPath("", "journal/{YYYY}/{MM}/{YYYY}-{MM}-{DD}.md", "2026-09-26"),
        )
    }

    @Test
    fun `today is the local date`() {
        val before = java.util.TimeZone.getDefault()
        java.util.TimeZone.setDefault(java.util.TimeZone.getTimeZone("UTC"))
        try {
            assertEquals("2026-09-26", CaptureKit.today(1_790_380_800_000L)) // 2026-09-26T00:00:00Z
        } finally {
            java.util.TimeZone.setDefault(before)
        }
    }

    // --- the inbox name --------------------------------------------------------------

    @Test
    fun `the first inbox name is Untitled`() {
        assertEquals("Untitled", CaptureKit.nextInboxName(emptyList()))
    }

    @Test
    fun `a taken name walks the numbers`() {
        assertEquals("Untitled 2", CaptureKit.nextInboxName(listOf("untitled.md")))
        assertEquals("Untitled 3", CaptureKit.nextInboxName(listOf("Untitled.md", "Untitled 2.md")))
    }

    @Test
    fun `the walk is case blind`() {
        assertEquals("Untitled 2", CaptureKit.nextInboxName(listOf("UNTITLED.md")))
    }
}
