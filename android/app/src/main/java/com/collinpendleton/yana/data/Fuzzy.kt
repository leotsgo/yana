package com.collinpendleton.yana.data

/** One fuzzy match: how well it scored and where each query character landed. */
data class FuzzyMatch(val score: Double, val positions: List<Int>)

/**
 * Subsequence matching for the quick switcher, a line-for-line port of
 * the web's fuzzy.ts: every query character must appear in order; runs
 * of consecutive matches and matches at word or path boundaries score
 * higher. Case-insensitive.
 */
fun fuzzy(query: String, text: String): FuzzyMatch? {
    val q = query.lowercase()
    val t = text.lowercase()
    if (q.isEmpty()) return FuzzyMatch(0.0, emptyList())
    // The boundary look-ahead can skip past the only run that works
    // ("picture" against "put a picture in a note" jumps to the i of
    // "in"), so a plain left-to-right pass is the fallback.
    return scan(q, t, lookAhead = true) ?: scan(q, t, lookAhead = false)
}

private fun scan(q: String, t: String, lookAhead: Boolean): FuzzyMatch? {
    val positions = ArrayList<Int>(q.length)
    var score = 0.0
    var ti = 0
    var prev = -2
    for (qi in 0 until q.length) {
        val ch = q[qi]
        val idx = t.indexOf(ch, ti)
        if (idx < 0) return null
        // Prefer a later occurrence that sits on a boundary when the next
        // one is mid-word; a cheap look-ahead keeps "jou" matching journal/
        // over j-o-u scattered across a long path.
        var at = idx
        if (lookAhead && !isBoundary(t, idx)) {
            val next = t.indexOf(ch, idx + 1)
            if (next >= 0 && isBoundary(t, next) && next - idx < 24) at = next
        }
        positions.add(at)
        score += 1.0
        if (at == prev + 1) score += 3.0
        if (isBoundary(t, at)) score += 2.0
        prev = at
        ti = at + 1
    }
    // Shorter targets win ties; early matches win too.
    score -= (positions.firstOrNull() ?: 0) * 0.05 + t.length * 0.01
    return FuzzyMatch(score, positions)
}

private fun isBoundary(t: String, i: Int): Boolean {
    if (i == 0) return true
    val p = t[i - 1]
    return p == '/' || p == ' ' || p == '-' || p == '_' || p == '.'
}
