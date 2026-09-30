package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The completion sources: a note links by its file name, twins in one
 * space by their path within it, and the tags are every tag in use —
 * the web client's rules over the same pool.
 */
class LinkTargetsTest {
    private fun meta(id: String, space: String, path: String, title: String, tags: List<String> = emptyList()) =
        NoteMeta(id = id, space = space, path = path, title = title, tags = tags)

    @Test
    fun notesLinkByFileName() {
        val (bySpace, _) = LinkTargets.build(
            listOf(
                meta("1", "home", "Projects/Kiln.md", "Kiln"),
                meta("2", "home", "Start here.md", "Start here"),
            ),
        )
        val targets = bySpace["home"]!!.associateBy { it.id }
        assertEquals("Kiln", targets["1"]?.target)
        assertEquals("Start here", targets["2"]?.target)
    }

    @Test
    fun twinsLinkByTheirPath() {
        val (bySpace, _) = LinkTargets.build(
            listOf(
                meta("1", "home", "work/Plan.md", "Plan"),
                meta("2", "home", "life/Plan.md", "Plan"),
                meta("3", "home", "other/Plan.md", "Plan"),
            ),
        )
        val targets = bySpace["home"]!!.associateBy { it.id }
        assertEquals("work/Plan", targets["1"]?.target)
        assertEquals("life/Plan", targets["2"]?.target)
        assertEquals("other/Plan", targets["3"]?.target)
    }

    @Test
    fun sameNameInDifferentSpacesIsNotATwin() {
        val (bySpace, _) = LinkTargets.build(
            listOf(
                meta("1", "home", "Plan.md", "Plan"),
                meta("2", "work", "Plan.md", "Plan"),
            ),
        )
        assertEquals("Plan", bySpace["home"]!![0].target)
        assertEquals("Plan", bySpace["work"]!![0].target)
    }

    @Test
    fun tagsCollectAcrossNotesSortedCaseInsensitively() {
        val (_, tags) = LinkTargets.build(
            listOf(
                meta("1", "home", "a.md", "A", tags = listOf("Zebra", "apple")),
                meta("2", "home", "b.md", "B", tags = listOf("mango", "Apple")),
            ),
        )
        assertEquals(listOf("apple", "Apple", "mango", "Zebra"), tags)
    }
}
