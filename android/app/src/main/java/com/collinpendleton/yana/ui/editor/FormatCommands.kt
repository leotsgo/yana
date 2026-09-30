package com.collinpendleton.yana.ui.editor

import kotlinx.serialization.json.add
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/**
 * The formatting commands, a port of the web's format.tsx: each button
 * is the same small edit over the same selection, so both clients
 * write the same markdown. Every command returns its change as
 * replacement hunks — measured against the text as it stands, in
 * ascending position order — plus the selection the web leaves; the
 * editor commits the hunks as one multi-region transaction, so a
 * button is one CRDT operation and one undo step.
 */
object Format {
    /** One replacement: [d] UTF-16 units at [p] become [i]. */
    data class Op(val p: Int, val d: Int, val i: String)

    /** A command's outcome: hunks to commit and where the caret lands. */
    data class Result(
        val ops: List<Op>,
        val selStart: Int,
        val selEnd: Int,
        /** The link button with nothing selected opened brackets: show the notes. */
        val openLinks: Boolean = false,
        /** The tag button with nothing selected wrote the hash: show the tags. */
        val openTags: Boolean = false,
    )

    /** The ops as the bind package's EditMany JSON. */
    fun opsJson(ops: List<Op>): String = buildJsonArray {
        for (op in ops) {
            add(
                buildJsonObject {
                    put("p", op.p)
                    put("d", op.d)
                    put("i", op.i)
                },
            )
        }
    }.toString()

    /** The text the hunks produce, for the field's next value. */
    fun apply(text: String, ops: List<Op>): String {
        val b = StringBuilder(text)
        for (op in ops.sortedByDescending { it.p }) {
            if (op.d == 0 && op.i.isEmpty()) continue
            b.replace(op.p, op.p + op.d, op.i)
        }
        return b.toString()
    }

    /** The line [at] sits on starts here (the offset of its first character). */
    private fun lineStartAt(text: String, at: Int): Int {
        val nl = text.lastIndexOf('\n', at - 1)
        return if (nl < 0) 0 else nl + 1
    }

    /**
     * Wraps the selection in marks; an empty selection gets the marks
     * with the caret between them. Selecting text that is already
     * wrapped unwraps it.
     */
    fun wrap(text: String, from: Int, to: Int, marks: String): Result {
        val n = marks.length
        val before = text.substring(maxOf(0, from - n), from)
        val after = text.substring(to, minOf(text.length, to + n))
        return if (before == marks && after == marks) {
            Result(
                listOf(Op(from - n, n, ""), Op(to, n, "")),
                from - n,
                to - n,
            )
        } else {
            Result(
                listOf(Op(from, 0, marks), Op(to, 0, marks)),
                from + n,
                to + n,
            )
        }
    }

    /** Code: a multi-line selection is fenced as a block, a line or less is wrapped in backticks. */
    fun code(text: String, from: Int, to: Int): Result {
        val selected = text.substring(from, to)
        if (selected.contains('\n')) {
            val start = lineStartAt(text, from)
            val end = text.indexOf('\n', to).let { if (it < 0) text.length else it }
            return Result(listOf(Op(start, 0, "```\n"), Op(end, 0, "\n```")), start + 3, start + 3)
        }
        return wrap(text, from, to, "`")
    }

    /** Rewrites the start of every line the selection touches; [next] returns what replaces each line's marker. */
    private fun prefixLines(
        text: String,
        from: Int,
        to: Int,
        next: (body: String) -> Pair<Int, String>,
    ): Result {
        val ops = mutableListOf<Op>()
        var lineStart = lineStartAt(text, from)
        while (lineStart <= text.length) {
            val nl = text.indexOf('\n', lineStart)
            val lineEnd = if (nl < 0) text.length else nl
            val line = text.substring(lineStart, lineEnd)
            val indent = line.indexOfFirst { !it.isWhitespace() }.let { if (it < 0) line.length else it }
            val (strip, add) = next(line.substring(indent))
            if (strip > 0 || add.isNotEmpty()) {
                ops.add(Op(lineStart + indent, strip, add))
            }
            if (lineEnd >= to || nl < 0) break
            lineStart = nl + 1
        }
        // The selection maps through the hunks the way the document's
        // other edits map a cursor — the convention the web's editor
        // follows leaving the caret to CodeMirror's change mapping.
        val hunks = ops.map { SelectionMapper.Hunk(it.p, it.d, it.i.length) }
        val sel = SelectionMapper.mapSelection(hunks, from, to, apply(text, ops).length)
        return Result(ops, sel.first, sel.second)
    }

    private val bulletRe = Regex("^([-*+]|\\d+[.)])\\s+")
    private val taskRe = Regex("^([-*+]|\\d+[.)])\\s+\\[[ xX]\\]\\s*")
    private val headingRe = Regex("^(#{1,6})\\s+")
    private val quoteRe = Regex("^>\\s?")

    /** Heading: none becomes one, three or more come off, otherwise it deepens a level. */
    fun heading(text: String, from: Int, to: Int): Result = prefixLines(text, from, to) { body ->
        val m = headingRe.find(body)
        when {
            m == null -> 0 to "# "
            m.groupValues[1].length >= 3 -> m.value.length to ""
            else -> m.value.length to ("#".repeat(m.groupValues[1].length + 1) + " ")
        }
    }

    /** List: a task line becomes a plain item, a plain item comes off, anything else becomes an item. */
    fun list(text: String, from: Int, to: Int): Result = prefixLines(text, from, to) { body ->
        when {
            taskRe.find(body) != null -> taskRe.find(body)!!.value.length to "- "
            bulletRe.find(body) != null -> bulletRe.find(body)!!.value.length to ""
            else -> 0 to "- "
        }
    }

    /** Task: a task line comes off, a list line gains its box, anything else becomes an open task. */
    fun task(text: String, from: Int, to: Int): Result = prefixLines(text, from, to) { body ->
        val t = taskRe.find(body)
        if (t != null) return@prefixLines t.value.length to ""
        val b = bulletRe.find(body)
        if (b != null) b.value.length to "${b.groupValues[1]} [ ] " else 0 to "- [ ] "
    }

    /** Quote: the marker toggles. */
    fun quote(text: String, from: Int, to: Int): Result = prefixLines(text, from, to) { body ->
        val q = quoteRe.find(body)
        if (q != null) q.value.length to "" else 0 to "> "
    }

    /**
     * Link: a bare URL gains a label to fill in, selected words become
     * the name of the note to link to, and nothing selected opens the
     * brackets and offers the notes to pick from.
     */
    fun link(text: String, from: Int, to: Int): Result {
        val selected = text.substring(from, to)
        if (Regex("^https?://\\S+$").matches(selected)) {
            return Result(listOf(Op(from, to - from, "[]($selected)")), from + 1, from + 1)
        }
        if (selected.isNotEmpty()) {
            return Result(
                listOf(Op(from, to - from, "[[${selected}]]")),
                from + selected.length + 4,
                from + selected.length + 4,
            )
        }
        return Result(listOf(Op(from, 0, "[[")), from + 2, from + 2, openLinks = true)
    }

    /** Tag: selected words become the tag, otherwise the hash is written and the tags are offered. */
    fun tag(text: String, from: Int, to: Int): Result {
        val selected = text.substring(from, to)
        val prev = if (from > 0) text[from - 1].toString() else ""
        val lead = if (prev.isEmpty() || prev.isBlank()) "" else " "
        if (selected.isNotEmpty()) {
            val t = selected.trim().replace(Regex("\\s+"), "-")
            val ins = "$lead#$t"
            val anchor = from + lead.length + t.length + 1
            return Result(listOf(Op(from, to - from, ins)), anchor, anchor)
        }
        return Result(listOf(Op(from, 0, "$lead#")), from + lead.length + 1, from + lead.length + 1, openTags = true)
    }
}

/**
 * The completion triggers, ports of the web editor's sources: `[[`
 * up to the caret, with no closing bracket or bar since, is a wikilink
 * being typed; `#` at the start of a word is a tag. Each names the
 * range the typed text occupies, so a pick replaces exactly it.
 */
object Completion {
    /** A trigger under the caret: [start] is where the typed text begins. */
    data class Query(val start: Int, val typed: String)

    private val linkTail = Regex("\\[\\[([^\\[\\]|]*)$")
    private val tagTail = Regex("(^|\\s)#([\\w-]*)$")

    /** The line [at] sits on starts here (the offset of its first character). */
    private fun lineStartAt(text: String, at: Int): Int {
        val nl = text.lastIndexOf('\n', at - 1)
        return if (nl < 0) 0 else nl + 1
    }

    /** The wikilink being typed at [caret], or null when the caret is not inside one. */
    fun linkAt(text: String, caret: Int): Query? {
        val before = text.substring(lineStartAt(text, caret), caret)
        val m = linkTail.find(before) ?: return null
        val typed = m.groupValues[1]
        return Query(caret - typed.length, typed)
    }

    /** The tag being typed at [caret], or null when the word before it did not start with a hash. */
    fun tagAt(text: String, caret: Int): Query? {
        val before = text.substring(lineStartAt(text, caret), caret)
        val m = tagTail.find(before) ?: return null
        val typed = m.groupValues[2]
        return Query(caret - typed.length, typed)
    }

    /**
     * Picks a note for a link query the way the web's completion
     * applies one: the typed text becomes the target, the brackets
     * close unless they already had, and the caret lands after the
     * name — inside, so `|` can start an alias.
     */
    fun applyLink(text: String, q: Query, caret: Int, target: String): Format.Result {
        val closed = text.startsWith("]]", caret)
        val insert = if (closed) target else "$target]]"
        val anchor = if (closed) q.start + target.length else q.start + target.length + 2
        return Format.Result(listOf(Format.Op(q.start, caret - q.start, insert)), anchor, anchor)
    }

    /** Picks a tag: the typed word becomes the whole tag. */
    fun applyTag(q: Query, caret: Int, tag: String): Format.Result {
        val anchor = q.start + tag.length
        return Format.Result(listOf(Format.Op(q.start, caret - q.start, tag)), anchor, anchor)
    }
}
