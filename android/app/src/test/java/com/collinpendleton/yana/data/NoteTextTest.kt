package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Test

/** The body derivation must mirror the server's scanner and renderer. */
class NoteTextTest {

    @Test
    fun markdownBodyIsTheContentAfterTheBlock() {
        assertEquals("Hello\n", markdownBody("---\nid: x\ncreated: 2026-01-01T00:00:00Z\n---\nHello\n"))
    }

    @Test
    fun dottedCloseEndsTheBlock() {
        assertEquals("Body", markdownBody("---\nid: x\n...\nBody"))
    }

    @Test
    fun noBlockMeansTheWholeFile() {
        assertEquals("# Title\n\ntext", markdownBody("# Title\n\ntext"))
    }

    @Test
    fun unterminatedBlockMeansTheWholeFile() {
        assertEquals("---\nid: x\nstuff", markdownBody("---\nid: x\nstuff"))
    }

    @Test
    fun crlfLineEndingsTolerated() {
        assertEquals("Hello\r\n", markdownBody("---\r\nid: x\r\n---\r\nHello\r\n"))
    }

    @Test
    fun htmlStripsTagsAndCollapsesSpace() {
        assertEquals("Dash stats", stripHtml("<h1>Dash</h1>\n<p>stats</p>"))
        assertEquals("hidden", stripHtml("<style>a { color: red }</style>hidden"))
        assertEquals("gone", stripHtml("<script>bad()</script>gone"))
    }

    @Test
    fun indexTextPairsBodyAndRaw() {
        val md = "---\nid: x\n---\n# Hi\n"
        assertEquals(Pair("# Hi\n", "# Hi\n"), indexText("md", md))
        val html = "<h1>Dash</h1>"
        assertEquals(Pair("Dash", "<h1>Dash</h1>"), indexText("html", html))
    }
}
