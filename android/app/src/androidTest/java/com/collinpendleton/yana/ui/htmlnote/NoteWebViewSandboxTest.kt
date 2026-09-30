package com.collinpendleton.yana.ui.htmlnote

import android.os.SystemClock
import android.webkit.CookieManager
import android.webkit.WebSettings
import android.webkit.WebView
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.espresso.Espresso.onView
import androidx.test.espresso.matcher.ViewMatchers.isAssignableFrom
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The sandbox, on a device: the app's own NoteWebView pointed at a fake
 * content origin that serves the same headers the real one does. A note
 * whose script tries the acceptance escape — fetch the API, read the app
 * origin's cookies, navigate away — gets nowhere: the CSP blocks the
 * fetches, cookies do not cross origins, and the WebViewClient cancels
 * the navigation. A trusted note's canvas animation runs.
 *
 * Run with a device or emulator attached: ./gradlew connectedDebugAndroidTest
 */
@RunWith(AndroidJUnit4::class)
class NoteWebViewSandboxTest {
    @get:Rule val rule = createComposeRule()

    private lateinit var content: MockWebServer
    private lateinit var api: MockWebServer
    private var apiHits = 0

    /** The exact policy the content origin puts on every note page (internal/server/content.go). */
    private val csp = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
        "img-src 'self'; font-src 'self'; media-src 'self'; connect-src 'none'; " +
        "form-action 'none'; base-uri 'self'"

    @Before fun start() {
        content = MockWebServer()
        api = MockWebServer()
        content.start()
        api.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                apiHits++
                return MockResponse.Builder().code(200).addHeader("Content-Type", "application/json").body("[]").build()
            }
        }
        api.start()
    }

    @After fun stop() {
        rule.runOnUiThread {
            runCatching { CookieManager.getInstance().removeAllCookies(null) }
            CookieManager.getInstance().flush()
        }
        content.close()
        api.close()
    }

    private fun contentBase() = content.url("/").toString().trimEnd('/')
    private fun apiBase() = api.url("/").toString().trimEnd('/')

    @Test fun scriptCannotEscapeTheContentOrigin() {
        content.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                return if (request.url.encodedPath == "/n/one") page(containmentBody()) else notFound()
            }
        }
        // A cookie the app origin would hold; the content origin must never see it.
        rule.runOnUiThread {
            CookieManager.getInstance().setCookie(apiBase() + "/", "yana_refresh=stolen; Path=/")
            CookieManager.getInstance().flush()
        }

        rule.setContent { NoteWebView(url = contentBase() + "/n/one?token=tok") }
        val webView = captureWebView()
        val report = awaitTitle(webView, "R|")

        // The script's fetches never reach an API, relative or absolute.
        assertTrue(report, report.contains("rel:blocked"))
        assertTrue(report, report.contains("api:blocked"))
        assertFalse(report, report.contains("rel:ok"))
        assertFalse(report, report.contains("api:ok"))
        // No cookie from the app origin leaks into the page.
        assertFalse(report, report.substringAfter("cookie:").substringBefore('|').contains("yana_refresh"))
        // The navigation away was cancelled: the page is still the note.
        assertTrue(report, report.substringAfter("url:").startsWith(contentBase()))
        assertEquals(0, apiHits)
        rule.runOnUiThread {
            assertTrue(webView.url.orEmpty().startsWith(contentBase()))
            // The settings the sandbox depends on, on the shipped view.
            assertTrue(webView.settings.javaScriptEnabled)
            assertFalse(webView.settings.allowFileAccess)
            assertFalse(webView.settings.allowContentAccess)
            @Suppress("DEPRECATION")
            assertFalse(webView.settings.allowFileAccessFromFileURLs)
            @Suppress("DEPRECATION")
            assertFalse(webView.settings.allowUniversalAccessFromFileURLs)
            assertEquals(WebSettings.MIXED_CONTENT_NEVER_ALLOW, webView.settings.mixedContentMode)
        }
    }

    @Test fun trustedCanvasAnimationRuns() {
        content.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                return if (request.url.encodedPath == "/n/two") canvasPage() else notFound()
            }
        }

        rule.setContent { NoteWebView(url = contentBase() + "/n/two?token=tok") }
        val webView = captureWebView()
        val report = awaitTitle(webView, "C|")

        assertTrue(report, report.contains("frames:8"))
        val drawn = report.substringAfter("len:").substringBefore('|').toInt()
        val blank = report.substringAfter("blank:").substringBefore('|').toInt()
        assertTrue("drawn $drawn bytes against a blank $blank", drawn > blank + 10)
    }

    /** The page the acceptance describes: fetch the API, read cookies, navigate off the origin. */
    private fun containmentScript(): String = """
        var out = [];
        function report(){
          out.push('url:' + location.href);
          out.push('cookie:' + (document.cookie === '' ? 'none' : document.cookie));
          document.title = 'R|' + out.join('|');
        }
        var pending = 2;
        function settled(){ if (--pending === 0) report(); }
        fetch('/api/notes')
          .then(function(r){ out.push('rel:ok' + r.status); }, function(){ out.push('rel:blocked'); })
          .then(settled, settled);
        fetch('${apiBase()}/api/notes')
          .then(function(r){ out.push('api:ok' + r.status); }, function(){ out.push('api:blocked'); })
          .then(settled, settled);
        setTimeout(function(){ location.href = 'http://example.net/leak'; setTimeout(report, 600); }, 300);
        setTimeout(report, 6000);
    """.trimIndent()

    /** A trusted note's animation: eight requestAnimationFrame turns that paint the canvas. */
    private fun canvasScript(): String = """
        var cv = document.getElementById('c');
        var g = cv.getContext('2d');
        var frames = 0;
        function draw(){
          frames++;
          g.fillStyle = '#0f766e';
          g.fillRect((frames * 3) % 16, (frames * 5) % 16, 5, 5);
          if (frames < 8) { requestAnimationFrame(draw); }
          else {
            var blank = document.createElement('canvas');
            blank.width = 16; blank.height = 16;
            document.title = 'C|frames:' + frames + '|len:' + cv.toDataURL().length + '|blank:' + blank.toDataURL().length;
          }
        }
        requestAnimationFrame(draw);
    """.trimIndent()

    private fun page(body: String): MockResponse =
        MockResponse.Builder()
            .code(200)
            .addHeader("Content-Security-Policy", csp)
            .addHeader("Content-Type", "text/html; charset=utf-8")
            .addHeader("X-Content-Type-Options", "nosniff")
            .addHeader("Referrer-Policy", "no-referrer")
            .addHeader("Cache-Control", "no-store")
            .body("<!doctype html><html><head><base href=\"" + contentBase() + "/t/tok/f/\"></head>" + body + "</html>")
            .build()

    private fun containmentBody() = "<body><script>" + containmentScript() + "</script></body>"

    private fun canvasPage(): MockResponse =
        page("<body><canvas id=\"c\" width=\"16\" height=\"16\"></canvas><script>" + canvasScript() + "</script></body>")

    private fun notFound(): MockResponse = MockResponse.Builder().code(404).body("no such page").build()

    private fun captureWebView(): WebView {
        var captured: WebView? = null
        onView(isAssignableFrom(WebView::class.java)).check { view, _ -> captured = view as WebView }
        return captured ?: throw AssertionError("no WebView in the hierarchy")
    }

    /** Polls the page's title, which the test scripts report through; no bridge in production code. */
    private fun awaitTitle(webView: WebView, prefix: String, timeoutMs: Long = 30_000): String {
        val deadline = SystemClock.elapsedRealtime() + timeoutMs
        var last: String? = null
        while (SystemClock.elapsedRealtime() < deadline) {
            val latch = CountDownLatch(1)
            var value: String? = null
            rule.runOnUiThread {
                webView.evaluateJavascript("document.title") { v ->
                    value = v
                    latch.countDown()
                }
            }
            latch.await(5, TimeUnit.SECONDS)
            val title = value?.trim('"')
            if (title != null) {
                last = title
                if (title.startsWith(prefix)) return title
            }
            SystemClock.sleep(250)
        }
        throw AssertionError("page never reported $prefix; last title was $last")
    }
}
