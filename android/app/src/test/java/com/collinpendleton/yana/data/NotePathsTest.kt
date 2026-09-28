package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * The title edit's rules, the web's paths.ts: what a typed title names
 * and where it places the note, and which heading in a body is the
 * one a rename rewrites.
 */
class NotePathsTest {
    @Test
    fun aPlainTitleRenamesInPlace() {
        val r = resolveTitle("home/Plan.md", "Plan", "Roadmap")
        assertEquals("Roadmap", r.title)
        assertEquals("home", r.dir)
        assertEquals("home/Roadmap.md", r.path)
        assertEquals(false, r.moves)
    }

    @Test
    fun aSlashMovesTheNote() {
        val r = resolveTitle("home/Plan.md", "Plan", "projects/kiln")
        assertEquals("kiln", r.title)
        assertEquals("home/projects", r.dir)
        assertEquals("home/projects/kiln.md", r.path)
        assertEquals(true, r.moves)
    }

    @Test
    fun aTrailingSlashMovesWithoutRenaming() {
        val r = resolveTitle("home/Plan.md", "Plan", "work/")
        assertEquals("Plan", r.title)
        assertEquals("home/work", r.dir)
        assertEquals("home/work/Plan.md", r.path)
    }

    @Test
    fun aRootedPathStartsAtTheRoot() {
        val r = resolveTitle("home/Plan.md", "Plan", "/top/idea")
        assertEquals("idea", r.title)
        assertEquals("top", r.dir)
        assertEquals("top/idea.md", r.path)
    }

    @Test
    fun dotDotStepsUp() {
        val r = resolveTitle("home/deep/Plan.md", "Plan", "../up/two")
        assertEquals("two", r.title)
        assertEquals("home/up", r.dir)
        assertEquals("home/up/two.md", r.path)
    }

    @Test
    fun badCharactersDropFromTheName() {
        val r = resolveTitle("home/Plan.md", "Plan", "a:b*c?  name")
        // The heading keeps what was typed; only the file name drops the rest.
        assertEquals("a:b*c? name", r.title)
        assertEquals("home/abc name.md", r.path)
    }

    @Test
    fun aNoteLooseInTheRootSpace() {
        val r = resolveTitle("Plan.md", "Plan", "new folder/Name")
        assertEquals("Name", r.title)
        assertEquals("new folder", r.dir)
        assertEquals("new folder/Name.md", r.path)
    }

    @Test
    fun theHeadingIsTheFirstH1BeforeAnythingElse() {
        val h = findHeading("# Start here\n\nbody")!!
        assertEquals(2, h.first)
        assertEquals(10, h.second)
    }

    @Test
    fun theHeadingSitsPastFrontmatter() {
        val h = findHeading("---\nid: x\n---\n# Title\n\ntext")!!
        assertEquals(16, h.first)
        assertEquals(5, h.second)
    }

    @Test
    fun aHeadingAfterContentIsNotTheTitle() {
        assertNull(findHeading("intro text\n\n# Late"))
        assertNull(findHeading("plain body, no heading"))
    }

    @Test
    fun theHeadingTrimTrailingSpaces() {
        val h = findHeading("# Name   ")!!
        assertEquals(4, h.second)
    }
}
