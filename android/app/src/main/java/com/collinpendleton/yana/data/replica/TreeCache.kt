package com.collinpendleton.yana.data.replica

import com.collinpendleton.yana.data.SpaceTree
import com.collinpendleton.yana.data.TreeNode

/**
 * The folder tree's cached form: the server's nested response flattened
 * into one row per node (parent paths carry the nesting, positions carry
 * the server's ordering) and nested back on the way out, so what the
 * screens render offline is what the server sent online. The order and
 * conflict_of fields the server also sends only ever fed that ordering,
 * which is already baked into what arrives.
 */

/** Flattens a tree response into cacheable rows. */
internal fun flattenTree(tree: List<SpaceTree>): List<TreeNodeEntity> {
    val out = ArrayList<TreeNodeEntity>()
    fun walk(space: String, parent: String, nodes: List<TreeNode>) {
        for (n in nodes) {
            out += TreeNodeEntity(
                path = n.path,
                space = space,
                parent = parent,
                type = n.type,
                name = n.name,
                id = n.id,
                title = n.title,
                kind = n.kind,
                tags = n.tags.joinToString(","),
                public = n.public,
                conflict = n.conflict,
                position = out.size,
            )
            walk(space, n.path, n.children)
        }
    }
    for (s in tree) walk(s.name, "", s.children)
    return out
}

/** Nests one space's cached rows back into the tree the screens render. */
internal fun nestTree(rows: List<TreeNodeEntity>): List<TreeNode> {
    val byParent = rows.groupBy { it.parent }
    fun nodeOf(e: TreeNodeEntity): TreeNode = TreeNode(
        type = e.type,
        name = e.name,
        path = e.path,
        id = e.id,
        title = e.title,
        kind = e.kind,
        tags = e.tags.split(',').filter { it.isNotEmpty() },
        public = e.public,
        conflict = e.conflict,
        children = (byParent[e.path] ?: emptyList()).sortedBy { it.position }.map { nodeOf(it) },
    )
    return (byParent[""] ?: emptyList()).sortedBy { it.position }.map { nodeOf(it) }
}
