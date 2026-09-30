package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Test

class TreeTest {
    private fun note(path: String, conflict: Boolean = false) =
        TreeNode(type = "note", name = path.substringAfterLast('/'), path = path, id = path, title = path, conflict = conflict)

    private val tree = listOf(
        TreeNode(
            type = "dir", name = "a", path = "s/a",
            children = listOf(
                TreeNode(type = "dir", name = "b", path = "s/a/b", children = listOf(note("s/a/b/1.md"))),
                note("s/a/2.md"),
                note("s/a/2 (conflict).md", conflict = true),
            ),
        ),
        note("s/3.md"),
    )

    @Test fun collapsedShowsTopLevelOnly() =
        assertEquals(listOf("s/a", "s/3.md"), visibleRows(tree, emptySet()).map { it.node.path })

    @Test fun expandingNestsWithDepth() {
        val rows = visibleRows(tree, setOf("s/a", "s/a/b"))
        assertEquals(listOf("s/a", "s/a/b", "s/a/b/1.md", "s/a/2.md", "s/3.md"), rows.map { it.node.path })
        assertEquals(listOf(0, 1, 2, 1, 0), rows.map { it.depth })
    }

    @Test fun collapsedParentHidesExpandedChild() =
        assertEquals(listOf("s/a", "s/3.md"), visibleRows(tree, setOf("s/a/b")).map { it.node.path })

    @Test fun countSkipsConflictCopies() = assertEquals(2, noteCount(tree[0]))
}
