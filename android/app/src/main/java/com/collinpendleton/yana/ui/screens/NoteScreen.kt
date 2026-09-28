package com.collinpendleton.yana.ui.screens

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.clickable
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Edit
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.launch
import com.collinpendleton.yana.ui.ShellInsets
import com.collinpendleton.yana.data.Backlink
import com.collinpendleton.yana.data.LinkTargets
import com.collinpendleton.yana.data.Note
import com.collinpendleton.yana.data.NoteMoveOutcome
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.findHeading
import com.collinpendleton.yana.data.markdownBody
import com.collinpendleton.yana.data.resolveTitle
import com.collinpendleton.yana.data.rt.SyncEngine
import com.collinpendleton.yana.data.toSwitcherNote
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.capture.CapturePerf
import com.collinpendleton.yana.ui.ConnectionDot
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.ShellState
import com.collinpendleton.yana.ui.YanaIcons
import com.collinpendleton.yana.ui.formatTime
import com.collinpendleton.yana.ui.editor.EditorLookup
import com.collinpendleton.yana.ui.editor.Format
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
    val vm: Loader<Note> = viewModel(key = "note:$id") {
        // The cached body paints the note at once; the fetch replaces it.
        Loader(fetch = { repo.note(id) }, cached = { repo.cachedNote(id) })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val note = state.data
    LaunchedEffect(note?.id, note?.title) {
        val n = note ?: return@LaunchedEffect
        app.prefs.touchRecent(n.id, n.title.ifEmpty { n.path.substringAfterLast('/').substringBeforeLast('.') })
    }

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

    // What the editor's completions draw on: the notes of this note's
    // space, as the switcher orders them, with the wikilink target
    // that resolves to each, and every tag in use.
    var lookup by remember(id) { mutableStateOf<EditorLookup?>(null) }
    LaunchedEffect(id, note?.space, note?.updatedAt) {
        val sp = note?.space ?: return@LaunchedEffect
        val metas = runCatching { repo.allNotes() }.getOrDefault(emptyList())
        val (bySpace, tags) = LinkTargets.build(metas)
        val targets = bySpace[sp].orEmpty().associateBy { it.id }
        lookup = EditorLookup(
            notes = metas.filter { it.space == sp }.map { it.toSwitcherNote() },
            recents = app.prefs.recents.value.map { it.id }.toSet(),
            targets = targets,
            tags = tags,
        )
    }

    // The scroll each mode leaves, as a fraction of how far it can go:
    // Edit opens about where the reading was, and Done resumes about
    // where the editing was — the same hand-off the web's tab scroll
    // gives its two views.
    var readFraction by rememberSaveable(id) { mutableStateOf(0f) }
    var editFraction by rememberSaveable(id) { mutableStateOf(0f) }

    // The title a rename left showing, until the note's own catches up.
    var renamedTitle by rememberSaveable(id) { mutableStateOf<String?>(null) }
    if (note?.title != null && renamedTitle != null && note.title == renamedTitle) renamedTitle = null

    // The editor holds the whole screen: the bottom bar steps out of
    // the way while it is up, the way the web's phone bar does.
    LaunchedEffect(editing) { ShellState.editing.value = editing }
    DisposableEffect(id) {
        onDispose { ShellState.editing.value = false }
    }

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
    val scope = rememberCoroutineScope()

    /**
     * Rename by the title, the web's commitTitle: the H1 in the
     * document follows as one edit, then the file moves to the path
     * the title spells — the server rewrites the links — with the
     * offline move joining the queue when it cannot go out now.
     */
    fun commitTitle(raw: String) {
        val n = note ?: return
        val res = resolveTitle(n.path, n.title, raw)
        if (res.title.isEmpty() || (res.title == n.title && res.path == n.path)) return
        renamedTitle = res.title
        scope.launch {
            if (res.title != n.title && live != null && liveReady) {
                liveText?.let { t ->
                    findHeading(t)?.let { (from, len) ->
                        sync.editOps(id, Format.opsJson(listOf(Format.Op(from, len, res.title))))
                    }
                }
            }
            if (res.path == n.path) {
                vm.reload(pull = true)
                return@launch
            }
            runCatching { repo.moveNote(id, res.path) }
                .onSuccess { outcome ->
                    if (outcome is NoteMoveOutcome.Done) {
                        vm.reload(pull = true)
                    } else {
                        toast("Offline. The rename moves when the connection returns.")
                        vm.reload(pull = true)
                    }
                }
                .onFailure { e ->
                    renamedTitle = null
                    toast(if (e is retrofit2.HttpException) e.userMessage() else "Could not rename the note.")
                }
        }
    }

    // The note's menu: pinning and the details sheet.
    val pins by app.prefs.pins.collectAsStateWithLifecycle()
    val pinned = pins.any { it.id == id }
    var menuOpen by remember { mutableStateOf(false) }
    var detailsOpen by remember { mutableStateOf(false) }

    Scaffold(
        contentWindowInsets = ShellInsets,
        topBar = {
            TopAppBar(
                title = {
                    val shown = note?.title?.ifEmpty { null } ?: title
                    if (canEdit && note != null) {
                        TitleField(
                            key = id,
                            current = renamedTitle ?: shown,
                            onCommit = ::commitTitle,
                            modifier = Modifier.fillMaxWidth(),
                        )
                    } else {
                        Text(shown, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                actions = {
                    IconButton(onClick = { onHistory(id, note?.title?.ifEmpty { null } ?: title) }) {
                        Icon(YanaIcons.History, contentDescription = "History")
                    }
                    IconButton(onClick = { menuOpen = true }) { Icon(Icons.Default.MoreVert, contentDescription = "Note menu") }
                    DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                        DropdownMenuItem(
                            text = { Text(if (pinned) "Unpin" else "Pin to the top") },
                            onClick = {
                                menuOpen = false
                                app.prefs.togglePin(id, note?.title?.ifEmpty { null } ?: title)
                            },
                        )
                        DropdownMenuItem(
                            text = { Text("Details") },
                            onClick = {
                                menuOpen = false
                                detailsOpen = true
                            },
                        )
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
                    notePath = note.path,
                    repo = repo,
                    images = app.images,
                    workScope = app.appScope,
                    lookup = lookup,
                    atEnd = startEditing,
                    initialFraction = readFraction,
                    onScrollFraction = { editFraction = it },
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
                            restoreFraction = editFraction,
                            onScrollFraction = { readFraction = it },
                            onOpenNote = onOpenNote,
                            onTag = onTag,
                            onToast = toast,
                            modifier = Modifier.fillMaxWidth().weight(1f),
                        )
                    }
                }
            }
        }
        if (detailsOpen && note != null) {
            DetailsSheet(
                repo = repo,
                note = note,
                onOpenNote = onOpenNote,
                onTag = onTag,
                onHistory = { onHistory(id, note.title.ifEmpty { title }) },
                onDismiss = { detailsOpen = false },
            )
        }
    }
}

/**
 * The details sheet: where the note lives, when it was made and
 * changed, its tags, the notes linking in, and the way to its history —
 * the web's details panel from the bottom.
 */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
private fun DetailsSheet(
    repo: NoteRepository,
    note: Note,
    onOpenNote: (String) -> Unit,
    onTag: (String) -> Unit,
    onHistory: () -> Unit,
    onDismiss: () -> Unit,
) {
    // Backlinks live in the server's index: they load when the sheet
    // opens and say so when the server will not answer.
    var backlinks by remember { mutableStateOf<List<Backlink>?>(null) }
    var backlinksFailed by remember { mutableStateOf(false) }
    LaunchedEffect(note.id) {
        backlinks = null
        backlinksFailed = false
        runCatching { repo.backlinks(note.id) }
            .onSuccess { backlinks = it }
            .onFailure { backlinksFailed = true }
    }

    ModalBottomSheet(onDismissRequest = onDismiss) {
        Column(
            Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Text("Details", style = MaterialTheme.typography.titleMedium)
            DetailLine("Path", if (note.space.isEmpty()) note.path else "${note.space}/${note.path}")
            DetailLine("Created", formatTime(note.created))
            DetailLine("Modified", formatTime(note.updatedAt))
            if (note.size > 0) DetailLine("Size", formatBytes(note.size))
            if (note.tags.isNotEmpty()) {
                Text("Tags", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                    note.tags.forEach { tag ->
                        Surface(
                            shape = MaterialTheme.shapes.small,
                            color = MaterialTheme.colorScheme.primaryContainer,
                            modifier = Modifier.clickable { onTag(tag) },
                        ) {
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
            Text("Linked from", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            when {
                backlinksFailed -> Text(
                    "Could not load the backlinks.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                backlinks == null -> Text(
                    "Loading the backlinks…",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                backlinks!!.isEmpty() -> Text(
                    "Nothing links here yet. Write [[${note.title.ifEmpty { note.path.substringAfterLast('/') }}]] in another note and it shows up.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                else -> backlinks!!.forEach { link ->
                    Column(
                        Modifier
                            .fillMaxWidth()
                            .clickable { link.note.id.takeIf { it.isNotEmpty() }?.let(onOpenNote) }
                            .padding(vertical = 4.dp),
                    ) {
                        Text(
                            link.note.title.ifEmpty { link.note.path.substringAfterLast('/') },
                            style = MaterialTheme.typography.bodyLarge,
                            color = MaterialTheme.colorScheme.primary,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        if (link.context.isNotEmpty()) {
                            Text(
                                link.context,
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                maxLines = 2,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                    }
                }
            }
            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            Row(
                Modifier.fillMaxWidth().clickable(onClick = onHistory).padding(vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(YanaIcons.History, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.width(12.dp))
                Text("History", style = MaterialTheme.typography.bodyLarge, modifier = Modifier.weight(1f))
                Icon(
                    Icons.AutoMirrored.Filled.KeyboardArrowRight,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.height(12.dp))
        }
    }
}

@Composable
private fun DetailLine(label: String, value: String) {
    if (value.isEmpty()) return
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        Text(
            label,
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.width(72.dp),
        )
        Text(value, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.weight(1f))
    }
}

/**
 * The title as the web's editable heading: a plain field in the app
 * bar, committed on Done or when the focus leaves it. A slash in what
 * is typed places the note, the same rule the web's title follows.
 */
@Composable
private fun TitleField(key: String, current: String, onCommit: (String) -> Unit, modifier: Modifier = Modifier) {
    var draft by remember(key) { mutableStateOf<String?>(null) }
    var focused by remember(key) { mutableStateOf(false) }
    val shown = draft ?: current
    fun settle() {
        val v = draft
        draft = null
        if (v != null && v.trim().isNotEmpty() && v != current) onCommit(v)
    }
    BasicTextField(
        value = shown,
        onValueChange = { draft = it },
        singleLine = true,
        textStyle = MaterialTheme.typography.titleLarge.copy(color = MaterialTheme.colorScheme.onSurface),
        cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
        keyboardActions = KeyboardActions(onDone = { settle() }),
        modifier = modifier
            .onFocusChanged { f ->
                if (focused && !f.isFocused) settle()
                focused = f.isFocused
            },
    )
}

/** A size in the web's words: B, KB, MB, GB, rounded to one decimal. */
internal fun formatBytes(bytes: Long): String {
    if (bytes < 1024) return "$bytes B"
    val kb = bytes / 1024.0
    if (kb < 1024) return String.format("%.1f KB", kb)
    val mb = kb / 1024.0
    if (mb < 1024) return String.format("%.1f MB", mb)
    return String.format("%.1f GB", mb / 1024.0)
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
