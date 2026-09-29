package com.collinpendleton.yana.ui.reader

import android.content.Intent
import android.net.Uri
import android.view.ViewGroup
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.webkit.WebViewAssetLoader
import androidx.webkit.WebViewAssetLoader.AssetsPathHandler
import com.collinpendleton.yana.data.decodeAssetPath
import com.collinpendleton.yana.data.normalizeServerUrl

/**
 * What a yana:// navigation from the reader asks for. The page's script
 * turns every wikilink, tag, and task box into one of these (see
 * web/src/android-reader.ts); the WebViewClient hands it back to the app
 * here, which is the only bridge the reader has.
 */
sealed interface ReaderTap {
    /** A resolved wikilink: open this note. */
    data class OpenNote(val id: String) : ReaderTap

    /** A dashed link: create the note at this path and open it. */
    data class CreateNote(val path: String) : ReaderTap

    /** An inline #tag: open its page (here, its search). */
    data class Tag(val name: String) : ReaderTap

    /** A task box tapped into [done]; [line] is the body line it sits on. */
    data class Task(val line: Int, val done: Boolean) : ReaderTap

    /** An attachment link: open this file under an `_assets/` directory. */
    data class Asset(val path: String) : ReaderTap
}

// The taps the reader's script builds, one shape each: a note id (a
// ULID), a create path (percent-encoded, one segment), a tag (lower
// case, letters digits _ / -), a task line with its state, and an
// attachment's tree path (percent-encoded per segment, `_assets`
// inside).
private val noteTap = Regex("""^yana://note/([A-Za-z0-9]{1,64})$""")
private val createTap = Regex("""^yana://create/([^/?#]+)$""")
private val tagTap = Regex("""^yana://tag/([a-z0-9_/-]{1,200})$""")
private val taskTap = Regex("""^yana://task/(\d+)\?done=(0|1)$""")
private val assetTap = Regex("""^yana://asset/([^#?]+)$""")

/**
 * Parses a yana:// url into the tap it asks for, or null when it is
 * none of ours. The page's script builds these; anything a page could
 * improvise outside their shapes goes nowhere.
 */
fun parseTap(url: Uri): ReaderTap? = tapOf(url.toString())

/** The tap a yana:// url string asks for; android-free so unit tests pin it. */
fun tapOf(url: String): ReaderTap? {
    noteTap.matchEntire(url)?.let { return ReaderTap.OpenNote(it.groupValues[1]) }
    createTap.matchEntire(url)?.let {
        // The page sends encodeURIComponent, which never leaves a '+'.
        return ReaderTap.CreateNote(java.net.URLDecoder.decode(it.groupValues[1], "UTF-8"))
    }
    tagTap.matchEntire(url)?.let { return ReaderTap.Tag(it.groupValues[1]) }
    taskTap.matchEntire(url)?.let {
        val line = it.groupValues[1].toIntOrNull() ?: return null
        return ReaderTap.Task(line = line, done = it.groupValues[2] == "1")
    }
    assetTap.matchEntire(url)?.let {
        // The page sends clean tree paths only (its join collapses the
        // climbing); anything that climbs or is not under an `_assets`
        // directory goes nowhere.
        val path = decodeAssetPath(it.groupValues[1])
        val clean = path.split('/').all { s -> s.isNotEmpty() && s != "." && s != ".." }
        if (clean && Regex("""(^|/)_assets/""").containsMatchIn(path)) return ReaderTap.Asset(path)
        return null
    }
    return null
}

/**
 * The reader: a WebView over the app's own assets through
 * WebViewAssetLoader, with no bridge back to the app, no file access,
 * and a CSP on the page that allows scripts only from those assets and
 * no network of any kind from inside. Images route through the app's
 * auth'd fetcher; taps leave as yana:// navigations; anything else on
 * the web opens in the system browser.
 */
class ReaderWebViewClient(
    private val assets: WebViewAssetLoader,
    private val fetcher: AssetFetcher?,
    private val serverUrl: () -> String?,
    private val openExternal: (Uri) -> Unit,
    private val onTap: (ReaderTap) -> Unit,
    /** Fired when a page finishes loading; the pane pushes renders after it. */
    private val onPageLoaded: () -> Unit = {},
) : WebViewClient() {
    override fun onPageFinished(view: WebView?, url: String?) {
        onPageLoaded()
    }

    override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
        val url = request.url
        // Images on the asset origin: fetch from the server with the
        // auth header and the disk cache, off the page's hands.
        if (url.path?.startsWith(AssetFetcher.IMAGE_PATH) == true) {
            val server = serverUrl()?.let(::normalizeServerUrl)
            if (fetcher != null && server != null && url.path != null) {
                val encoded = url.path!!.removePrefix(AssetFetcher.IMAGE_PATH)
                if (encoded.isNotEmpty()) return fetcher.fetch(server, encoded)
            }
            return WebResourceResponse("text/plain", "utf-8", 404, "no such image", emptyMap(), "".byteInputStream())
        }
        return assets.shouldInterceptRequest(request.url)
    }

    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
        val url = request.url
        parseTap(url)?.let { tap ->
            onTap(tap)
            return true
        }
        return when (url.scheme) {
            "http", "https" -> {
                // The note's own links leave for the system browser; the
                // reader's assets never navigate.
                openExternal(url)
                true
            }
            else -> true // file:, intent:, javascript:, everything else: nowhere
        }
    }
}

/** The sandbox settings the reader depends on, in one place. */
fun applyReaderSettings(web: WebView) {
    web.settings.apply {
        javaScriptEnabled = true
        allowFileAccess = false
        allowContentAccess = false
        @Suppress("DEPRECATION")
        allowFileAccessFromFileURLs = false
        @Suppress("DEPRECATION")
        allowUniversalAccessFromFileURLs = false
        mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
    }
    web.layoutParams = ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
}

/** The origin WebViewAssetLoader serves APK assets on. */
const val ASSET_ORIGIN = "https://appassets.androidplatform.net"

/** A reader client over [context]'s assets; the loader is the page's only file source. */
fun assetLoader(context: android.content.Context): WebViewAssetLoader =
    WebViewAssetLoader.Builder()
        .addPathHandler("/assets/", AssetsPathHandler(context))
        .build()

/** The base URL reader pages load under, so their asset references resolve. */
fun readerBaseUrl(): String = "$ASSET_ORIGIN/assets/reader/"

/** Opens a web address in the system browser, failures swallowed. */
fun openInBrowser(context: android.content.Context, uri: Uri) {
    runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, uri)) }
}
