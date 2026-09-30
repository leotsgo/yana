package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The offline resolver mirrors internal/wikilink: the same order (the
 * note's directory, the space root, a unique filename), the same
 * boundaries (a link never crosses a space, an ambiguous filename never
 * resolves). The cases are the Go suite's, so the phone's offline
 * resolution cannot drift from the server's.
 */
class WikiLinksTest {
    private fun mainNotes() = listOf(
        NoteRef("home", "main/home.md"),
        NoteRef("target", "main/target.md"),
        NoteRef("nested", "main/guides/nested.md"),
        NoteRef("deep", "main/guides/deep/x.md"),
        NoteRef("page", "main/page.html"),
        NoteRef("twinA", "main/a/twin.md"),
        NoteRef("twinB", "main/b/twin.md"),
    )

    @Test fun resolveOrder() {
        val r = WikiResolver("main", mainNotes())
        val cases = listOf(
            "sibling" to ("target" to "main/home.md" to "target"),
            "sibling with extension" to ("target.md" to "main/home.md" to "target"),
            "sibling dir" to ("guides/nested" to "main/home.md" to "nested"),
            "up and over" to ("../../target" to "main/guides/deep/x.md" to "target"),
            "up one" to ("../nested" to "main/guides/deep/x.md" to "nested"),
            "html sibling" to ("page.html" to "main/home.md" to "page"),
            "root path" to ("target" to "main/guides/nested.md" to "target"),
            "root path with extension" to ("target.md" to "main/guides/nested.md" to "target"),
            "root html" to ("page.html" to "main/guides/nested.md" to "page"),
            "filename unique" to ("nested" to "main/a/twin.md" to "nested"),
        )
        for ((name, c) in cases) {
            val got = r.resolve(c.first.first, c.first.second)
            assertTrue("$name: expected resolved, got $got", got.resolved)
            assertEquals("$name: wrong note", c.second, got.toId)
        }
    }

    @Test fun unresolvedCases() {
        val r = WikiResolver("main", mainNotes())
        for ((name, raw, from) in listOf(
            Triple("filename ambiguous", "twin", "main/home.md"),
            Triple("cross space", "other/target", "main/home.md"),
            Triple("escape the space", "../../../etc/keys", "main/guides/deep/x.md"),
            Triple("missing", "nowhere", "main/home.md"),
            Triple("empty", "  ", "main/home.md"),
            Triple("brackets", "a[b", "main/home.md"),
        )) {
            val got = r.resolve(raw, from)
            assertFalse("$name: should not resolve", got.resolved)
            assertNull("$name: no id expected", got.toId)
        }
    }

    @Test fun looseNotes() {
        val r = WikiResolver(
            "",
            listOf(NoteRef("loose", "loose.md"), NoteRef("journal", "journal/today.md")),
        )
        val got = r.resolve("loose", "journal/today.md")
        assertTrue(got.resolved)
        assertEquals("loose", got.toId)
        // A path that escapes the root is not a note.
        assertFalse(r.resolve("../escape", "loose.md").resolved)
    }

    @Test fun createPathFallsBackToTheSpaceRoot() {
        // The web's createPathFor: relative inside the space, the space
        // root prepended when the joined path escapes it (the raw target
        // keeps its ..s; the server cleans and may refuse it, as it does
        // for the web's own affordance).
        assertEquals(
            "main/guides/new note.md",
            WikiResolver.createPathFor(base = "main/guides", space = "main", raw = "new note"),
        )
        assertEquals(
            "main/../../new note.md",
            WikiResolver.createPathFor(base = "main/guides", space = "main", raw = "../../new note"),
        )
        assertEquals(
            "notes/todo.md",
            WikiResolver.createPathFor(base = "notes/sub", space = "", raw = "../todo.md"),
        )
        assertEquals(
            "main/a/keep.md",
            WikiResolver.createPathFor(base = "main/a", space = "main", raw = "keep.md"),
        )
    }

    @Test fun joinMatchesGoPathJoin() {
        assertEquals("a/b", WikiResolver.joinPath("a", "b"))
        assertEquals("b", WikiResolver.joinPath("a", "../b"))
        assertEquals("../b", WikiResolver.joinPath("", "../b"))
        assertEquals("a/c", WikiResolver.joinPath("a", "./c"))
    }
}
