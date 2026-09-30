package com.collinpendleton.yana.data

/**
 * What `[[` and `#` can complete to, a port of the web client's
 * completions builder: a note links by its file name, and twins in one
 * space (two `Plan.md` in different folders) link by their path within
 * the space; the tags are every tag in use, sorted. The paths the
 * replica carries are space-relative (the space travels in its own
 * field), so the stems come straight off them.
 */
data class LinkTarget(val id: String, val target: String, val title: String, val path: String)

object LinkTargets {
    private val noteExt = Regex("\\.(md|markdown|html?|htm)$", RegexOption.IGNORE_CASE)

    /** The name without its note extension. */
    fun stem(name: String): String = noteExt.replace(name, "")

    /**
     * Builds the per-space link targets and the sorted tag list from
     * the replica's note pool.
     */
    fun build(notes: List<NoteMeta>): Pair<Map<String, List<LinkTarget>>, List<String>> {
        val counts = HashMap<String, Int>()
        val tags = mutableSetOf<String>()
        for (n in notes) {
            val key = n.space + " " + stem(n.path.substringAfterLast('/')).lowercase()
            counts[key] = (counts[key] ?: 0) + 1
            tags.addAll(n.tags)
        }
        val bySpace = LinkedHashMap<String, MutableList<LinkTarget>>()
        for (n in notes) {
            val twins = (counts[n.space + " " + stem(n.path.substringAfterLast('/')).lowercase()] ?: 0) > 1
            val target = if (twins) stem(n.path) else stem(n.path.substringAfterLast('/'))
            bySpace.getOrPut(n.space) { mutableListOf() }.add(LinkTarget(n.id, target, n.title, n.path))
        }
        return bySpace to tags.toList().sortedBy { it.lowercase() }
    }
}
