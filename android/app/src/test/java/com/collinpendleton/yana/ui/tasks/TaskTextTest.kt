package com.collinpendleton.yana.ui.tasks

import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * The task line reader: the inline HTML the server's scanner renders,
 * as plain text plus the spans that style it. The shapes below are the
 * ones the scanner produces (internal/render/tasks.go and its InlineHTML).
 */
class TaskTextTest {

    @Test
    fun readsPlainAndStyledText() {
        val p = TaskTextParser.parse("Buy <strong>milk</strong> and <em>eggs</em>")
        assertEquals("Buy milk and eggs", p.text)
        assertEquals(
            listOf(
                TaskTextParser.Span(4, 8, TaskTextParser.Style.Bold),
                TaskTextParser.Span(13, 17, TaskTextParser.Style.Italic),
            ),
            p.spans,
        )
    }

    @Test
    fun readsCodeStrikethroughAndNesting() {
        val p = TaskTextParser.parse("Run <code>go test</code> on <del><em>linux</em></del>")
        assertEquals("Run go test on linux", p.text)
        assertEquals(
            listOf(
                TaskTextParser.Span(4, 11, TaskTextParser.Style.Code),
                TaskTextParser.Span(15, 20, TaskTextParser.Style.Italic),
                TaskTextParser.Span(15, 20, TaskTextParser.Style.Strike),
            ),
            p.spans,
        )
    }

    @Test
    fun keepsTextOfLinksTagsAndWikilinks() {
        val p = TaskTextParser.parse(
            """See <a href="https://example.com" title="t">the guide</a> about <span class="tag" data-tag="home">home</span> and <span class="wikilink" data-target="Shopping">Shopping</span>""",
        )
        assertEquals("See the guide about home and Shopping", p.text)
        assertEquals(emptyList<TaskTextParser.Span>(), p.spans)
    }

    @Test
    fun decodesEntitiesAndComments() {
        val p = TaskTextParser.parse("A &amp; B &lt;C&gt; &#39;quoted&#39; <!-- a note -->done")
        assertEquals("A & B <C> 'quoted' done", p.text)
    }

    @Test
    fun closesUnbalancedTagsAtTheEnd() {
        val p = TaskTextParser.parse("never closed <strong>bold")
        assertEquals("never closed bold", p.text)
        assertEquals(listOf(TaskTextParser.Span(13, 17, TaskTextParser.Style.Bold)), p.spans)
    }

    @Test
    fun emptyLineReadsEmpty() {
        val p = TaskTextParser.parse("")
        assertEquals("", p.text)
        assertEquals(emptyList<TaskTextParser.Span>(), p.spans)
    }
}
