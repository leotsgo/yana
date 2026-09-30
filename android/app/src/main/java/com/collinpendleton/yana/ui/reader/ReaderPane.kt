package com.collinpendleton.yana.ui.reader

import android.content.ActivityNotFoundException
import android.content.Intent
import android.webkit.WebView
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.core.content.FileProvider
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import com.collinpendleton.yana.data.GoRender
import com.collinpendleton.yana.data.Note
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.ResolvedLink
import com.collinpendleton.yana.data.TickOutcome
import com.collinpendleton.yana.data.YanaClient
import com.collinpendleton.yana.data.YanaJson
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.htmlnote.NoteWebView
import java.io.File
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.serialization.builtins.serializer

/**
 * A markdown note, rendered to read: the shared Go engine renders on
 * the device, the reader page (an app asset built from the web's own
 * rich runtime) draws it in the Identity palette, and every tap a
 * rendered note can produce — a wikilink, a dashed link, a tag, a task
 * box — comes back as a yana:// navigation the pane answers. The body
 * re-renders through the same path when the live document changes, so
 * the read view is as current as the editor, offline included.
 */
@Composable
fun ReaderPane(
    repo: NoteRepository,
    client: YanaClient,
    note: Note,
    body: String?,
    dark: Boolean,
    onOpenNote: (String) -> Unit,
    onTag: (String) -> Unit,
    onToast: (String) -> Unit,
    modifier: Modifier = Modifier,
    /** The body line a tasks row opened the note at; -1 opens at the top. */
    atLine: Int = -1,
    /** Where the editor left the note, as a fraction of its scroll; the page resumes there. */
    restoreFraction: Float = 0f,
    /** The read view's scroll as a fraction, reported as the person moves; feeds the editor's open. */
    onScrollFraction: (Float) -> Unit = {},
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    // The template ships in the APK's assets; one read serves the pane.
    val template = remember {
        context.assets.open("reader/reader.html").bufferedReader().use { it.readText() }
    }
    val fetcher = remember(client) {
        AssetFetcher(client.socketClient(), context.cacheDir, token = { client.auth.validAccessToken() })
    }

    // A tick queued offline flips the cached body; that flipped body is
    // what this pane renders until the live document says otherwise.
    var flippedBody by remember(note.id) { mutableStateOf<String?>(null) }
    val text = (flippedBody ?: body).orEmpty()

    // Render and resolve off the main thread, debounced the way the
    // web's reader debounces its live renders.
    var html by remember(note.id) { mutableStateOf("") }
    var links by remember(note.id) { mutableStateOf<List<ResolvedLink>>(emptyList()) }
    LaunchedEffect(note.id, text) {
        // The first render goes straight out; only live changes wait.
        if (html.isNotEmpty()) delay(220)
        html = withContext(Dispatchers.Default) { runCatching { GoRender.markdown(text) }.getOrDefault("") }
        links = runCatching { repo.resolveLinks(note, text) }.getOrDefault(emptyList())
    }

    // The page document embeds the first (render, resolution) pair; a
    // theme change or a reopen rebuilds it, everything else goes in
    // through the page's own setBody once it has loaded.
    val readOnly = note.role == "viewer"
    var first by remember(note.id) { mutableStateOf<Pair<String, List<ResolvedLink>>?>(null) }
    if (first == null && (html.isNotEmpty() || text.isEmpty())) first = html to links
    val page = remember(note.id, template, dark, readOnly, first) {
        val f = first ?: ("" to emptyList())
        ReaderPage.build(template, dark, note.base, note.space, readOnly, f.second, f.first)
    }

    var pageLoaded by remember(note.id) { mutableStateOf(false) }
    var loadedPage by remember(note.id) { mutableStateOf<String?>(null) }
    var pushedSeq by remember(note.id) { mutableStateOf(0) }
    var repush by remember(note.id) { mutableStateOf(0) }

    // The scroll mapping with the editor: the fraction the editor left
    // goes back on this mount once the page has its body, and the
    // person's own scrolling is reported as a fraction so the editor
    // can open about where the reading was.
    var scrollRestored by remember(note.id) { mutableStateOf(restoreFraction <= 0f) }
    var lastReportY by remember(note.id) { mutableStateOf(0) }
    val reportScroll: (android.webkit.WebView) -> Unit = { wv ->
        wv.evaluateJavascript(scrollFractionJs()) { v ->
            v?.toFloatOrNull()?.let(onScrollFraction)
        }
    }

    // Images whose upload the offline queue still holds answer with a
    // placeholder; when the queue drains, one repush re-renders the
    // body so the real ones load.
    var pendingWas by remember(note.id) { mutableStateOf(false) }
    LaunchedEffect(note.id) {
        repo.pendingUploadPaths.collect { paths ->
            fetcher.pending = paths
            val pendingNow = paths.isNotEmpty()
            if (pendingWas && !pendingNow) repush++
            pendingWas = pendingNow
        }
    }

    // A tasks row's line: scroll its checkbox into view once the body
    // that holds it is on the page, trying until the render lands (the
    // first paints may still carry an empty body) and giving up after a
    // spell when the line is not there (it moved under the list).
    var pendingLine by remember(note.id) { mutableStateOf(atLine) }
    var web by remember(note.id) { mutableStateOf<WebView?>(null) }
    LaunchedEffect(note.id, pageLoaded, html, pendingLine) {
        var tries = 0
        while (pageLoaded && pendingLine >= 0 && tries < 20) {
            delay(250)
            tries++
            val wv = web ?: return@LaunchedEffect
            wv.evaluateJavascript(scrollToLineJs(pendingLine)) { found ->
                if (found == "true") pendingLine = -1
            }
        }
    }

    // An attachment opened from the note: a PDF renders here on the
    // content origin (the same sandboxed WebView an HTML note renders
    // in, through the minted asset-view URL); anything else downloads
    // to the cache and leaves for the system viewer.
    var pdfView by remember(note.id) { mutableStateOf<PdfView?>(null) }

    /**
     * Opens one `_assets/` file. PDFs mint the content-origin URL and
     * render in the viewer dialog, never in the app's origin; other
     * files download with this session's auth and hand the cache copy
     * to whatever app opens their type.
     */
    fun openAsset(path: String) {
        val name = path.substringAfterLast('/')
        if (name.endsWith(".pdf", ignoreCase = true)) {
            pdfView = PdfView(name)
            scope.launch {
                try {
                    pdfView = PdfView(name, repo.assetViewUrl(note.id, path))
                } catch (e: CancellationException) {
                    throw e
                } catch (e: retrofit2.HttpException) {
                    pdfView = null
                    onToast(
                        if (e.code() == 501) "This server runs without the content origin."
                        else e.userMessage(),
                    )
                } catch (e: Exception) {
                    pdfView = null
                    onToast("The viewer needs the server; ${e.userMessage()}")
                }
            }
            return
        }
        scope.launch {
            try {
                val dl = client.downloadAsset(path)
                val dir = File(context.cacheDir, "attachments").apply { mkdirs() }
                val file = File(dir, name)
                withContext(Dispatchers.IO) { file.writeBytes(dl.bytes) }
                val uri = FileProvider.getUriForFile(context, context.packageName + ".files", file)
                val view = Intent(Intent.ACTION_VIEW).apply {
                    setDataAndType(uri, mimeFor(name, dl.mime))
                    addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                }
                try {
                    context.startActivity(view)
                } catch (_: ActivityNotFoundException) {
                    onToast("No app opens $name.")
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                onToast(e.userMessage())
            }
        }
    }

    val onTap: (ReaderTap) -> Unit = { tap ->
        when (tap) {
            is ReaderTap.OpenNote -> onOpenNote(tap.id)
            is ReaderTap.CreateNote -> scope.launch {
                val id = repo.createNoteAt(tap.path)
                if (id != null) {
                    onOpenNote(id)
                } else {
                    onToast("Offline. The note is created on the next sync.")
                }
            }
            is ReaderTap.Tag -> onTag(tap.name)
            is ReaderTap.Asset -> openAsset(tap.path)
            is ReaderTap.Task -> scope.launch {
                when (val outcome = repo.tickTask(note.id, tap.line, tap.done)) {
                    is TickOutcome.Done -> Unit // the live document brings the tick back
                    is TickOutcome.Queued -> flippedBody = outcome.body
                    is TickOutcome.Refused -> {
                        onToast(outcome.message)
                        repush++ // the box was tapped on; put it back as the text reads
                    }
                }
            }
        }
    }

    // A WebView paints white until its page loads; the theme's
    // background instead, so opening a note never flashes.
    val background = MaterialTheme.colorScheme.background.toArgb()

    AndroidView(
        modifier = modifier,
        factory = { ctx ->
            WebView(ctx).apply {
                web = this
                setBackgroundColor(background)
                applyReaderSettings(this)
                setOnScrollChangeListener { _, _, y, _, oldY ->
                    // A drag reports often; a fraction per coarse step is plenty.
                    if (kotlin.math.abs(y - oldY) >= 40 || y == 0) {
                        if (kotlin.math.abs(y - lastReportY) >= 40 || y == 0) {
                            lastReportY = y
                            reportScroll(this)
                        }
                    }
                }
                webViewClient = ReaderWebViewClient(
                    assets = assetLoader(ctx),
                    fetcher = fetcher,
                    serverUrl = { client.session.value?.server },
                    openExternal = { uri -> openInBrowser(context, uri) },
                    onTap = onTap,
                    onPageLoaded = { pageLoaded = true },
                )
            }
        },
        update = { wv ->
            wv.setBackgroundColor(background)
            // Wait for the first render rather than load an empty page
            // and then load it again with the body.
            if (first == null) return@AndroidView
            if (loadedPage != page) {
                loadedPage = page
                pageLoaded = false
                pushedSeq = 0
                wv.loadDataWithBaseURL(readerBaseUrl(), page, "text/html", "utf-8", null)
            } else if (pageLoaded && pushedSeq <= repush) {
                val due = repush > 0 || html != first!!.first
                if (due) {
                    val arg = YanaJson.encodeToString(String.serializer(), html)
                    wv.evaluateJavascript("window.yanaReader && yanaReader.setBody($arg)", null)
                    pushedSeq = repush + 1
                }
            }
            // The editor's fraction goes back once the page it maps is
            // on screen; the page's own body swap keeps its scroll.
            if (pageLoaded && !scrollRestored) {
                scrollRestored = true
                wv.evaluateJavascript(scrollToFractionJs(restoreFraction), null)
            }
        },
        onRelease = { it.destroy() },
    )

    pdfView?.let { view -> AssetViewer(view, onClose = { pdfView = null }) }
}

/** A PDF being viewed: its name and the minted URL, once there is one. */
data class PdfView(val name: String, val url: String? = null)

/**
 * The MIME type an `ACTION_VIEW` gets for one attachment: the server's
 * answer, unless it is the generic octet stream and the file's own
 * extension names something better.
 */
internal fun mimeFor(name: String, served: String?): String {
    val guess = name.substringAfterLast('.', "").lowercase()
        .takeIf { it.isNotEmpty() }
        ?.let { android.webkit.MimeTypeMap.getSingleton().getMimeTypeFromExtension(it) }
    return when {
        served == null || served.isBlank() -> guess ?: "application/octet-stream"
        served == "application/octet-stream" && guess != null -> guess
        else -> served
    }
}

/**
 * The PDF viewer: a near-fullscreen dialog holding the same sandboxed
 * content-origin WebView an HTML note renders in, with the same
 * navigation restrictions — the minted URL is the only credential, and
 * links off the content origin leave for the system browser.
 */
@Composable
private fun AssetViewer(view: PdfView, onClose: () -> Unit) {
    Dialog(onDismissRequest = onClose, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Column(
            Modifier.fillMaxSize().padding(top = 40.dp),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Row(
                Modifier.fillMaxWidth().padding(horizontal = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Text(
                    view.name,
                    style = MaterialTheme.typography.titleSmall,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f).padding(start = 8.dp),
                )
                IconButton(onClick = onClose) { Icon(Icons.Default.Close, contentDescription = "Close") }
            }
            Box(Modifier.fillMaxWidth().weight(1f), contentAlignment = Alignment.Center) {
                val url = view.url
                if (url != null) {
                    NoteWebView(url, Modifier.fillMaxSize())
                } else {
                    CircularProgressIndicator(Modifier.size(28.dp))
                }
            }
        }
    }
}

/**
 * The script that scrolls one body line's checkbox into view — the same
 * selector the web's own jump-to-line uses — and says whether the line
 * was on the page yet.
 */
internal fun scrollToLineJs(line: Int): String =
    "(function(){var b=document.querySelector('input[type=checkbox][data-line=\"$line\"]');" +
        "if(!b)return false;var li=b.closest('li');if(li)li.classList.add('task-hit');" +
        "b.scrollIntoView({block:'center'});return true})()"

/** The page's scroll as a fraction of how far it can go, for the editor's hand-off. */
internal fun scrollFractionJs(): String =
    "(function(){var m=Math.max(1,document.documentElement.scrollHeight-window.innerHeight);" +
        "return m>0?window.pageYOffset/m:0})()"

/** Scrolls the page to a fraction of its scrollable height. */
internal fun scrollToFractionJs(fraction: Float): String =
    "(function(){var m=Math.max(1,document.documentElement.scrollHeight-window.innerHeight);" +
        "window.scrollTo(0,Math.round(m*$fraction));return true})()"
