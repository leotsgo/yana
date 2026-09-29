package com.collinpendleton.yana.ui.reader

import android.net.Uri
import com.collinpendleton.yana.data.ResolvedLink
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The yana:// protocol between the reader page and the app. Uri.parse
 * is stubbed to null in plain unit tests, so these go through the
 * same-shaped android-free strings via the tap regexes directly.
 */
class ReaderTapTest {
    private fun tap(url: String): ReaderTap? = tapOf(url)

    @Test fun opensNotes() {
        assertEquals(ReaderTap.OpenNote("01ABCDEFGHJKLMNPQRSTVWXYZ0"), tap("yana://note/01ABCDEFGHJKLMNPQRSTVWXYZ0"))
    }

    @Test fun createsAtDecodedPaths() {
        assertEquals(
            ReaderTap.CreateNote("main/guides/new note.md"),
            tap("yana://create/main%2Fguides%2Fnew%20note.md"),
        )
    }

    @Test fun tagsCarryTheirName() {
        assertEquals(ReaderTap.Tag("project/sub"), tap("yana://tag/project/sub"))
        assertEquals(ReaderTap.Tag("done"), tap("yana://tag/done"))
    }

    @Test fun tasksCarryLineAndState() {
        assertEquals(ReaderTap.Task(12, true), tap("yana://task/12?done=1"))
        assertEquals(ReaderTap.Task(3, false), tap("yana://task/3?done=0"))
    }

    @Test fun attachmentsCarryTheirTreePath() {
        assertEquals(
            ReaderTap.Asset("main/_assets/report q3.pdf"),
            tap("yana://asset/main/_assets/report%20q3.pdf"),
        )
        assertEquals(
            ReaderTap.Asset("work/guides/_assets/budget.xlsx"),
            tap("yana://asset/work/guides/_assets/budget.xlsx"),
        )
    }

    @Test fun attachmentsOutsideAssetsGoNowhere() {
        assertNull(tap("yana://asset/work/notes/other.md"))
        assertNull(tap("yana://asset/main/../../etc/_assets/x.pdf"))
    }

    @Test fun anythingElseIsNotOurs() {
        assertNull(tap("https://example.net/leak"))
        assertNull(tap("yana://unknown/x"))
        assertNull(tap("yana://task/notanumber"))
        assertNull(tap("yana://task/5"))
        assertNull(tap("javascript:alert(1)"))
        assertNull(tap("yana://note"))
        assertNull(tap("yana://note/../../etc"))
        assertNull(tap("yana://tag/UPPER"))
    }
}

/** The page the WebView loads: config, theme, and body in the right places. */
class ReaderPageTest {
    private val template = """
        <html lang="en" data-theme="__THEME__">
        <script type="application/json" id="yana-config"><!--YANA-CONFIG--></script>
        </head>
        <body>
          <div class="reader" id="yana-note">
            <template id="yana-body"><!--YANA-BODY--></template>
          </div>
        </body>
        </html>
    """.trimIndent()

    @Test fun buildsTheDocument() {
        val page = ReaderPage.build(
            template = template,
            dark = true,
            base = "main/guides",
            space = "main",
            readOnly = false,
            links = listOf(
                ResolvedLink("target", "01ABC", true),
                ResolvedLink("missing", null, false),
            ),
            bodyHtml = "<h1>Hello</h1>",
        )
        assertTrue(page.contains("data-theme=\"dark\""))
        assertTrue(page.contains("\"base\":\"main/guides\""))
        assertTrue(page.contains("\"dark\":true"))
        assertTrue(page.contains("\"raw\":\"target\""))
        assertTrue(page.contains("\"to\":\"01ABC\""))
        assertTrue(page.contains("\"ok\":true"))
        assertTrue(page.contains("\"raw\":\"missing\""))
        assertTrue(page.contains("\"ok\":false"))
        assertTrue(page.contains("<h1>Hello</h1>"))
        assertFalse(page.contains("__THEME__"))
        assertFalse(page.contains("<!--YANA-CONFIG-->"))
        assertFalse(page.contains("<!--YANA-BODY-->"))
    }

    @Test fun lightTheme() {
        val page = ReaderPage.build(template, dark = false, base = "", space = "", readOnly = true, links = emptyList(), bodyHtml = "")
        assertTrue(page.contains("data-theme=\"light\""))
        assertTrue(page.contains("\"readOnly\":true"))
    }
}
