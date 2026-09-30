package com.collinpendleton.yana.data

/**
 * One note as the new-note picker and the move picker judge it: where
 * it lives and what it is called. [path] is the full path, the space
 * included — the shape the wire, the tree, and the web's own picker
 * all use.
 */
data class PickerNote(
    val id: String,
    val path: String,
    val title: String,
)

/** What a typed path means: where the note lands, its name, the
 * folders that will be made, what is wrong, and which folder the
 * list shows. */
data class ParsedPath(
    val dir: String,
    val name: String,
    val missing: List<String>,
    val error: String?,
    /** The folder the list shows, found fuzzily; null when nothing fits. */
    val browse: String?,
)

/**
 * The pure half of organising from a phone: paths typed by a person,
 * resolved against where notes already sit, a line-for-line port of
 * the web's newnote.tsx and paths.ts so both clients judge a path the
 * same way. Free of Android types; the JVM suite holds it to the
 * web's cases.
 */
object NewNoteKit {
    private val NOTE_EXT = Regex("\\.(md|markdown|html?)$", RegexOption.IGNORE_CASE)
    private val BAD_CHARS = Regex("[\\\\/:*?\"<>|\\x00-\\x1f]")

    /** The folder a path sits in: everything before the last slash. */
    fun dirOf(path: String): String = path.substringBeforeLast('/', "")

    /** The last segment of a path: the file, or the folder's own name. */
    fun baseOf(path: String): String = path.substringAfterLast('/')

    /** A file name for a title: the characters a path cannot hold are dropped. */
    fun fileName(title: String): String =
        title.replace(BAD_CHARS, "")
            .replace(Regex("\\s+"), " ")
            .trim()
            .replace(Regex("^\\.+"), "")
            .take(120)
            .ifEmpty { "untitled" }

    /** A folder name typed by a person; empty when nothing usable is left. */
    fun dirName(seg: String): String =
        seg.replace(BAD_CHARS, "")
            .replace(Regex("\\s+"), " ")
            .trim()
            .replace(Regex("^\\.+$"), "")
            .take(120)

    /**
     * A folder path typed against a base folder: `a/b` sits inside
     * base, `/a/b` starts at the root, `..` steps up. Segments that
     * clean to nothing are dropped.
     */
    fun resolveDir(base: String, typed: String): String {
        val t = typed.trim()
        val parts: MutableList<String> =
            if (t.startsWith("/") || base.isEmpty()) mutableListOf() else base.split('/').toMutableList()
        for (raw in t.split('/')) {
            val seg = raw.trim()
            if (seg.isEmpty() || seg == ".") continue
            if (seg == "..") {
                if (parts.isNotEmpty()) parts.removeAt(parts.size - 1)
                continue
            }
            val name = dirName(seg)
            if (name.isNotEmpty()) parts.add(name)
        }
        return parts.joinToString("/")
    }

    /** The file a name makes: `.md` unless it already names a note file. */
    fun noteFile(name: String): String {
        val clean = fileName(name)
        return if (NOTE_EXT.containsMatchIn(clean)) clean else "$clean.md"
    }

    /**
     * The note a folder and a name point at, with or without the
     * extension, in any case.
     */
    fun existingNote(notes: List<PickerNote>, dir: String, name: String): PickerNote? {
        if (name.isEmpty()) return null
        val want = fileName(name).lowercase()
        val bare = NOTE_EXT.replace(want, "")
        val prefix = if (dir.isEmpty()) "" else dir.lowercase() + "/"
        for (n in notes) {
            val p = n.path.lowercase()
            if (!p.startsWith(prefix) || p.substring(prefix.length).contains('/')) continue
            val base = p.substring(prefix.length)
            if (base == want || NOTE_EXT.replace(base, "") == bare) return n
        }
        return null
    }

    /** The next free name in a folder: `name 2`, `name 3`, and so on. */
    fun freeName(notes: List<PickerNote>, dir: String, name: String): String {
        val file = noteFile(name)
        val ext = file.substring(file.lastIndexOf('.'))
        val stem = file.substring(0, file.length - ext.length)
        val taken = notes.filter { dirOf(it.path) == dir }
            .map { NOTE_EXT.replace(baseOf(it.path).lowercase(), "") }
            .toSet()
        if (!taken.contains(stem.lowercase())) return stem + ext
        var i = 2
        while (taken.contains("${stem} $i".lowercase())) i++
        return "$stem $i$ext"
    }

    /**
     * What the typed path means. Typed from the root when it starts
     * with a slash or a space's name; otherwise inside [space], as
     * the Move picker does. Folders that are there keep their
     * spelling: `Personal/daily` lands in personal/Daily rather than
     * making a second one.
     */
    fun parsePath(text: String, space: String, spaces: List<String>, dirs: List<String>): ParsedPath {
        val cut = text.lastIndexOf('/')
        val folderPart = if (cut >= 0) text.substring(0, cut + 1) else ""
        val name = (if (cut >= 0) text.substring(cut + 1) else text).replace(Regex("\\s+"), " ").trim()
        val byLower = dirs.associateBy { it.lowercase() }
        val spaceByLower = spaces.associateBy { it.lowercase() }
        val trimmed = folderPart.trim()
        val first = trimmed.replace(Regex("^/+"), "").split('/').firstOrNull() ?: ""
        val abs = trimmed.startsWith("/") || spaceByLower.containsKey(first.lowercase()) || space.isEmpty()
        val raw = resolveDir(if (abs) "" else space, if (abs) trimmed.replace(Regex("^/+"), "") else trimmed)
        var dir = ""
        for (seg in if (raw.isEmpty()) emptyList() else raw.split('/')) {
            val next = if (dir.isEmpty()) seg else "$dir/$seg"
            dir = byLower[next.lowercase()] ?: next
        }
        val missing = ArrayList<String>()
        val parts = if (dir.isEmpty()) emptyList() else dir.split('/')
        for (i in 2..parts.size) {
            val p = parts.subList(0, i).joinToString("/")
            if (!byLower.containsKey(p.lowercase())) missing.add(p)
        }
        var error: String? = null
        val top = parts.firstOrNull() ?: ""
        if (dir.isEmpty()) {
            error = "A note lives inside a space. Start the path with one."
        } else if (!spaceByLower.containsKey(top.lowercase())) {
            error = "$top is not a space. A note lives inside one."
        }
        return ParsedPath(dir, name, missing, error, browseDir(folderPart, space, spaces, dirs, dir))
    }

    /**
     * The folder to list under what is typed: the typed folder when it
     * is there, else each segment matched fuzzily against the folders
     * one level down, from the root and then from the current space.
     */
    private fun browseDir(folderPart: String, space: String, spaces: List<String>, dirs: List<String>, literal: String): String? {
        val t = folderPart.trim()
        if (t.isEmpty() || t == "/") return ""
        if (dirs.any { it == literal }) return literal
        val segs = t.split('/').map { it.trim() }.filter { it.isNotEmpty() && it != "." }
        if (segs.contains("..")) return null
        fun walk(from: String): String? {
            var cur = from
            for (seg in segs) {
                val kids = children(cur, spaces, dirs)
                val exact = kids.firstOrNull { baseOf(it).lowercase() == seg.lowercase() }
                if (exact != null) {
                    cur = exact
                    continue
                }
                var best: String? = null
                var score = Double.NEGATIVE_INFINITY
                for (k in kids) {
                    val m = fuzzy(seg, baseOf(k))
                    if (m != null && m.score > score) {
                        best = k
                        score = m.score
                    }
                }
                if (best == null) return null
                cur = best
            }
            return cur
        }
        return if (t.startsWith("/") || space.isEmpty()) walk("") else walk("") ?: walk(space)
    }

    /** The folders one level under a folder; the spaces under the root. */
    fun children(dir: String, spaces: List<String>, dirs: List<String>): List<String> =
        if (dir.isEmpty()) spaces.filter { it.isNotEmpty() }
        else dirs.filter { dirOf(it) == dir && it != dir }
}
