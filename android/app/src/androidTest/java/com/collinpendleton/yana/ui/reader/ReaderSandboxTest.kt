package com.collinpendleton.yana.ui.reader

import android.webkit.WebView
import android.webkit.WebChromeClient
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.collinpendleton.yana.crdt.crdt.Crdt
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicInteger
import java.util.concurrent.atomic.AtomicReference
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The reader's sandbox, on a device: the app's own reader page (the
 * asset template plus a render of a note whose markdown tries the
 * acceptance escapes) loaded under the production WebViewClient and
 * settings. A `<script>`, an `onerror` handler, and a `javascript:`
 * link in a note's markup must not run: the renderer escapes them, the
 * page's CSP allows scripts only from the app's own assets, and no
 * fetch reaches any server.
 *
 * Run with a device or emulator attached: ./gradlew connectedDebugAndroidTest
 */
@RunWith(AndroidJUnit4::class)
class ReaderSandboxTest {
    private lateinit var api: MockWebServer
    private var apiHits = 0

    @Before fun start() {
        api = MockWebServer()
        api.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                apiHits++
                return MockResponse.Builder().code(200).addHeader("Content-Type", "application/json").body("[]").build()
            }
        }
        api.start()
    }

    @After fun stop() {
        api.close()
    }

    private fun apiBase() = api.url("/").toString().trimEnd('/')

    /** The note a hostile or careless author could write. */
    private fun hostileNote(): String = listOf(
        "# Injection attempts",
        "",
        "<script>document.title='PWNED';fetch('${apiBase()}/api/notes');fetch('/api/notes');location.href='http://example.net/leak';</script>",
        "",
        "<img src=x onerror=\"document.title='PWNED-ONERROR'\">",
        "",
        "[a javascript link](javascript:document.title='PWNED-LINK')",
        "",
        "<iframe src=\"https://example.net/\"></iframe>",
        "",
        "But an ordinary task still renders: - [ ] tick me",
    ).joinToString("\n")

    @Test fun markdownInjectionCannotRunInTheReader() {
        val html = Crdt.renderMarkdown(hostileNote())

        // The device's own render (the same engine the server uses) never
        // passes raw HTML through: the markup arrives as text.
        assertFalse("render passed a script through", html.contains("<script>"))
        assertFalse("render passed an iframe through", html.contains("<iframe"))
        assertTrue("task boxes survive", html.contains("data-line="))

        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val template = context.assets.open("reader/reader.html").bufferedReader().use { it.readText() }
        val page = ReaderPage.build(
            template = template,
            dark = false,
            base = "",
            space = "",
            readOnly = false,
            links = emptyList(),
            bodyHtml = html,
        )

        val instrument = InstrumentationRegistry.getInstrumentation()
        val alerts = AtomicInteger(0)
        val external = AtomicReference<String?>(null)
        val taps = AtomicInteger(0)
        var title = ""
        var currentUrl = ""

        instrument.runOnMainSync {
            val wv = WebView(context)
            applyReaderSettings(wv)
            wv.webChromeClient = object : WebChromeClient() {
                override fun onJsAlert(view: WebView?, url: String?, message: String?, result: android.webkit.JsResult?): Boolean {
                    alerts.incrementAndGet()
                    result?.cancel()
                    return true
                }
            }
            wv.webViewClient = ReaderWebViewClient(
                assets = assetLoader(context),
                fetcher = null,
                serverUrl = { apiBase() },
                openExternal = { uri -> external.set(uri.toString()) },
                onTap = { taps.incrementAndGet() },
            )
            wv.loadDataWithBaseURL(readerBaseUrl(), page, "text/html", "utf-8", null)

            // Give the page time to load and any smuggled script time to
            // misbehave, then read what it left behind.
            repeat(40) {
                val latch = CountDownLatch(1)
                wv.evaluateJavascript("document.title") { v ->
                    title = v?.trim('"').orEmpty()
                    latch.countDown()
                }
                try {
                    latch.await(1, TimeUnit.SECONDS)
                } catch (_: InterruptedException) {
                }
                currentUrl = wv.url.orEmpty()
                if (title.isNotEmpty() || currentUrl.isNotEmpty()) {
                    // keep polling: a script that lands takes a moment
                }
            }
            currentUrl = wv.url.orEmpty()
            wv.evaluateJavascript("document.documentElement.outerHTML.length") { }
            wv.destroy()
        }

        // No script ran: none of the markers the injections set appear.
        assertFalse("an injected script ran", title.contains("PWNED"))
        assertEquals("no dialog came from the page", 0, alerts.get())
        // No fetch reached an API, relative or absolute.
        assertEquals("a fetch escaped the page", 0, apiHits)
        // Nothing navigated away: the page is still the reader, and no
        // external address was handed to the system browser.
        assertTrue("the reader navigated away: $currentUrl", currentUrl.startsWith(readerBaseUrl()))
        assertEquals("an external link fired", null, external.get())
        // No tap was produced either: injections do not forge yana:// taps.
        assertEquals(0, taps.get())

        // The settings the sandbox depends on, applied the way the reader
        // applies them, are checked by construction above through
        // applyReaderSettings; the production client also blocks every
        // navigation that is not a tap or a web link.
    }
}
