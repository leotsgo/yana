package com.collinpendleton.yana.ui.screens

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import android.widget.Toast
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.data.Note
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.markdownBody
import com.collinpendleton.yana.data.rt.SyncEngine
import com.collinpendleton.yana.capture.CapturePerf
import com.collinpendleton.yana.ui.ConnectionDot
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.YanaIcons
import com.collinpendleton.yana.ui.formatTime
import com.collinpendleton.yana.ui.editor.MarkdownEditor
import com.collinpendleton.yana.ui.htmlnote.HtmlNotePane
import com.collinpendleton.yana.ui.reader.ReaderPane
import com.collinpendleton.yana.ui.theme.ThemeMode
import com.collinpendleton.yana.yana

/**
 * A note: its title, where it lives, its tags, and its body — from the
 * server, or from the replica when the server is out of reach. A
 * markdown note reads rendered (the shared Go engine on the device, the
 * web's own rich runtime in a sandboxed WebView over app assets) and
 * joins the realtime document: the body is the CRDT's text once the
 * local state loads or the first handshake lands, the connection dot
 * beside the title says whether edits are waiting, settling, or live,
 * and the edit button opens the editor bound to that document. HTML
 * renders in a sandboxed WebView on the content origin, with its source
 * editable beside it (offline, the source reads as text until the
 * server returns).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NoteScreen(
    repo: NoteRepository,
    sync: SyncEngine,
    id: String,
    title: String,
    atLine: Int = -1,
    startEditing: Boolean = false,
    perfLabel: String? = null,
    onBack: () -> Unit,
    onOpenNote: (String) -> Unit = {},
    onTag: (String) -> Unit = {},
    onHistory: (id: String, title: String) -> Unit = { _, _ -> },
    onConflicts: (id: String, title: String) -> Unit = { _, _ -> },
) {
    val app = LocalContext.current.yana
    val vm: Loader<Note> = viewModel(key = "note:$id") { Loader(fetch = { repo.note(id) }) }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val note = state.data

    // Resolving a conflict settles the note behind this screen; coming
    // back from it refetches, so the banner keeps the list's count.
    var resolving by rememberSaveable(id) { mutableStateOf(false) }
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(id, lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME && resolving) {
                resolving = false
                vm.reload(pull = true)
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    // Only markdown notes have a CRDT document; HTML notes edit by
    // source and never join the relay.
    val isHtml = note?.kind == "html"
    val live = remember(id, isHtml) { if (isHtml) null else sync.open(id) }
    DisposableEffect(id, isHtml) {
        onDispose { live?.let { sync.close(id) } }
    }
    val liveText = live?.text?.collectAsStateWithLifecycle()?.value
    val liveReady = live?.ready?.collectAsStateWithLifecycle()?.value == true
    val status by sync.status.collectAsStateWithLifecycle()
    var editing by rememberSaveable(id) { mutableStateOf(startEditing) }

    // The capture timing log: this screen is the "first editable frame"
    // an entry point was measured against, once its editor is up on a
    // document this device holds.
    LaunchedEffect(editing, liveReady, perfLabel) {
        if (editing && liveReady && perfLabel != null) CapturePerf.done(perfLabel)
    }

    val canEdit = !isHtml && note?.role != "viewer"
    val mode by app.prefs.themeMode.collectAsStateWithLifecycle()
    val dark = when (mode) {
        ThemeMode.System -> isSystemInDarkTheme()
        ThemeMode.Light -> false
        ThemeMode.Dark -> true
    }
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(note?.title?.ifEmpty { null } ?: title, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                actions = {
                    IconButton(onClick = { onHistory(id, note?.title?.ifEmpty { null } ?: title) }) {
                        Icon(YanaIcons.History, contentDescription = "History")
                    }
                    if (canEdit && editing) {
                        IconButton(onClick = { editing = false }) {
                            Icon(Icons.Default.Check, contentDescription = "Done editing")
                        }
                    } else if (canEdit) {
                        IconButton(onClick = { editing = true }) {
                            Icon(Icons.Default.Edit, contentDescription = "Edit")
                        }
                    }
                    if (!isHtml) ConnectionDot(status, Modifier.padding(end = 20.dp))
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
            )
        },
    ) { pad ->
        PullToRefreshBox(
            isRefreshing = state.refreshing,
            onRefresh = { vm.reload(pull = true) },
            modifier = Modifier.padding(pad).fillMaxSize(),
        ) {
            if (note == null) {
                Placeholder(loading = state.loading, error = state.error, empty = null, onRetry = { vm.reload() })
            } else if (note.kind == "html") {
                // No outer scroll: the WebView and the source editor scroll themselves.
                Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally) {
                    Column(
                        Modifier.widthIn(max = 720.dp).fillMaxWidth().padding(horizontal = 20.dp, vertical = 8.dp),
                        verticalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        NoteHeader(note, Modifier.fillMaxWidth())
                        if (note.conflictCount > 0) {
                            ConflictBanner(note.conflictCount) {
                                resolving = true
                                onConflicts(id, note.title)
                            }
                        }
                    }
                    HtmlNotePane(
                        repo,
                        note,
                        Modifier.widthIn(max = 720.dp).fillMaxWidth().weight(1f),
                        onConflicts = {
                            resolving = true
                            onConflicts(id, note.title)
                        },
                    )
                }
            } else if (editing && live != null && liveReady) {
                MarkdownEditor(
                    sync = sync,
                    handle = live,
                    noteId = id,
                    modifier = Modifier.fillMaxSize(),
                )
            } else {
                Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally) {
                    Column(
                        Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp),
                    ) {
                        NoteHeader(note, Modifier.widthIn(max = 720.dp).fillMaxWidth())
                        if (note.conflictCount > 0) {
                            ConflictBanner(note.conflictCount, Modifier.widthIn(max = 720.dp).fillMaxWidth()) {
                                resolving = true
                                onConflicts(id, note.title)
                            }
                        }
                    }
                    // The reader scrolls itself; the body it renders is the
                    // live document's text once that loads, the cached file
                    // until then. A tasks row opens the note at its line.
                    val body = if (liveReady) liveText else note.markdown?.let(::markdownBody)
                    key(note.id, dark) {
                        ReaderPane(
                            repo = repo,
                            client = app.client,
                            note = note,
                            body = body,
                            dark = dark,
                            atLine = atLine,
                            onOpenNote = onOpenNote,
                            onTag = onTag,
                            onToast = toast,
                            modifier = Modifier.fillMaxWidth().weight(1f),
                        )
                    }
                }
            }
        }
    }
}

/** Where the note lives, when it changed, its tags: everything above the body. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun NoteHeader(note: Note, modifier: Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(note.path, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        val meta = listOfNotNull(
            formatTime(note.updatedAt).ifEmpty { null }?.let { "Edited $it" },
            note.role.takeIf { it == "viewer" }?.let { "view only" },
        ).joinToString(" · ")
        if (meta.isNotEmpty()) {
            Text(meta, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (note.tags.isNotEmpty()) {
            FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                note.tags.forEach { tag ->
                    Surface(shape = MaterialTheme.shapes.small, color = MaterialTheme.colorScheme.primaryContainer) {
                        Text(
                            "#$tag",
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onPrimaryContainer,
                            modifier = Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
                        )
                    }
                }
            }
        }
        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
    }
}

/**
 * The calm banner above a body with conflict copies waiting: what
 * happened, in the web's words, and the way in to settle it.
 */
@Composable
private fun ConflictBanner(count: Int, modifier: Modifier = Modifier, onOpen: () -> Unit) {
    Surface(
        shape = MaterialTheme.shapes.small,
        color = MaterialTheme.colorScheme.secondaryContainer,
        modifier = modifier.fillMaxWidth().clickable(onClick = onOpen),
    ) {
        Row(
            Modifier.padding(horizontal = 12.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Icon(
                YanaIcons.Alert,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSecondaryContainer,
                modifier = Modifier.size(16.dp),
            )
            Text(
                if (count == 1) "1 conflict copy waits — two writes met the same path"
                else "$count conflict copies wait — two writes met the same path",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSecondaryContainer,
            )
        }
    }
}
