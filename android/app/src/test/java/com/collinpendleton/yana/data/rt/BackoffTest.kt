package com.collinpendleton.yana.data.rt

import kotlin.random.Random
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** The reconnect schedule: 500ms doubling to 8s, with jitter, as the web client computes it. */
class BackoffTest {
    @Test
    fun doublesAndCaps() {
        val fixed = Random(0) // deterministic; jitter still varies
        val base = backoffDelay(0, fixed)
        assertTrue("first retry is within the first window", base in 500..750)
        assertTrue(backoffDelay(1, fixed) >= 1_000)
        assertTrue(backoffDelay(2, fixed) >= 2_000)
        assertTrue(backoffDelay(4, fixed) >= 8_000)
        repeat(100) {
            val d = backoffDelay(20, Random(it), 500, 8_000, 250)
            assertTrue("capped at 8s plus jitter", d in 8_000..8_250)
        }
    }

    @Test
    fun jitterKeepsItWithinTheWindow() {
        repeat(200) {
            val d = backoffDelay(3, Random(it), 500, 8_000, 250)
            assertTrue(d in 4_000..4_250)
        }
    }

    @Test
    fun zeroJitterIsExact() {
        val none = Random(1)
        assertEquals(500L, backoffDelay(0, none, 500, 8_000, 0))
        assertEquals(1_000L, backoffDelay(1, none, 500, 8_000, 0))
        assertEquals(8_000L, backoffDelay(10, none, 500, 8_000, 0))
    }
}
