package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertNotNull
import org.junit.Test

/** The new-note picker's path rules, ported from the web's newnote.tsx
 * with its cases. */
class NewNoteKitTest {
    private val spaces = listOf("personal", "work")
    private val dirs = listOf(
        "personal/Daily",
        "personal/Daily/Trips",
        "personal/recipes",
        "work/projects",
        "work/archive",
    )

    @Test
    fun `a bare name lands in the current space`() {
        val p = NewNoteKit.parsePath("plan", "personal", spaces, dirs)
        assertEquals("personal", p.dir)
        assertEquals("plan", p.name)
        assertNull(p.error)
    }

    @Test
    fun `a typed space is absolute`() {
        val p = NewNoteKit.parsePath("work/plan", "personal", spaces, dirs)
        assertEquals("work", p.dir)
        assertEquals("plan", p.name)
    }

    @Test
    fun `folders that are there keep their spelling`() {
        val p = NewNoteKit.parsePath("personal/daily/", "personal", spaces, dirs)
        assertEquals("personal/Daily", p.dir)
    }

    @Test
    fun `missing folders are named in order`() {
        val p = NewNoteKit.parsePath("work/new/deep/plan", "", spaces, dirs)
        assertEquals("work/new/deep", p.dir)
        assertEquals(listOf("work/new", "work/new/deep"), p.missing)
        assertNull(p.error)
    }

    @Test
    fun `a path with no space is an error`() {
        val p = NewNoteKit.parsePath("nowhere/plan", "", spaces, dirs)
        assertNotNull(p.error)
    }

    @Test
    fun `the empty path asks for a space`() {
        val p = NewNoteKit.parsePath("", "", spaces, dirs)
        assertEquals("A note lives inside a space. Start the path with one.", p.error)
    }

    @Test
    fun `the browse folder follows the typed folder exactly`() {
        val p = NewNoteKit.parsePath("personal/Daily/", "personal", spaces, dirs)
        assertEquals("personal/Daily", p.browse)
    }

    @Test
    fun `the browse folder matches fuzzily`() {
        // `pe/da` then the folder list sits on personal/Daily.
        val p = NewNoteKit.parsePath("pe/da/", "personal", spaces, dirs)
        assertEquals("personal/Daily", p.browse)
    }

    @Test
    fun `a dotted step up stops the browse`() {
        val p = NewNoteKit.parsePath("personal/Daily/../plan", "personal", spaces, dirs)
        assertNull(p.browse)
    }

    @Test
    fun `children of the root are the spaces`() {
        assertEquals(spaces, NewNoteKit.children("", spaces, dirs))
    }

    @Test
    fun `children of a folder are one level down`() {
        assertEquals(listOf("personal/Daily/Trips"), NewNoteKit.children("personal/Daily", spaces, dirs))
    }

    @Test
    fun `noteFile adds md unless the name carries one`() {
        assertEquals("plan.md", NewNoteKit.noteFile("plan"))
        assertEquals("page.html", NewNoteKit.noteFile("page.html"))
        assertEquals("aplan.md", NewNoteKit.noteFile("a:plan"))
    }

    private val notes = listOf(
        PickerNote("1", "personal/plan.md", "The plan"),
        PickerNote("2", "personal/plan 2.md", "Plan two"),
        PickerNote("3", "personal/Daily/plan.md", "A different plan"),
        PickerNote("4", "personal/plans.md", "Plans"),
    )

    @Test
    fun `existingNote finds the note with or without the extension`() {
        assertEquals("1", NewNoteKit.existingNote(notes, "personal", "plan")?.id)
        assertEquals("1", NewNoteKit.existingNote(notes, "personal", "plan.md")?.id)
        assertEquals("1", NewNoteKit.existingNote(notes, "personal", "PLAN")?.id)
    }

    @Test
    fun `existingNote stays in the folder asked for`() {
        // The Daily copy is not the folder's note.
        assertEquals("1", NewNoteKit.existingNote(notes, "personal", "plan")?.id)
        assertEquals("3", NewNoteKit.existingNote(notes, "personal/Daily", "plan")?.id)
        assertNull(NewNoteKit.existingNote(notes, "work", "plan"))
    }

    @Test
    fun `freeName walks to the next free number`() {
        assertEquals("idea.md", NewNoteKit.freeName(notes, "personal", "idea"))
        assertEquals("plan 3.md", NewNoteKit.freeName(notes, "personal", "plan"))
    }

    @Test
    fun `resolveDir keeps the base and steps up`() {
        assertEquals("a/b", NewNoteKit.resolveDir("a", "b"))
        assertEquals("x", NewNoteKit.resolveDir("a", "/x"))
        assertEquals("a", NewNoteKit.resolveDir("a/b", ".."))
        assertEquals("", NewNoteKit.resolveDir("a", "../.."))
    }

    @Test
    fun `fileName drops what a path cannot hold`() {
        assertEquals("whatever", NewNoteKit.fileName("what/ever?"))
        assertEquals("untitled", NewNoteKit.fileName("..."))
    }
}
