package com.collinpendleton.yana.data.search

import java.time.LocalDate
import java.time.ZoneOffset

/**
 * The search box grammar, a line-for-line port of the server's parser
 * (internal/search/query.go) so the same box contents mean the same
 * thing online and offline: whitespace-separated terms; a term is an
 * operator (name:value, the value optionally quoted), a bare #tag, a
 * quoted phrase, or plain text; a leading - negates. A name that is not
 * a known operator, an is:/has: selector that does not exist, or a date
 * that is not YYYY-MM-DD demotes the term to plain text — anything the
 * grammar does not recognise is searched, never rejected.
 */

/** Operator names. */
object Ops {
    const val TAG = "tag"
    const val PATH = "path"
    const val SPACE = "space"
    const val IS = "is"
    const val HAS = "has"
    const val AUTHOR = "author"
    const val BEFORE = "before"
    const val AFTER = "after"
}

private val isValues = setOf("untagged", "task", "html")
private val hasValues = setOf("image", "attachment")

/** One token of a query: plain text, or an operator and its argument. */
data class Term(
    /** The term exactly as typed, negation mark included. */
    val raw: String,
    /** The term excludes what it matches (-tag:x). */
    val negated: Boolean,
    /** The operator name, or "" for plain text. */
    val op: String = "",
    /** The operator's argument. */
    val value: String = "",
    /** [value] parsed, for before:/after: only: epoch nanoseconds. */
    val date: Long = 0,
    /** The text term was written in double quotes (an exact phrase). */
    val quoted: Boolean = false,
    /** The searchable text of a plain term ("" for operators). */
    val text: String = "",
)

/** A parsed search query. */
data class SearchQuery(val terms: List<Term>) {
    /**
     * The plain text terms, positive and negated apart. Words too short
     * for the trigram index ride along; the caller decides how to run
     * them.
     */
    fun textTerms(): Pair<List<Term>, List<Term>> {
        val positive = terms.filter { it.op == "" && it.text.isNotEmpty() && !it.negated }
        val negative = terms.filter { it.op == "" && it.text.isNotEmpty() && it.negated }
        return positive to negative
    }
}

/** Splits a query into terms, exactly as the server does. */
fun parseQuery(q: String): SearchQuery {
    val terms = ArrayList<Term>()
    val rs = q.trim().toCodePoints()
    var i = 0
    while (i < rs.size) {
        while (i < rs.size && isSpace(rs[i])) i++
        if (i >= rs.size) break
        val start = i
        var negated = false
        if (rs[i] == '-'.code && i + 1 < rs.size && !isSpace(rs[i + 1])) {
            negated = true
            i++
        }
        if (i >= rs.size) break
        when {
            rs[i] == '"'.code -> {
                val (content, end) = quotedRun(rs, i)
                terms += Term(raw = substring(rs, start, end), negated = negated, quoted = true, text = content)
                i = end
            }
            rs[i] == '#'.code && i + 1 < rs.size && !isSpace(rs[i + 1]) -> {
                val j = wordEnd(rs, i + 1)
                terms += Term(
                    raw = substring(rs, start, j),
                    negated = negated,
                    op = Ops.TAG,
                    value = substring(rs, i + 1, j).lowercase(),
                )
                i = j
            }
            else -> {
                val (name, opLen) = opName(rs, i)
                if (opLen > 0) {
                    val valStart = i + opLen
                    if (valStart < rs.size && rs[valStart] == '"'.code) {
                        val (v, end) = quotedRun(rs, valStart)
                        terms += operatorTerm(rs, start, valStart, end, negated, name, v, quoted = true)
                        i = end
                    } else {
                        val j = wordEnd(rs, valStart)
                        terms += operatorTerm(rs, start, valStart, j, negated, name, substring(rs, valStart, j), quoted = false)
                        i = j
                    }
                } else {
                    val j = wordEnd(rs, i)
                    terms += Term(raw = substring(rs, start, j), negated = negated, text = substring(rs, i, j))
                    i = j
                }
            }
        }
    }
    return SearchQuery(terms)
}

/**
 * Builds an operator term, or a plain-text term when the operator or its
 * value does not hold up — the server's demotion rules.
 */
private fun operatorTerm(
    rs: IntArray,
    start: Int,
    valStart: Int,
    rawEnd: Int,
    negated: Boolean,
    name: String,
    value: String,
    quoted: Boolean,
): Term {
    fun demote() = Term(
        raw = substring(rs, start, rawEnd),
        negated = negated,
        text = substring(rs, start + if (negated) 1 else 0, rawEnd),
    )
    return when (name) {
        Ops.IS -> if (!isValues.contains(value.lowercase())) {
            demote()
        } else {
            Term(raw = substring(rs, start, rawEnd), negated = negated, op = name, value = value.lowercase(), quoted = quoted)
        }
        Ops.HAS -> if (!hasValues.contains(value.lowercase())) {
            demote()
        } else {
            Term(raw = substring(rs, start, rawEnd), negated = negated, op = name, value = value.lowercase(), quoted = quoted)
        }
        Ops.BEFORE, Ops.AFTER -> {
            val day = runCatching { LocalDate.parse(value) }.getOrNull()
            if (day == null) demote()
            else Term(raw = substring(rs, start, rawEnd), negated = negated, op = name, value = value, date = day.atStartOfDay(ZoneOffset.UTC).toEpochSecond() * 1_000_000_000L, quoted = quoted)
        }
        Ops.TAG -> if (value.isEmpty()) {
            demote()
        } else {
            Term(raw = substring(rs, start, rawEnd), negated = negated, op = name, value = value.lowercase(), quoted = quoted)
        }
        Ops.PATH, Ops.SPACE, Ops.AUTHOR -> if (value.isEmpty()) {
            demote()
        } else {
            Term(raw = substring(rs, start, rawEnd), negated = negated, op = name, value = value, quoted = quoted)
        }
        else -> demote()
    }
}

/** Reads the quoted run starting at the quote: content, and index past the close. */
private fun quotedRun(rs: IntArray, open: Int): Pair<String, Int> {
    var i = open + 1
    while (i < rs.size) {
        if (rs[i] == '"'.code) return substring(rs, open + 1, i) to i + 1
        i++
    }
    return substring(rs, open + 1, rs.size) to rs.size
}

/** The index of the first space at or after i. */
private fun wordEnd(rs: IntArray, i: Int): Int {
    var j = i
    while (j < rs.size && !isSpace(rs[j])) j++
    return j
}

/** A known lowercase operator name followed by a colon, or "" with 0. */
private fun opName(rs: IntArray, i: Int): Pair<String, Int> {
    var j = i
    while (j < rs.size && rs[j] in 'a'.code..'z'.code) j++
    if (j == i || j >= rs.size || rs[j] != ':'.code) return "" to 0
    return when (val name = substring(rs, i, j)) {
        Ops.TAG, Ops.PATH, Ops.SPACE, Ops.IS, Ops.HAS, Ops.AUTHOR, Ops.BEFORE, Ops.AFTER -> name to j - i + 1
        else -> "" to 0
    }
}

private fun isSpace(r: Int): Boolean =
    r == ' '.code || r == '\t'.code || r == '\n'.code || r == '\r'.code

/** The string's code points, the unit the server's rune-based parser works in. */
private fun String.toCodePoints(): IntArray {
    val out = IntArray(length)
    var n = 0
    var i = 0
    while (i < length) {
        val cp = codePointAt(i)
        out[n++] = cp
        i += Character.charCount(cp)
    }
    return out.copyOf(n)
}

/** The substring between two code point indices. */
private fun substring(rs: IntArray, from: Int, to: Int): String {
    val sb = StringBuilder()
    for (k in from until to) sb.appendCodePoint(rs[k])
    return sb.toString()
}
