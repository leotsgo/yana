package com.collinpendleton.yana.data

/**
 * Paths typed by a person, resolved against where a note already sits,
 * a port of the web's paths.ts: a title of `projects/kiln` moves the
 * note into projects/ and calls it kiln; a trailing slash moves the
 * note without renaming it; the characters a file name cannot hold
 * are dropped.
 */

/** A file name for a title: the characters a path cannot hold are dropped. */
fun fileNameFor(title: String): String {
    var name = title.replace(Regex("[\\\\/:*?\"<>|\\x00-\\x1f]"), "").replace(Regex("\\s+"), " ").trim()
    name = name.replace(Regex("^\\.+"), "")
    if (name.length > 120) name = name.substring(0, 120)
    return name.ifEmpty { "untitled" }
}

/** A folder name typed by a person; empty when nothing usable is left. */
fun dirNameFor(seg: String): String {
    var name = seg.replace(Regex("[\\\\/:*?\"<>|\\x00-\\x1f]"), "").replace(Regex("\\s+"), " ").trim()
    if (name.length > 120) name = name.substring(0, 120)
    return if (Regex("^\\.+$").matches(name)) "" else name
}

/** A folder path typed against a base folder: `a/b` sits inside base, `/a/b` starts at the root, `..` steps up. */
fun resolveDir(base: String, typed: String): String {
    val t = typed.trim()
    val parts = if (t.startsWith("/") || base.isEmpty()) mutableListOf() else base.split('/').toMutableList()
    for (raw in t.split('/')) {
        val seg = raw.trim()
        when {
            seg.isEmpty() || seg == "." -> {}
            seg == ".." -> if (parts.isNotEmpty()) parts.removeAt(parts.size - 1)
            else -> dirNameFor(seg).takeIf { it.isNotEmpty() }?.let(parts::add)
        }
    }
    return parts.joinToString("/")
}

/** What a title typed into the header means for the note. */
data class TitlePath(
    /** The heading the note gets; the current one when the typed text ended in a slash. */
    val title: String,
    /** The folder the note lands in. */
    val dir: String,
    /** The full path of the file after the change. */
    val path: String,
    /** True when the file changes folder. */
    val moves: Boolean,
)

/** The last segment of [raw] names the note; the segments before it place it. */
fun resolveTitle(notePath: String, currentTitle: String, raw: String): TitlePath {
    val base = notePath.substringAfterLast('/')
    val dot = base.lastIndexOf('.')
    val ext = if (dot > 0) base.substring(dot) else ""
    val cur = notePath.substringBeforeLast('/', "")
    val text = raw.replace(Regex("\\s+"), " ").trim()
    val cut = text.lastIndexOf('/')
    var dir = cur
    var title = text
    if (cut >= 0) {
        dir = resolveDir(cur, text.substring(0, cut + 1))
        title = text.substring(cut + 1).trim()
        if (title.isEmpty()) title = currentTitle
    }
    val name = fileNameFor(title) + ext
    val path = if (dir.isEmpty()) name else "$dir/$name"
    return TitlePath(title, dir, path, dir != cur)
}

/**
 * The first H1 in a body, past any frontmatter block, when nothing
 * but whitespace stands before it — the heading a rename rewrites, a
 * port of the web's findHeading. The body is the document's text; the
 * frontmatter is not part of the CRDT, so what arrives here has
 * already had it taken off and the offset scan is a formality that
 * keeps parity.
 */
fun findHeading(body: String): Pair<Int, Int>? {
    var offset = 0
    if (body.startsWith("---\n")) {
        val end = body.indexOf("\n---", 4)
        if (end >= 0) {
            val nl = body.indexOf('\n', end + 1)
            offset = if (nl < 0) body.length else nl + 1
        }
    }
    val rest = body.substring(offset)
    val m = Regex("^(\\s*)#[ \\t]+([^\\n]*?)[ \\t]*$", RegexOption.MULTILINE).find(rest) ?: return null
    // Only a heading before any other content counts as the title.
    if (rest.substring(0, m.range.first).trim().isNotEmpty()) return null
    val hashes = Regex("^(\\s*)#[ \\t]+").find(m.value) ?: return null
    return (offset + m.range.first + hashes.value.length) to m.groupValues[2].length
}
