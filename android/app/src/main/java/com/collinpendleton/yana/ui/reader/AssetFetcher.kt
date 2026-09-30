package com.collinpendleton.yana.ui.reader

import android.webkit.WebResourceResponse
import com.collinpendleton.yana.data.decodeAssetPath
import java.io.File
import okhttp3.Cache
import okhttp3.CacheControl
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request

/**
 * Serves the reader's images: a relative `![](pic.png)` becomes a
 * request on the app-assets origin, and this fetcher turns it into the
 * server's `/api/files/` with the bearer token attached — the token
 * never enters the page. A disk cache (OkHttp's, in the app's cache
 * directory) keeps what the server served, so a note read in airplane
 * mode still shows the images it showed online; stale entries are
 * revalidated, and served as-is when the server cannot be reached.
 * An image whose upload still waits in the offline queue answers with
 * a placeholder instead of the server's 404, and is uncached so the
 * real one shows as soon as the queue drains.
 */
class AssetFetcher(
    plain: OkHttpClient,
    cacheDir: File,
    private val token: () -> String?,
) {
    private val http: OkHttpClient = plain.newBuilder()
        .cache(Cache(File(cacheDir, "reader-images"), CACHE_BYTES))
        .build()

    /** The `_assets/` tree paths whose upload the offline queue still holds. */
    @Volatile
    var pending: Set<String> = emptySet()

    /** Fetches one asset path (percent-encoded segments joined by /) for the reader. */
    fun fetch(server: HttpUrl, encodedPath: String): WebResourceResponse {
        if (decodeAssetPath(encodedPath) in pending) return placeholder(encodedPath)
        val url = server.newBuilder()
            .addPathSegments("api/files")
            .addEncodedPathSegments(encodedPath)
            .build()
        val response = run(url, null) ?: run(url, CacheControl.FORCE_CACHE)
            ?: return missing()
        try {
            val body = response.body
            val contentType = body?.contentType()
            return if (response.isSuccessful && body != null) {
                WebResourceResponse(
                    contentType?.toString() ?: "application/octet-stream",
                    null,
                    response.code,
                    response.message.ifEmpty { "OK" },
                    response.headers.toMultimap().mapValues { (_, v) -> v.joinToString(", ") },
                    body.byteStream(),
                )
            } else {
                body?.close()
                WebResourceResponse("text/plain", "utf-8", response.code, response.message.ifEmpty { "error" }, emptyMap(), "".byteInputStream())
            }
        } catch (_: Exception) {
            return missing()
        }
    }

    /** What a queued-but-not-yet-uploaded image shows: its name and that it waits. */
    private fun placeholder(encodedPath: String): WebResourceResponse {
        val name = encodedPath.substringAfterLast('/')
        val text = PLACEHOLDER_TEXT
        val svg = """
            <svg xmlns="http://www.w3.org/2000/svg" width="640" height="240" viewBox="0 0 640 240">
              <rect x="1" y="1" width="638" height="238" rx="8" fill="#f2f0ec" stroke="#a8a29a" stroke-width="1.5" stroke-dasharray="6 5"/>
              <text x="320" y="112" text-anchor="middle" font-family="sans-serif" font-size="26" fill="#78716c">$name</text>
              <text x="320" y="146" text-anchor="middle" font-family="sans-serif" font-size="16" fill="#a8a29a">$text</text>
            </svg>
        """.trimIndent()
        return WebResourceResponse(
            "image/svg+xml",
            "utf-8",
            200,
            "OK",
            mapOf("Cache-Control" to "no-store"),
            svg.byteInputStream(),
        )
    }

    private fun run(url: HttpUrl, control: CacheControl?): okhttp3.Response? = try {
        val builder = Request.Builder().url(url).get()
        token()?.let { builder.header("Authorization", "Bearer $it") }
        control?.let { builder.cacheControl(it) }
        http.newCall(builder.build()).execute()
    } catch (_: Exception) {
        null
    }

    private fun missing(): WebResourceResponse =
        WebResourceResponse("text/plain", "utf-8", 404, "no such image", emptyMap(), "".byteInputStream())

    companion object {
        /** Images are worth a big cache; they do not change under the same name. */
        private const val CACHE_BYTES = 64L * 1024 * 1024

        /** The path prefix the reader's pages ask for images under. */
        const val IMAGE_PATH = "/yana-img/"

        /** What the placeholder says, matching the offline upload toast. */
        const val PLACEHOLDER_TEXT = "uploads when the connection returns"

        fun serverOf(raw: String?): HttpUrl? = raw?.toHttpUrlOrNull()
    }
}
