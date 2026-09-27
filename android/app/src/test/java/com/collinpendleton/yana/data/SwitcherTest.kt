package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** The switcher's row rules, ported from the web palette with its cases. */
class SwitcherTest {
    private fun note(
        id: String,
        path: String,
        title: String = path.substringAfterLast('/'),
        space: String = path.substringBefore('/').ifEmpty { "" },
        kind: String = "md",
        tags: List<String> = emptyList(),
    ) = SwitcherNote(id, space, if (space.isEmpty()) path else path.substringAfter('/'), title, kind, tags)

    private val notes = listOf(
        note("a", "journal/2026/week.md", title = "Week", tags = listOf("work", "planning")),
        note("b", "journal/2026/picture.md", title = "A picture in a note", tags = listOf("home")),
        note("c", "recipes/bread.md", title = "Bread", kind = "html", tags = listOf("baking")),
        note("d", "scratch.md", title = "Scratch"),
        note("e", "work/spec.md", title = "Spec", tags = listOf("work")),
    )

    @Test
    fun `an empty box lists recents first`() {
        val rows = Switcher.rows(notes, recents = setOf("d", "c"), query = "")
        // A stable sort: the recents keep the tree's order among
        // themselves, and so does everything after them.
        assertEquals(listOf("c", "d", "a", "b", "e"), rows.map { it.id })
    }

    @Test
    fun `the box caps at forty rows`() {
        val many = (0 until 60).map { note("$it", "n$it.md") }
        assertEquals(40, Switcher.rows(many, emptySet(), "").size)
    }

    @Test
    fun `free text fuzzy matches the better of title and detail`() {
        // "work" hits a's tag detail, not any title.
        val rows = Switcher.rows(notes, emptySet(), "work")
        assertEquals(setOf("a", "e"), rows.map { it.id }.toSet())
    }

    @Test
    fun `a hash word narrows to the notes carrying the tag`() {
        val rows = Switcher.rows(notes, emptySet(), "#work")
        assertEquals(setOf("a", "e"), rows.map { it.id }.toSet())
    }

    @Test
    fun `a negated hash word excludes the tag`() {
        val rows = Switcher.rows(notes, emptySet(), "-#work bread")
        assertEquals(listOf("c"), rows.map { it.id })
    }

    @Test
    fun `tag operator matches the hash form`() {
        assertEquals(
            Switcher.rows(notes, emptySet(), "#work").map { it.id },
            Switcher.rows(notes, emptySet(), "tag:work").map { it.id },
        )
    }

    @Test
    fun `path operator matches a folder prefix`() {
        val rows = Switcher.rows(notes, emptySet(), "path:journal")
        assertEquals(setOf("a", "b"), rows.map { it.id }.toSet())
    }

    @Test
    fun `space operator matches the first path segment`() {
        val rows = Switcher.rows(notes, emptySet(), "space:recipes")
        assertEquals(listOf("c"), rows.map { it.id })
    }

    @Test
    fun `is selectors judge the tree's facts`() {
        assertEquals(listOf("d"), Switcher.rows(notes, emptySet(), "is:untagged").map { it.id })
        assertEquals(listOf("c"), Switcher.rows(notes, emptySet(), "is:html").map { it.id })
    }

    @Test
    fun `operators filter and the free text still scores`() {
        val rows = Switcher.rows(notes, emptySet(), "#work week")
        assertEquals(listOf("a"), rows.map { it.id })
    }

    @Test
    fun `a query nothing carries answers empty`() {
        assertEquals(emptyList<SwitcherNote>(), Switcher.rows(notes, emptySet(), "zzz"))
    }

    @Test
    fun `the create row offers the free text unless a title is it`() {
        assertNull(Switcher.createFor(notes, ""))
        assertNull(Switcher.createFor(notes, "#work"))
        assertNull(Switcher.createFor(notes, "bread"))
        assertEquals("Fresh bread", Switcher.createFor(notes, "Fresh bread"))
        assertEquals("Fresh bread", Switcher.createFor(emptyList(), "#home Fresh bread"))
    }
}
