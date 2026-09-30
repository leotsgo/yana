package com.collinpendleton.yana.ui.screens

import java.time.Instant
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Both writer shapes name a copy with a timestamp; the banner and rows read it back. */
class ConflictFormatTest {
    @Test fun trashRestoreShapeCarriesATimestamp() {
        assertEquals(
            Instant.parse("2026-09-23T12:12:12Z"),
            conflictStamp("main/a.conflict-20260923T121212.md"),
        )
    }

    @Test fun htmlSaveShapeWithItsRetrySuffix() {
        assertEquals(
            Instant.parse("2026-09-23T12:12:12Z"),
            conflictStamp("main/dash.conflict-20260923-121212-2.html"),
        )
    }

    @Test fun plainNamesAndShortStampsCarryNone() {
        assertNull(conflictStamp("main/a.md"))
        assertNull(conflictStamp("main/a.conflict.md"))
        assertNull(conflictStamp("main/a.conflict-2026092-121212.md"))
        assertNull(conflictStamp("main/a.conflict-20260923-1212.md"))
    }

    @Test fun whenFallsBackToTheFileTime() {
        assertEquals("", conflictWhen("main/a.md", ""))
    }
}
