package com.collinpendleton.yana.data.search

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** The SQL builder must shape the server's queries (internal/index/opsearch.go). */
class LocalSearchTest {

    @Test
    fun ftsModeForTrigramWords() {
        val built = buildSearchQuery(parseQuery("lantern"), null)
        assertTrue(built.sql.contains("notes_fts MATCH ?"))
        assertTrue(built.sql.contains("snippet(notes_fts, 1, '<mark>', '</mark>', '…', 24)"))
        assertTrue(built.sql.trim().endsWith("ORDER BY bm25(notes_fts, 4.0, 1.0) LIMIT ?"))
        assertEquals(listOf<Any>("\"lantern\"", 50), built.args)
    }

    @Test
    fun quotedPhrasesEscapeInnerQuotes() {
        // A quote inside an unquoted word doubles up in the phrase; a
        // quoted term shorter than three runes cannot match the trigram
        // index and drops out, exactly as on the server.
        val built = buildSearchQuery(parseQuery("ab\"cd glass \"hi\""), null)
        assertEquals("\"ab\"\"cd\" \"glass\"", built.args.first())
    }

    @Test
    fun titleModeForShortWords() {
        val built = buildSearchQuery(parseQuery("ve"), null)
        assertTrue(built.sql.contains("n.title LIKE ? ESCAPE '\\'"))
        assertTrue(built.sql.trim().endsWith("ORDER BY n.title LIMIT ?"))
        assertEquals(listOf<Any>("%ve%", 50), built.args)
    }

    @Test
    fun filterModeRunsNewestFirst() {
        val built = buildSearchQuery(parseQuery("is:untagged"), null)
        assertTrue(built.sql.contains("NOT EXISTS (SELECT 1 FROM tags t WHERE t.note_id = n.id)"))
        assertTrue(built.sql.trim().endsWith("ORDER BY n.updated_at DESC LIMIT ?"))
    }

    @Test
    fun operatorsNarrowTheMatch() {
        val built = buildSearchQuery(parseQuery("tag:home path:docs lantern"), null)
        assertTrue(built.sql.contains("EXISTS (SELECT 1 FROM tags t WHERE t.note_id = n.id AND t.tag = ?)"))
        assertTrue(built.sql.contains("(n.rel_path LIKE ? ESCAPE '\\' OR n.rel_path LIKE ? ESCAPE '\\')"))
        assertEquals("home", built.args[1])
        assertEquals("docs/%", built.args[2])
        assertEquals("docs.%", built.args[3])
    }

    @Test
    fun negatedOperatorsWrapInNot() {
        val built = buildSearchQuery(parseQuery("-tag:done lantern"), null)
        assertTrue(built.sql.contains("NOT (EXISTS (SELECT 1 FROM tags t WHERE t.note_id = n.id AND t.tag = ?))"))
    }

    @Test
    fun datesCompareAgainstUpdatedNanos() {
        val built = buildSearchQuery(parseQuery("after:2026-01-01 lantern"), null)
        val nanos = built.args[1] as Long
        assertEquals(1767225600000000000L, nanos)
        assertTrue(built.sql.contains("n.updated_at >= ?"))
    }

    @Test
    fun spaceScopesTheQuery() {
        val built = buildSearchQuery(parseQuery("lantern"), "garden")
        assertTrue(built.sql.contains("AND n.space = ?"))
        assertEquals("garden", built.args[1])
    }

    @Test
    fun serverOnlyFiltersSaySo() {
        for (q in listOf("author:collin", "is:task", "has:image")) {
            try {
                buildSearchQuery(parseQuery(q), null)
                throw AssertionError("expected ServerOnlyFilter for $q")
            } catch (e: ServerOnlyFilter) {
                // expected
            }
        }
    }

    @Test
    fun likePatternsEscapeSpecials() {
        assertEquals("\\\\a\\%b\\_c", escapeLike("\\a%b_c"))
    }

    @Test
    fun limitClampsToTheServerRange() {
        val built = buildSearchQuery(parseQuery("lantern"), null, limit = 999)
        assertEquals(50, built.args.last())
    }
}
