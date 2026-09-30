package com.collinpendleton.yana.ui.htmlnote

import android.content.Intent
import android.net.Uri
import android.view.ViewGroup
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.runtime.Composable
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/** What the note WebView does with a navigation request. */
enum class NavDecision { Load, External, Blocked }

/**
 * Decides navigation for the note WebView. Only http(s) pages on the
 * content origin load inside it; other http(s) addresses go to the
 * system browser; everything else — file:, intent:, javascript: — goes
 * nowhere. Unparseable addresses are blocked rather than handed on.
 */
fun navDecision(url: String?, origin: HttpUrl?): NavDecision {
    val target = url?.toHttpUrlOrNull() ?: return NavDecision.Blocked
    if (origin == null) return NavDecision.Blocked
    return if (target.scheme == origin.scheme && target.host == origin.host && target.port == origin.port) {
        NavDecision.Load
    } else {
        NavDecision.External
    }
}

/** A WebViewClient that keeps the WebView on the content origin it was opened on. */
class NoteWebViewClient(
    private val origin: () -> HttpUrl?,
    private val openExternal: (Uri) -> Unit,
) : WebViewClient() {
    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean =
        when (navDecision(request.url?.toString(), origin())) {
            NavDecision.Load -> false
            NavDecision.External -> {
                request.url?.let(openExternal)
                true
            }
            NavDecision.Blocked -> true
        }
}

/**
 * The rendered note: a WebView on the content origin with JavaScript on
 * and no bridge back to the app, no file access, and mixed content
 * blocked. The signed URL is the only credential it carries; links off
 * the content origin leave for the system browser.
 */
@Composable
fun NoteWebView(url: String, modifier: Modifier = Modifier) {
    val origin = remember { mutableStateOf<HttpUrl?>(null) }
    val loaded = remember { mutableStateOf<String?>(null) }
    AndroidView(
        modifier = modifier,
        factory = { ctx ->
            WebView(ctx).apply {
                layoutParams = ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT)
                settings.apply {
                    javaScriptEnabled = true
                    allowFileAccess = false
                    allowContentAccess = false
                    @Suppress("DEPRECATION")
                    allowFileAccessFromFileURLs = false
                    @Suppress("DEPRECATION")
                    allowUniversalAccessFromFileURLs = false
                    mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
                }
                webViewClient = NoteWebViewClient(
                    origin = { origin.value },
                    openExternal = { uri -> runCatching { ctx.startActivity(Intent(Intent.ACTION_VIEW, uri)) } },
                )
            }
        },
        update = { wv ->
            origin.value = url.toHttpUrlOrNull()
            if (loaded.value != url) {
                loaded.value = url
                wv.loadUrl(url)
            }
        },
        onRelease = { it.destroy() },
    )
}
