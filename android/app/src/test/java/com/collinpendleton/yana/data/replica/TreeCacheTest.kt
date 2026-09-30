package com.collinpendleton.yana.data.replica

import com.collinpendleton.yana.data.SpaceTree
import com.collinpendleton.yana.data.TreeNode
import org.junit.Assert.assertEquals
import org.junit.Test

/** A cached tree must round-trip to the nested shape the server sent. */
class TreeCacheTest {

    private fun tree(): List<SpaceTree> = listOf(
        SpaceTree(
            name = "garden",
            notes = 3,
            children = listOf(
                TreeNode(
                    type = "dir", name = "recipes", path = "garden/recipes",
                    children = listOf(
                        TreeNode(type = "note", name = "soup.md", path = "garden/recipes/soup.md", id = "a", title = "Soup", kind = "md", tags = listOf("winter")),
                    ),
                ),
                TreeNode(type = "note", name = "readme.md", path = "garden/readme.md", id = "b", title = "Readme", kind = "md", public = true),
            ),
        ),
        SpaceTree(
            name = "",
            notes = 1,
            children = listOf(
                TreeNode(type = "note", name = "loose.md", path = "loose.md", id = "c", title = "Loose", kind = "md", conflict = true),
            ),
        ),
    )

    @Test
    fun flattensAndNestsBack() {
        val rows = flattenTree(tree())
        assertEquals(4, rows.size)
        val nested = nestTree(rows.filter { it.space == "garden" })
        assertEquals(tree()[0].children, nested)
        assertEquals(tree()[1].children, nestTree(rows.filter { it.space == "" }))
    }

    @Test
    fun spacesKeepTheirOwnTopLevel() {
        val rows = flattenTree(tree())
        val garden = nestTree(rows.filter { it.space == "garden" })
        val root = nestTree(rows.filter { it.space == "" })
        assertEquals(listOf("garden/recipes", "garden/readme.md"), garden.map { it.path })
        assertEquals(listOf("loose.md"), root.map { it.path })
    }

    @Test
    fun serverOrderSurvives() {
        val rows = flattenTree(tree())
        val nested = nestTree(rows.filter { it.space == "garden" })
        assertEquals("garden/recipes", nested[0].path)
        assertEquals(listOf("garden/recipes/soup.md"), nested[0].children.map { it.path })
    }
}
