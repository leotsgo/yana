package com.collinpendleton.yana.ui.screens

import android.widget.Toast
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.background
import androidx.compose.foundation.shape.CircleShape
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.Prefs
import com.collinpendleton.yana.R
import com.collinpendleton.yana.data.CaptureNotes
import com.collinpendleton.yana.data.NewNoteKit
import com.collinpendleton.yana.data.NewNoteKit.baseOf
import com.collinpendleton.yana.data.NewNoteKit.dirOf
import com.collinpendleton.yana.data.NoteMoveOutcome
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.TreeNode
import com.collinpendleton.yana.data.TreeRow
import com.collinpendleton.yana.data.noteCount
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.data.visibleRows
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.ShellInsets
import com.collinpendleton.yana.ui.YanaIcons
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** What a long press opened the sheet on. */
private sealed interface Target {
    data class NoteTarget(val id: String, val path: String, val title: String) : Target

    data class DirTarget(val path: String, val name: String, val inside: Int) : Target

    data class SpaceTarget(val name: String) : Target
}

/** The dialogue an action opened, one at a time. */
private sealed interface Act {
    data class Sheet(val target: Target) : Act

    data class NewFolder(val parent: String) : Act

    data class RenameNote(val id: String, val path: String, val title: String) : Act

    data class RenameFolder(val path: String, val name: String) : Act

    data class MoveNote(val id: String, val dir: String, val file: String, val title: String) : Act

    data class MoveFolder(val path: String, val name: String) : Act

    data class DeleteNote(val id: String, val title: String) : Act

    data class DeleteFolder(val path: String, val name: String, val inside: Int) : Act
}

/**
 * The tree: every space the account holds, each collapsible, its
 * folders opening in place and its notes under them — the web's tree
 * drawer on a phone. Folders sit closed until they are opened; the
 * opened folders and the collapsed spaces are remembered, per device,
 * the moment they change, so a kill and relaunch shows the same shape.
 * [focus] names the space a tap on home arrived for: it starts open and
 * in view.
 *
 * The web's tree actions live here too: a plus on a space or folder row
 * makes an untitled note there, and a long press opens the row's
 * actions — new note, new folder, rename and move by picker, delete to
 * the trash with a confirm. Creating and moving a note work offline
 * through the queue; folders and deletes are writes the server must
 * see.
 */
@OptIn(ExperimentalMaterial3Api::class, ExperimentalFoundationApi::class)
@Composable
fun SpaceScreen(
    repo: NoteRepository,
    prefs: Prefs,
    capture: CaptureNotes,
    focus: String,
    label: String,
    onBack: () -> Unit,
    onNote: (id: String, title: String) -> Unit,
    onNewNote: (id: String, title: String) -> Unit = { id, title -> onNote(id, title) },
    onActivity: () -> Unit = {},
    onSwitcher: () -> Unit = {},
) {
    // The replica answers if the network cannot; pull-to-refresh syncs.
    val vm: Loader<List<Space>> = viewModel(key = "spaces-tree") {
        Loader(fetch = { repo.spaces() }, refetch = { repo.sync(); repo.spaces() })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val spaces = state.data
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val recents by prefs.recentFolders.collectAsStateWithLifecycle()

    // A space the tree was opened for starts open; from then on the
    // closed set is remembered.
    var closed by remember { mutableStateOf(prefs.closedSpaces() - focus) }
    val trees = remember { mutableStateMapOf<String, List<TreeNode>>() }
    val folders = remember { mutableStateMapOf<String, Set<String>>() }
    var treeError by remember { mutableStateOf<String?>(null) }
    val open = spaces.orEmpty().filter { it.name !in closed }
    val list = rememberLazyListState()
    var act by remember { mutableStateOf<Act?>(null) }
    var busy by remember { mutableStateOf(false) }
    var refreshing by remember { mutableStateOf(false) }
    // The folder pool, loaded when a move picker first needs it.
    var dirs by remember { mutableStateOf<List<String>?>(null) }

    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }
    val noSpace = stringResource(R.string.capture_no_space)

    // The trees of the open spaces, loaded once each; the replica
    // answers offline.
    LaunchedEffect(open.joinToString("\u0000") { it.name }) {
        for (s in open) {
            if (!trees.containsKey(s.name)) {
                runCatching { repo.tree(s.name) }
                    .onSuccess { trees[s.name] = it }
                    .onFailure { if (treeError == null) treeError = it.message ?: "Could not load the tree." }
            }
        }
    }

    // Coming back to the tree — a note made from the picker, a change
    // on another device — rereads what the replica holds now.
    val lifecycleOwner = LocalLifecycleOwner.current
    DisposableEffect(lifecycleOwner) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) {
                scope.launch {
                    for (s in open) {
                        runCatching { repo.tree(s.name) }.onSuccess { trees[s.name] = it }
                    }
                }
            }
        }
        lifecycleOwner.lifecycle.addObserver(observer)
        onDispose { lifecycleOwner.lifecycle.removeObserver(observer) }
    }

    fun rowsOf(space: Space): List<TreeRow> {
        val nodes = trees[space.name] ?: return emptyList()
        val expanded = folders[space.name] ?: prefs.openFolders(space.name)
        return visibleRows(nodes, expanded)
    }

    // A focused space lands in view once its rows exist.
    LaunchedEffect(focus, spaces?.size, trees.size) {
        if (focus.isEmpty()) return@LaunchedEffect
        val names = spaces.orEmpty()
        val idx = names.indexOfFirst { it.name == focus }
        if (idx >= 0) {
            val header = (0 until idx).sumOf { i ->
                val s = names[i]
                1 + if (s.name !in closed) maxOf(1, rowsOf(s).size) else 0
            }
            runCatching { list.scrollToItem(header) }
        }
    }

    fun setSpaceOpen(name: String, openNow: Boolean) {
        closed = if (openNow) closed - name else closed + name
        prefs.setClosedSpaces(closed)
    }

    fun toggleFolder(space: Space, row: TreeRow) {
        val n = row.node
        val current = folders.getOrPut(space.name) { prefs.openFolders(space.name) }
        val next = if (row.expanded) current - n.path else current + n.path
        folders[space.name] = next
        prefs.setOpenFolders(space.name, next)
    }

    /** Syncs, then rereads the trees and the spaces — what a change here owes the screen. */
    fun refresh() {
        refreshing = true
        scope.launch {
            runCatching { repo.sync() }
            trees.clear()
            for (s in spaces.orEmpty().filter { it.name !in closed }) {
                runCatching { repo.tree(s.name) }.onSuccess { trees[s.name] = it }
            }
            vm.reload()
            refreshing = false
        }
    }

    fun newNoteAt(dir: String) {
        busy = true
        scope.launch {
            // The loose-root space's plus follows the capture space, the
            // web's defaultDir; a named folder is itself.
            val where = dir.ifEmpty { runCatching { capture.captureSpace() }.getOrDefault("") }
            val note = where.takeIf { it.isNotEmpty() }?.let { runCatching { capture.newNoteIn(it, null) }.getOrNull() }
            busy = false
            if (note == null) {
                toast(noSpace)
            } else {
                onNewNote(note.id, note.title)
            }
        }
    }

    fun moveNoteTo(id: String, from: String, to: String) {
        busy = true
        scope.launch {
            try {
                val outcome = repo.moveNote(id, to)
                when (outcome) {
                    is NoteMoveOutcome.Done -> {
                        val rewritten = outcome.result.rewritten
                        toast(
                            if (rewritten > 0) "Moved to $to. $rewritten link${if (rewritten == 1) "" else "s"} updated."
                            else "Moved to $to.",
                        )
                    }
                    NoteMoveOutcome.Queued -> toast("Offline. $from moves when the connection returns.")
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                toast(e.userMessage())
            } finally {
                busy = false
                refresh()
            }
        }
    }

    fun moveFolderTo(path: String, name: String, dir: String) {
        val to = if (dir.isEmpty()) name else "$dir/$name"
        busy = true
        scope.launch {
            try {
                val res = repo.moveFolder(path, to)
                val what = "${res.moved} note${if (res.moved == 1) "" else "s"}"
                toast(
                    if (res.rewritten > 0) "Moved $what to ${res.path}/. ${res.rewritten} link${if (res.rewritten == 1) "" else "s"} updated."
                    else "Moved $what to ${res.path}/.",
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                toast(e.userMessage())
            } finally {
                busy = false
                refresh()
            }
        }
    }

    fun createFolderAt(parent: String, name: String) {
        val clean = NewNoteKit.dirName(name.trim())
        if (clean.isEmpty()) return
        val path = if (parent.isEmpty()) clean else "$parent/$clean"
        busy = true
        scope.launch {
            try {
                repo.createFolder(path)
                toast("Made $path/.")
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                toast(e.userMessage())
            } finally {
                busy = false
                refresh()
            }
        }
    }

    fun deleteNote(id: String, title: String) {
        busy = true
        scope.launch {
            try {
                repo.deleteNote(id)
                prefs.forgetNote(id)
                toast("Deleted ${title.ifEmpty { "the note" }}. It is in the trash.")
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                toast(e.userMessage())
            } finally {
                busy = false
                refresh()
            }
        }
    }

    fun deleteFolder(path: String, name: String) {
        busy = true
        scope.launch {
            try {
                val res = repo.deleteFolder(path)
                toast(
                    if (res.deleted > 0) "Deleted $name/. ${res.deleted} note${if (res.deleted == 1) " is" else "s are"} in the trash."
                    else "Deleted $name/.",
                )
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                toast(e.userMessage())
            } finally {
                busy = false
                refresh()
            }
        }
    }

    fun loadDirs() {
        if (dirs == null) {
            scope.launch { dirs = runCatching { repo.folderList() }.getOrDefault(emptyList()) }
        }
    }

    Scaffold(
        contentWindowInsets = ShellInsets,
        topBar = {
            TopAppBar(
                title = { Text(label.ifEmpty { "Notes" }, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                actions = {
                    IconButton(onClick = onSwitcher) { Icon(YanaIcons.Command, contentDescription = "Switcher") }
                    IconButton(onClick = onActivity) { Icon(YanaIcons.History, contentDescription = "What changed") }
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
            )
        },
    ) { pad ->
        PullToRefreshBox(
            isRefreshing = refreshing || state.refreshing,
            onRefresh = { refresh() },
            modifier = Modifier.padding(pad).fillMaxSize(),
        ) {
            if (spaces.isNullOrEmpty()) {
                Placeholder(
                    loading = state.loading,
                    error = state.error,
                    empty = "No notes yet. Make one, or drop a markdown file into the notes directory; it shows up on the next scan.",
                    onRetry = { vm.reload() },
                )
            } else {
                LazyColumn(Modifier.fillMaxSize(), state = list) {
                    spaces.forEach { space ->
                        val isOpen = space.name !in closed
                        item(key = "space:" + space.name) {
                            SpaceHeader(space, isOpen, onOpen = { setSpaceOpen(space.name, !isOpen) }, onNew = { newNoteAt(space.name) }) {
                                act = Act.Sheet(Target.SpaceTarget(space.name))
                            }
                        }
                        if (isOpen) {
                            val nodes = trees[space.name]
                            when {
                                nodes == null -> item(key = "space-loading:" + space.name) { PendingRow() }
                                nodes.isEmpty() -> item(key = "space-empty:" + space.name) {
                                    Text(
                                        "Nothing here yet.",
                                        style = MaterialTheme.typography.bodyMedium,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        modifier = Modifier.padding(start = 32.dp, top = 4.dp, bottom = 12.dp),
                                    )
                                }
                                else -> items(rowsOf(space), key = { space.name + "\u0000" + it.node.path }) { row ->
                                    TreeRowItem(
                                        row,
                                        depth = row.depth + 1,
                                        onClick = {
                                            if (row.node.isDir) toggleFolder(space, row)
                                            else row.node.id?.let { onNote(it, row.node.label) }
                                        },
                                        onLong = {
                                            val n = row.node
                                            if (n.isDir) {
                                                act = Act.Sheet(Target.DirTarget(n.path, n.name, noteCount(n)))
                                            } else if (n.id != null) {
                                                act = Act.Sheet(Target.NoteTarget(n.id!!, n.path, n.label))
                                            }
                                        },
                                        onNew = if (row.node.isDir) {
                                            { newNoteAt(row.node.path) }
                                        } else {
                                            null
                                        },
                                    )
                                }
                            }
                        }
                    }
                    treeError?.let { item { ErrorLine(it) } }
                    state.error?.let { item { ErrorLine(it) } }
                }
            }
        }
    }

    act?.let { current ->
        when (current) {
            is Act.Sheet -> ActionSheet(
                current.target,
                onOpen = { id, title -> act = null; onNote(id, title) },
                onNewNote = { dir -> act = null; newNoteAt(dir) },
                onNewFolder = { parent -> act = Act.NewFolder(parent) },
                onRenameNote = { id, path, title -> act = Act.RenameNote(id, path, title) },
                onRenameFolder = { path, name -> act = Act.RenameFolder(path, name) },
                onMoveNote = { id, dir, file, title -> loadDirs(); act = Act.MoveNote(id, dir, file, title) },
                onMoveFolder = { path, name -> loadDirs(); act = Act.MoveFolder(path, name) },
                onDeleteNote = { id, title -> act = Act.DeleteNote(id, title) },
                onDeleteFolder = { path, name, inside -> act = Act.DeleteFolder(path, name, inside) },
                onDismiss = { act = null },
            )

            is Act.NewFolder -> NameDialog(
                title = "New folder",
                label = "A folder inside ${current.parent}",
                initial = "",
                busy = busy,
                onDismiss = { if (!busy) act = null },
                onSubmit = { name ->
                    act = null
                    createFolderAt(current.parent, name)
                },
            )

            is Act.RenameNote -> {
                val nameStart = current.path.length - baseOf(current.path).length
                val dot = baseOf(current.path).let { b -> if (b.lastIndexOf('.') > 0) b.lastIndexOf('.') else b.length }
                NameDialog(
                    title = "Rename or move by path",
                    label = "Moving the file rewrites wikilinks that point at it.",
                    initial = current.path,
                    select = TextRange(nameStart, nameStart + dot),
                    busy = busy,
                    onDismiss = { if (!busy) act = null },
                    onSubmit = { path ->
                        if (path != current.path) {
                            act = null
                            moveNoteTo(current.id, current.path, path)
                        }
                    },
                )
            }

            is Act.RenameFolder -> NameDialog(
                title = "Rename ${current.name}/",
                label = "Every note inside moves with it; wikilinks that point at them are rewritten.",
                initial = current.name,
                busy = busy,
                onDismiss = { if (!busy) act = null },
                onSubmit = { name ->
                    val clean = NewNoteKit.dirName(name.trim())
                    if (clean.isNotEmpty() && clean != current.name) {
                        act = null
                        val parent = dirOf(current.path)
                        moveFolderTo(current.path, clean, parent)
                    }
                },
            )

            is Act.MoveNote -> dirs?.let { pool ->
                FolderPickerDialog(
                    title = "Move ${current.title.ifEmpty { current.file }} to",
                    here = current.dir,
                    dirs = pool,
                    spaces = spaces.orEmpty().map { it.name },
                    recents = recents,
                    exclude = { it == current.dir },
                    createFolder = { path ->
                        try {
                            repo.createFolder(path)
                            true
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: Exception) {
                            toast(e.userMessage())
                            false
                        }
                    },
                    onPick = { dir ->
                        act = null
                        val to = if (dir.isEmpty()) current.file else "$dir/${current.file}"
                        moveNoteTo(current.id, current.path(), to)
                    },
                    onDismiss = { act = null },
                )
            }

            is Act.MoveFolder -> dirs?.let { pool ->
                FolderPickerDialog(
                    title = "Move ${current.name}/ to",
                    here = dirOf(current.path),
                    dirs = pool,
                    spaces = spaces.orEmpty().map { it.name },
                    recents = recents,
                    exclude = { d -> d.isEmpty() || d == dirOf(current.path) || d == current.path || d.startsWith(current.path + "/") },
                    createFolder = { path ->
                        try {
                            repo.createFolder(path)
                            true
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: Exception) {
                            toast(e.userMessage())
                            false
                        }
                    },
                    onPick = { dir -> act = null; moveFolderTo(current.path, current.name, dir) },
                    onDismiss = { act = null },
                )
            }

            is Act.DeleteNote -> ConfirmDialog(
                title = "Delete ${current.title.ifEmpty { "this note" }}?",
                body = "The file moves to the trash and its links go unresolved. Restore it any time in the next 30 days.",
                confirm = "Delete",
                busy = busy,
                onDismiss = { if (!busy) act = null },
                onConfirm = {
                    act = null
                    deleteNote(current.id, current.title)
                },
            )

            is Act.DeleteFolder -> ConfirmDialog(
                title = if (current.inside > 0) {
                    "Delete ${current.name}/ and the ${current.inside} note${if (current.inside == 1) "" else "s"} inside?"
                } else {
                    "Delete the empty folder ${current.name}/?"
                },
                body = if (current.inside > 0) {
                    "Each note moves to the trash and can be restored for 30 days. Links pointing at them go unresolved until then."
                } else {
                    null
                },
                confirm = "Delete",
                busy = busy,
                onDismiss = { if (!busy) act = null },
                onConfirm = {
                    act = null
                    deleteFolder(current.path, current.name)
                },
            )
        }
    }
}

private fun Act.MoveNote.path(): String = if (dir.isEmpty()) file else "$dir/$file"

/** The long-press sheet: the row's actions, the web's context menu on a phone. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ActionSheet(
    target: Target,
    onOpen: (id: String, title: String) -> Unit,
    onNewNote: (dir: String) -> Unit,
    onNewFolder: (parent: String) -> Unit,
    onRenameNote: (id: String, path: String, title: String) -> Unit,
    onRenameFolder: (path: String, name: String) -> Unit,
    onMoveNote: (id: String, dir: String, file: String, title: String) -> Unit,
    onMoveFolder: (path: String, name: String) -> Unit,
    onDeleteNote: (id: String, title: String) -> Unit,
    onDeleteFolder: (path: String, name: String, inside: Int) -> Unit,
    onDismiss: () -> Unit,
) {
    ModalBottomSheet(onDismissRequest = onDismiss) {
        when (target) {
            is Target.NoteTarget -> {
                SheetTitle(target.title, target.path)
                SheetRow("Open") { onOpen(target.id, target.title) }
                SheetRow("Move to a folder") { onMoveNote(target.id, dirOf(target.path), baseOf(target.path), target.title) }
                SheetRow("Rename or move by path") { onRenameNote(target.id, target.path, target.title) }
                SheetRow("Delete", detail = "to the trash", danger = true) { onDeleteNote(target.id, target.title) }
            }
            is Target.DirTarget -> {
                SheetTitle(target.name + "/", target.path)
                SheetRow("New note here") { onNewNote(target.path) }
                SheetRow("New folder inside") { onNewFolder(target.path) }
                SheetRow("Rename") { onRenameFolder(target.path, target.name) }
                SheetRow("Move to a folder") { onMoveFolder(target.path, target.name) }
                SheetRow("Delete", detail = "notes to the trash", danger = true) { onDeleteFolder(target.path, target.name, target.inside) }
            }
            is Target.SpaceTarget -> {
                SheetTitle(if (target.name.isEmpty()) "/" else target.name + "/", null)
                SheetRow("New note here") { onNewNote(target.name) }
                if (target.name.isNotEmpty()) {
                    SheetRow("New folder") { onNewFolder(target.name) }
                }
            }
        }
        Spacer(Modifier.height(24.dp))
    }
}

@Composable
private fun SheetTitle(title: String, detail: String?) {
    Column(Modifier.padding(horizontal = 24.dp)) {
        Text(title, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (detail != null) {
            Text(
                detail,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Spacer(Modifier.height(4.dp))
    }
}

@Composable
private fun SheetRow(label: String, detail: String? = null, danger: Boolean = false, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 24.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            label,
            style = MaterialTheme.typography.bodyLarge,
            color = if (danger) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface,
            modifier = Modifier.weight(1f),
        )
        if (detail != null) {
            Text(detail, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

/** A name or path asked for: the field seeded, the named part selected. */
@Composable
private fun NameDialog(
    title: String,
    label: String,
    initial: String,
    select: TextRange? = null,
    busy: Boolean,
    onDismiss: () -> Unit,
    onSubmit: (value: String) -> Unit,
) {
    var value by remember {
        mutableStateOf(TextFieldValue(initial, selection = select ?: TextRange(initial.length)))
    }
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(title) },
        text = {
            Column {
                OutlinedTextField(
                    value = value,
                    onValueChange = { value = it },
                    singleLine = true,
                    enabled = !busy,
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    label,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        },
        confirmButton = {
            TextButton(enabled = !busy && value.text.isNotBlank(), onClick = { onSubmit(value.text.trim()) }) { Text("Save") }
        },
        dismissButton = { TextButton(enabled = !busy, onClick = onDismiss) { Text("Cancel") } },
    )
}

/** A dangerous action's ask: the body says what it does before it is done. */
@Composable
private fun ConfirmDialog(
    title: String,
    body: String?,
    confirm: String,
    busy: Boolean,
    onDismiss: () -> Unit,
    onConfirm: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(title) },
        text = { body?.let { Text(it) } },
        confirmButton = { TextButton(enabled = !busy, onClick = onConfirm) { Text(confirm) } },
        dismissButton = { TextButton(enabled = !busy, onClick = onDismiss) { Text("Cancel") } },
    )
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun SpaceHeader(space: Space, open: Boolean, onOpen: () -> Unit, onNew: () -> Unit, onHold: () -> Unit) {
    val turn by animateFloatAsState(if (open) 90f else 0f, label = "chevron")
    Row(
        Modifier.fillMaxWidth().combinedClickable(onClick = onOpen, onLongClick = onHold).padding(start = 8.dp, end = 8.dp, top = 14.dp, bottom = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(24.dp), contentAlignment = Alignment.Center) {
            Icon(
                Icons.AutoMirrored.Filled.KeyboardArrowRight,
                contentDescription = if (open) "Collapse" else "Expand",
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.rotate(turn),
            )
        }
        Spacer(Modifier.width(8.dp))
        Text(
            space.displayName,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Medium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        Text(
            notes(space.notes),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        IconButton(onClick = onNew, modifier = Modifier.size(32.dp)) {
            Icon(YanaIcons.Plus, contentDescription = "New note in ${space.displayName}", modifier = Modifier.size(18.dp))
        }
    }
}

@Composable
private fun PendingRow() {
    Text(
        "…",
        style = MaterialTheme.typography.bodyLarge,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 32.dp, top = 4.dp, bottom = 8.dp),
    )
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun TreeRowItem(
    row: TreeRow,
    depth: Int,
    onClick: () -> Unit,
    onLong: () -> Unit,
    onNew: (() -> Unit)?,
) {
    val n = row.node
    val turn by animateFloatAsState(if (row.expanded) 90f else 0f, label = "chevron")
    Row(
        Modifier
            .fillMaxWidth()
            .combinedClickable(onClick = onClick, onLongClick = onLong)
            .padding(start = 12.dp + 20.dp * depth, end = 8.dp, top = 11.dp, bottom = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(24.dp), contentAlignment = Alignment.Center) {
            if (n.isDir) {
                Icon(
                    Icons.AutoMirrored.Filled.KeyboardArrowRight,
                    contentDescription = if (row.expanded) "Collapse" else "Expand",
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.rotate(turn),
                )
            } else {
                Box(Modifier.size(5.dp).clip(CircleShape).background(MaterialTheme.colorScheme.outline))
            }
        }
        Spacer(Modifier.width(8.dp))
        Text(
            if (n.isDir) n.name + "/" else n.label,
            style = MaterialTheme.typography.bodyLarge,
            fontWeight = if (n.isDir) FontWeight.Medium else FontWeight.Normal,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (n.isDir) {
            Text(
                noteCount(n).toString(),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (onNew != null) {
                IconButton(onClick = onNew, modifier = Modifier.size(32.dp)) {
                    Icon(YanaIcons.Plus, contentDescription = "New note in ${n.name}/", modifier = Modifier.size(18.dp))
                }
            }
        }
    }
}
