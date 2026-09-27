package com.collinpendleton.yana.data

import com.collinpendleton.yana.data.search.Ops
import com.collinpendleton.yana.data.search.Term
import com.collinpendleton.yana.data.search.parseQuery

/**
 * One note as the switcher lists it: enough to match, to show, and to
 * open. [path] is the note's path within its space; [full] is the path
 * as the tree shows it, the space prefixed, and it is what the path and
 * space operators match against — the web's paths carry the space too.
 */
data class SwitcherNote(
    val id: String,
    val space: String,
    val path: String,
    val title: String,
    val kind: String,
    val tags: List<String>,
) {
    val full: String get() = if (space.isEmpty()) path else "$space/$path"

    /** The row's first line: the title. */
    val label: String get() = title

    /** The row's second line: the path, then the tags as they are written. */
    val detail: String get() =
        if (tags.isEmpty()) full else "$full  ${tags.joinToString(" ") { "#$it" }}"
}

/**
 * The switcher's rows, a port of the web palette's rules: an empty box
 * lists recents first and then the tree's order; operators in the query
 * (tag:, path:, space:, is:untagged, is:html, a bare #word among them)
 * decide which rows survive; the remaining free text fuzzy-matches
 * against the title and the detail, the better of the two scores, and
 * sorts the survivors.
 */
object Switcher {
    const val LIMIT = 40

    fun rows(notes: List<SwitcherNote>, recents: Set<String>, query: String): List<SwitcherNote> {
        val q = query.trim()
        if (q.isEmpty()) {
            // A stable sort, so recents keep the tree's order among
            // themselves and so does everything after them.
            return notes.sortedBy { if (it.id in recents) 0 else 1 }.take(LIMIT)
        }
        val terms = parseQuery(q).terms
        val pool = if (terms.any { it.op != "" }) notes.filter { noteMatches(terms, it) } else notes
        val text = freeText(terms)
        if (text.isEmpty()) return pool.take(LIMIT)
        val scored = pool.mapNotNull { n ->
            val score = maxOf(
                fuzzy(text, n.label)?.score ?: Double.NEGATIVE_INFINITY,
                fuzzy(text, n.detail)?.score ?: Double.NEGATIVE_INFINITY,
            )
            if (score == Double.NEGATIVE_INFINITY) null else n to score
        }
        return scored.sortedByDescending { it.second }.take(LIMIT).map { it.first }
    }

    /**
     * The name the create row offers, or null when there is nothing to
     * create: no free text, or a row whose title already is the text.
     */
    fun createFor(rows: List<SwitcherNote>, query: String): String? {
        val text = freeText(parseQuery(query.trim()).terms)
        if (text.isEmpty()) return null
        val taken = rows.any { it.label.lowercase() == text.lowercase() }
        return if (taken) null else text
    }

    private fun freeText(terms: List<Term>): String =
        terms.filter { it.op == "" }.joinToString(" ") { it.text }.trim()
}

/**
 * Whether a note satisfies the terms the tree can evaluate — tag, path,
 * space, is:untagged, is:html — a port of the web's noteMatches. The
 * other operators need the index, so the switcher leaves them to the
 * search box.
 */
fun noteMatches(terms: List<Term>, note: SwitcherNote): Boolean {
    val lower = note.full.lowercase()
    for (t in terms) {
        if (t.op == "") continue
        val hit = when (t.op) {
            Ops.TAG -> t.value in note.tags
            Ops.PATH -> {
                val v = t.value.trim('/')
                v.isNotEmpty() && (lower == v || lower.startsWith("$v/") || lower.startsWith("$v."))
            }
            Ops.SPACE -> note.full.substringBefore('/').lowercase() == t.value.lowercase()
            Ops.IS -> when (t.value) {
                "untagged" -> note.tags.isEmpty()
                "html" -> note.kind == "html"
                else -> continue
            }
            else -> continue
        }
        if (hit == t.negated) return false
    }
    return true
}
