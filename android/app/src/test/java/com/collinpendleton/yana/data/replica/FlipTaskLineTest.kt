package com.collinpendleton.yana.data.replica

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** The offline tick's line flip, the server's flipTaskLine in Kotlin. */
class FlipTaskLineTest {
    @Test fun ticksTheBoxOnTheLine() {
        val body = "# Heading\n\n- [ ] first\n- [x] done\n"
        assertEquals(
            "# Heading\n\n- [x] first\n- [x] done\n",
            flipTaskLine(body, 2, done = true),
        )
    }

    @Test fun unticks() {
        assertEquals("- [ ] done", flipTaskLine("- [x] done", 0, done = false))
    }

    @Test fun capitalXCountsAsTicked() {
        // Already ticked: nothing to change.
        assertNull(flipTaskLine("- [X] done", 0, done = true))
        assertEquals("- [ ] done", flipTaskLine("- [X] done", 0, done = false))
    }

    @Test fun alreadyInTheAskedStateChangesNothing() {
        assertNull(flipTaskLine("- [ ] open", 0, done = false))
        assertNull(flipTaskLine("- [x] done", 0, done = true))
    }

    @Test fun aLineWithoutABoxIsNotATask() {
        assertNull(flipTaskLine("plain text", 0, done = true))
    }

    @Test fun theServerFlipsAnyBoxOnTheLine() {
        // The server's tick does not require the box to be a rendered
        // task line; any [ ] or [x] on the asked line flips. Parity.
        assertEquals("- [x]first", flipTaskLine("- [ ]first", 0, done = true))
    }

    @Test fun linePastTheEndIsNull() {
        assertNull(flipTaskLine("one line", 5, done = true))
        assertNull(flipTaskLine("one line", -1, done = true))
    }

    @Test fun otherLinesAreUntouched() {
        val body = "a\n- [ ] b\nc"
        assertEquals("a\n- [x] b\nc", flipTaskLine(body, 1, done = true))
    }
}
