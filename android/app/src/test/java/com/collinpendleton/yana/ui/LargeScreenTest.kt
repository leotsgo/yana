package com.collinpendleton.yana.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The detail pane's saveable spec, the divider's bounds, and the shell's pane set. */
class LargeScreenTest {
    @Test fun specRoundTripsThroughItsSaveableForm() {
        val spec = NoteSpec("01ABCDEFINVALIDULIDISFINE", "A note", line = 42, edit = true)
        assertEquals(spec, decodeNoteSpec(encodeNoteSpec(spec)))
    }

    @Test fun specDefaultsRoundTrip() {
        val spec = NoteSpec("id", "")
        assertEquals(spec, decodeNoteSpec(encodeNoteSpec(spec)))
    }

    @Test fun decodingRejectsAnythingThatIsNotASpec() {
        assertNull(decodeNoteSpec(emptyList()))
        assertNull(decodeNoteSpec(listOf("id", "title")))
        assertNull(decodeNoteSpec(listOf("id", "title", "not-a-line", "true")))
        assertNull(decodeNoteSpec(listOf("id", "title", "3", "not-a-flag")))
    }

    @Test fun theDividerStaysWhereBothPanesCanLive() {
        assertEquals(0.3f, clampDivider(0.05f))
        assertEquals(0.5f, clampDivider(0.5f))
        assertEquals(0.7f, clampDivider(0.95f))
    }

    @Test fun panesRaiseAndLowerTheirHoldOnTheEditor() {
        ShellState.setEditing("detail", true)
        ShellState.setEditing("beside", true)
        assertTrue(ShellState.editingPanes.value.containsAll(setOf("detail", "beside")))
        ShellState.setEditing("beside", false)
        assertEquals(setOf("detail"), ShellState.editingPanes.value)
        ShellState.setEditing("detail", false)
        assertTrue(ShellState.editingPanes.value.isEmpty())
    }

    @Test fun aNoteSessionRemembersItWasBeingEdited() {
        assertFalse(NoteSessions.wasEditing("never-opened"))
        NoteSessions.state("some-note").editing = true
        assertTrue(NoteSessions.wasEditing("some-note"))
        NoteSessions.state("some-note").editing = false
        assertFalse(NoteSessions.wasEditing("some-note"))
    }
}
