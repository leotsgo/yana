package com.collinpendleton.yana.data.search

/**
 * The SQL half of offline search: a line-for-line port of the server's
 * operator search (internal/index/opsearch.go) against the replica's
 * tables. Filters narrow the full-text match the same way, the modes
 * fall back the same way (trigram terms to FTS, shorter words to a
 * title LIKE, no text to newest-first), and the ranking and snippet
 * expressions are the server's own, so the same query over the same
 * corpus returns the same ordered results.
 */

/** A built query: the SQL, its arguments in order. */
data class BuiltQuery(val sql: String, val args: List<Any>)

/**
 * The query uses data the replica does not hold (author names, task
 * state, attachment text): only the server can answer it.
 */
class ServerOnlyFilter(ops: List<String>) :
    Exception("The ${ops.joinToString(", ")} filter${if (ops.size == 1) "" else "s"} need${if (ops.size == 1) "s" else ""} the server.")

private const val NOTE_COLUMNS =
    "n.id AS id, n.space AS space, n.rel_path AS rel_path, n.title AS title, n.preview AS preview, n.kind AS kind, n.created AS created, n.updated_at AS updated_at"

/** Builds the search SQL for [q], scoped to [space] when given. */
fun buildSearchQuery(q: SearchQuery, space: String?, limit: Int = 50): BuiltQuery {
    val lim = if (limit <= 0 || limit > 200) 50 else limit
    val filters = ArrayList<Pair<String, List<Any>>>()
    for (t in q.terms) {
        if (t.op == "") continue
        val f = opFilter(t) ?: continue
        filters += if (t.negated) "NOT (${f.first})" to f.second else f
    }
    val (positive, negative) = q.textTerms()

    var mode = "filter"
    val usable = ArrayList<Term>()
    for (t in positive) {
        if (runeCount(t.text) >= 3) {
            usable += t
            mode = "fts"
        }
    }
    if (mode != "fts" && positive.isNotEmpty()) mode = "title"

    val sql = StringBuilder()
    val args = ArrayList<Any>()
    when (mode) {
        "fts" -> {
            sql.append(
                "SELECT $NOTE_COLUMNS, snippet(notes_fts, 1, '<mark>', '</mark>', '…', 24) AS snippet, bm25(notes_fts, 4.0, 1.0) AS rank\n" +
                    "FROM notes_fts f JOIN notes n ON n.rowid = f.rowid\n" +
                    "WHERE notes_fts MATCH ?",
            )
            args.add(ftsExpr(usable, negative))
        }
        "title" -> {
            sql.append("SELECT $NOTE_COLUMNS, '' AS snippet, 0.0 AS rank FROM notes n WHERE 1=1")
            for (t in positive) {
                sql.append(" AND n.title LIKE ? ESCAPE '\\'")
                args.add("%" + escapeLike(t.text) + "%")
            }
            for (t in negative) {
                // A negated word needs something to subtract from; with
                // no full-text match, the title is what is left to test.
                sql.append(" AND n.title NOT LIKE ? ESCAPE '\\'")
                args.add("%" + escapeLike(t.text) + "%")
            }
        }
        else -> {
            sql.append("SELECT $NOTE_COLUMNS, '' AS snippet, 0.0 AS rank FROM notes n WHERE 1=1")
        }
    }
    for ((clause, cargs) in filters) {
        sql.append(" AND ").append(clause)
        args.addAll(cargs)
    }
    if (space != null && space != "") {
        sql.append(" AND n.space = ?")
        args.add(space)
    }
    when (mode) {
        "fts" -> sql.append("\nORDER BY bm25(notes_fts, 4.0, 1.0)")
        "title" -> sql.append("\nORDER BY n.title")
        else -> sql.append("\nORDER BY n.updated_at DESC")
    }
    sql.append(" LIMIT ?")
    args += lim
    return BuiltQuery(sql.toString(), args)
}

/**
 * Maps one operator term onto SQL over the notes row n, or null when the
 * term narrows nothing. The server-only filters throw instead: the
 * replica has no authors, tasks, or attachment text to test.
 */
private fun opFilter(t: Term): Pair<String, List<Any>>? = when (t.op) {
    Ops.TAG -> "EXISTS (SELECT 1 FROM tags t WHERE t.note_id = n.id AND t.tag = ?)" to listOf(t.value)
    Ops.PATH -> {
        val v = t.value.removePrefix("/").trimEnd('/')
        if (v == "") null
        else {
            val e = escapeLike(v)
            "(n.rel_path LIKE ? ESCAPE '\\' OR n.rel_path LIKE ? ESCAPE '\\')" to listOf("$e/%", "$e.%")
        }
    }
    Ops.SPACE -> "n.space = ?" to listOf(t.value)
    Ops.IS -> when (t.value) {
        "untagged" -> "NOT EXISTS (SELECT 1 FROM tags t WHERE t.note_id = n.id)" to emptyList()
        "html" -> "n.kind = 'html'" to emptyList()
        // is:task needs the task index only the server keeps.
        "task" -> throw ServerOnlyFilter(listOf("is:task"))
        else -> null
    }
    Ops.HAS -> throw ServerOnlyFilter(listOf("has:${t.value}"))
    Ops.AUTHOR -> throw ServerOnlyFilter(listOf("author:"))
    Ops.BEFORE -> "n.updated_at < ?" to listOf(t.date)
    Ops.AFTER -> "n.updated_at >= ?" to listOf(t.date)
    else -> null
}

/**
 * The FTS5 expression for the usable positive terms and the negated
 * ones: phrases quoted so user punctuation cannot break the syntax,
 * negations appended as NOT.
 */
internal fun ftsExpr(positive: List<Term>, negative: List<Term>): String {
    val parts = ArrayList<String>()
    for (t in positive) {
        if (runeCount(t.text) < 3) continue // the trigram tokenizer cannot match shorter terms
        parts += "\"" + t.text.replace("\"", "\"\"") + "\""
    }
    for (t in negative) {
        if (runeCount(t.text) < 3) continue
        parts += "NOT \"" + t.text.replace("\"", "\"\"") + "\""
    }
    if (parts.isEmpty()) return "\"\""
    return parts.joinToString(" ")
}

/** The server's rune count, not UTF-16 length: astral characters count once. */
internal fun runeCount(s: String): Int = s.codePointCount(0, s.length)

/** Escapes a LIKE pattern's specials: backslash, percent, underscore. */
internal fun escapeLike(s: String): String = buildString(s.length) {
    for (c in s) {
        when (c) {
            '\\' -> append("\\\\")
            '%' -> append("\\%")
            '_' -> append("\\_")
            else -> append(c)
        }
    }
}
