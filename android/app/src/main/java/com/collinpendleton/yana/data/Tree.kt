package com.collinpendleton.yana.data

/** One visible row of a folder tree: a node and how deep it sits. */
data class TreeRow(val node: TreeNode, val depth: Int, val expanded: Boolean)

/**
 * Flattens a tree into the rows on screen: every node at the top level,
 * and the children of each folder whose path is in [expanded]. Folders
 * come before notes, in the order the server sent them.
 */
fun visibleRows(nodes: List<TreeNode>, expanded: Set<String>, depth: Int = 0): List<TreeRow> {
    val out = ArrayList<TreeRow>()
    fun walk(list: List<TreeNode>, d: Int) {
        for (n in list) {
            // A conflict copy nests under its note; the shell lists survivors only.
            if (n.conflict) continue
            val open = n.isDir && n.path in expanded
            out += TreeRow(n, d, open)
            if (open) walk(n.children, d + 1)
        }
    }
    walk(nodes, depth)
    return out
}

/** How many notes sit anywhere under [node]. */
fun noteCount(node: TreeNode): Int =
    if (!node.isDir) 1 else node.children.sumOf { if (it.conflict) 0 else noteCount(it) }
