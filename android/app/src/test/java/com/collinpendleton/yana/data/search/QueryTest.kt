package com.collinpendleton.yana.data.search

import java.time.LocalDate
import java.time.ZoneOffset
import org.junit.Assert.assertEquals
import org.junit.Test

/** The parser must agree with the server's (internal/search/query_test.go). */
class QueryTest {

    @Test
    fun plainWordsStayText() {
        val q = parseQuery("water heater")
        assertEquals(
            listOf(Term(raw = "water", negated = false, text = "water"), Term(raw = "heater", negated = false, text = "heater")),
            q.terms,
        )
    }

    @Test
    fun theAcceptanceQuery() {
        val q = parseQuery("tag:home -tag:done \"water heater\" after:2026-01-01")
        assertEquals(
            listOf(
                Term(raw = "tag:home", negated = false, op = Ops.TAG, value = "home"),
                Term(raw = "-tag:done", negated = true, op = Ops.TAG, value = "done"),
                Term(raw = "\"water heater\"", negated = false, quoted = true, text = "water heater"),
                Term(
                    raw = "after:2026-01-01",
                    negated = false,
                    op = Ops.AFTER,
                    value = "2026-01-01",
                    date = LocalDate.of(2026, 1, 1).atStartOfDay(ZoneOffset.UTC).toEpochSecond() * 1_000_000_000L,
                ),
            ),
            q.terms,
        )
    }

    @Test
    fun bareHashIsATag() {
        val q = parseQuery("#Home -#done")
        assertEquals(
            listOf(
                Term(raw = "#Home", negated = false, op = Ops.TAG, value = "home"),
                Term(raw = "-#done", negated = true, op = Ops.TAG, value = "done"),
            ),
            q.terms,
        )
    }

    @Test
    fun unknownOperatorIsText() {
        assertEquals(
            listOf(Term(raw = "foo:bar", negated = false, text = "foo:bar")),
            parseQuery("foo:bar").terms,
        )
    }

    @Test
    fun uppercaseOperatorNameIsText() {
        assertEquals(
            listOf(Term(raw = "Tag:home", negated = false, text = "Tag:home")),
            parseQuery("Tag:home").terms,
        )
    }

    @Test
    fun unknownIsAndHasSelectorsAreText() {
        assertEquals(
            listOf(
                Term(raw = "is:nope", negated = false, text = "is:nope"),
                Term(raw = "has:nope", negated = false, text = "has:nope"),
            ),
            parseQuery("is:nope has:nope").terms,
        )
    }

    @Test
    fun selectorsAreCaseInsensitive() {
        assertEquals(
            listOf(
                Term(raw = "is:HTML", negated = false, op = Ops.IS, value = "html"),
                Term(raw = "has:Image", negated = false, op = Ops.HAS, value = "image"),
            ),
            parseQuery("is:HTML has:Image").terms,
        )
    }

    @Test
    fun aBadDateIsText() {
        assertEquals(
            listOf(
                Term(raw = "before:january", negated = false, text = "before:january"),
                Term(raw = "after:2026-1-1", negated = false, text = "after:2026-1-1"),
            ),
            parseQuery("before:january after:2026-1-1").terms,
        )
    }

    @Test
    fun emptyValuesAreText() {
        assertEquals(
            listOf("tag:", "path:", "space:", "is:", "has:", "author:", "before:", "after:")
                .map { Term(raw = it, negated = false, text = it) },
            parseQuery("tag: path: space: is: has: author: before: after:").terms,
        )
    }

    @Test
    fun quotedOperatorValues() {
        assertEquals(
            listOf(
                Term(raw = "path:\"my docs/\"", negated = false, op = Ops.PATH, value = "my docs/", quoted = true),
                Term(raw = "tag:\"Two Words\"", negated = false, op = Ops.TAG, value = "two words", quoted = true),
            ),
            parseQuery("path:\"my docs/\" tag:\"Two Words\"").terms,
        )
    }

    @Test
    fun negatedPlainWordAndPhrase() {
        assertEquals(
            listOf(
                Term(raw = "-draft", negated = true, text = "draft"),
                Term(raw = "-\"out of date\"", negated = true, quoted = true, text = "out of date"),
            ),
            parseQuery("-draft -\"out of date\"").terms,
        )
    }

    @Test
    fun aLoneDashIsAWord() {
        assertEquals(listOf(Term(raw = "-", negated = false, text = "-")), parseQuery("-").terms)
    }

    @Test
    fun hashAloneIsAWord() {
        assertEquals(listOf(Term(raw = "#", negated = false, text = "#")), parseQuery("#").terms)
    }

    @Test
    fun tabsAndNewlinesSeparate() {
        assertEquals(3, parseQuery("a\tb\nc").terms.size)
    }

    @Test
    fun astralCharactersCountOncePerRune() {
        // Two emoji are two runes in Go's parser: too short for the
        // trigram index, so the query runs in title mode.
        val q = parseQuery("🙈🙈")
        val built = buildSearchQuery(q, null)
        assertEquals(true, built.sql.contains("LIKE"))
    }
}
