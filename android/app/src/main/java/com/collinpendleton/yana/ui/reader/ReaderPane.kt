package com.collinpendleton.yana.ui.reader

import android.webkit.WebView
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.material3.MaterialTheme
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import com.collinpendleton.yana.data.GoRender
import com.collinpendleton.yana.data.Note
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.ResolvedLink
import com.collinpendleton.yana.data.TickOutcome
import com.collinpendleton.yana.data.YanaClient
import com.collinpendleton.yana.data.YanaJson
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
        },
        onRelease = { it.destroy() },
    )
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
