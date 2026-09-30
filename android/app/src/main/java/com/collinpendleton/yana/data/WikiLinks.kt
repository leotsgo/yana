package com.collinpendleton.yana.data

/**
 * Wikilink resolution on the phone, for notes read offline or edited
 * since the payload's link rows were indexed. The rules are
 * internal/wikilink's, one space at a time: exact relative path from the
 * linking note, exact path from the space root, then a unique filename
 * match anywhere in the space. The create path an unresolved link offers
 * is panels.ts's createPathFor: the raw target joined onto the linking
 * note's directory, falling back to the space root when that escapes the
 * space.
 */

/** One note a target can resolve to: id plus path relative to the notes root. */
data class NoteRef(val id: String, val relPath: String)

/** One resolved [[wikilink]]: the raw target as written and where it goes. */
data class ResolvedLink(val raw: String, val toId: String?, val resolved: Boolean)

class WikiResolver(private val space: String, refs: List<NoteRef>) {
    private val byPath = HashMap<String, String>(refs.size)
    private val byBase = HashMap<String, MutableList<String>>()

    init {
        for (ref in refs) {
            byPath[ref.relPath] = ref.id
            byBase.getOrPut(ref.relPath.substringAfterLast('/')) { mutableListOf() }.add(ref.id)
        }
    }

    /** Follows the resolution order for [raw] written in the note at [fromRel]. */
    fun resolve(raw: String, fromRel: String): ResolvedLink {
        val trimmed = raw.trim()
        if (trimmed.isEmpty() || trimmed.any { it == '[' || it == ']' || it == '\n' || it == '\r' }) {
            return ResolvedLink(raw, null, false)
        }
        // 1. Exact relative path from the linking note's directory.
        pathWithin(trimmed, dirOf(fromRel))?.let { p ->
            byPath[p]?.let { return ResolvedLink(raw, it, true) }
        }
        // 2. Exact path from the space root.
        pathWithin(trimmed, space)?.let { p ->
            byPath[p]?.let { return ResolvedLink(raw, it, true) }
        }
        // 3. Unique filename match anywhere in the space. Only a bare
        // filename falls through: a target that spells out a path was a
        // path reference and must not surprise-resolve by basename.
        if (!trimmed.contains('/')) {
            val cands = byBase[withExtension(trimmed)]
            if (cands?.size == 1) return ResolvedLink(raw, cands[0], true)
        }
        return ResolvedLink(raw, null, false)
    }

    /**
     * Joins [base] (a directory relative to the notes root) and [raw],
     * infers .md when raw carries no note extension, and reports the
     * cleaned path when it stays inside the resolver's space.
     */
    private fun pathWithin(raw: String, base: String): String? {
        val p = withExtension(joinPath(base, raw))
        if (space.isEmpty()) {
            // Notes loose in the root: their space is the root itself.
            return if (!p.startsWith("../") && p != ".." && !p.startsWith("/")) p else null
        }
        if (!p.startsWith("$space/")) return null
        return p
    }

    companion object {
        /** The create affordance's path: where a dashed link's note is made. */
        fun createPathFor(base: String, space: String, raw: String): String {
            var rel = raw
            if (!Regex("\\.(md|markdown|html|htm)$", RegexOption.IGNORE_CASE).containsMatchIn(rel)) rel += ".md"
            if (space.isNotEmpty()) {
                val joined = joinPath(base, rel)
                if (joined.startsWith("$space/")) return joined
                return "$space/$rel"
            }
            return joinPath(base, rel)
        }

        /** api.ts's join: segments folded onto the base, .. popping one. */
        fun joinPath(base: String, rel: String): String {
            val parts = if (base.isEmpty()) mutableListOf() else base.split('/').toMutableList()
            for (seg in rel.split('/')) {
                when {
                    seg.isEmpty() || seg == "." -> {}
                    seg == ".." -> {
                        if (parts.isNotEmpty()) parts.removeAt(parts.size - 1) else parts.add(seg)
                    }
                    else -> parts.add(seg)
                }
            }
            return parts.joinToString("/")
        }

        private fun dirOf(path: String): String {
            val i = path.lastIndexOf('/')
            return if (i < 0) "" else path.substring(0, i)
        }

        /** Appends .md when the target carries no note extension. */
        private fun withExtension(p: String): String {
            val ext = p.substringAfterLast('.', "").lowercase()
            return when (ext) {
                "md", "markdown", "html", "htm" -> p
                else -> "$p.md"
            }
        }
    }
}
