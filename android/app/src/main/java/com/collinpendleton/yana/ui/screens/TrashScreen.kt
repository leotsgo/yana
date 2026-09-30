package com.collinpendleton.yana.ui.screens

import android.widget.Toast
import androidx.compose.foundation.clickable
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
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.data.DeletedNoteRow
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.YanaIcons
import com.collinpendleton.yana.ui.formatTime
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * The trash: every deleted note with something left to recover, its
 * deletion time and original path. Restore puts the file back where
 * it lived (or beside a newer occupant, marked as a conflict). Delete
 * forever and Empty trash are the only permanent destruction, and both
 * ask before they act. The deleted-notes list under Data covers what
 * the history alone remembers; the row at the end leads there.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TrashScreen(
    repo: NoteRepository,
    onBack: () -> Unit,
    onOpenNote: (id: String, title: String) -> Unit = { _, _ -> },
    onDeletedNotes: () -> Unit = {},
) {
    val vm: Loader<List<DeletedNoteRow>> = viewModel { Loader(fetch = { repo.trash() }) }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }
    var destroying by remember { mutableStateOf<DeletedNoteRow?>(null) }
    var restoring by remember { mutableStateOf<DeletedNoteRow?>(null) }
    var emptying by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    fun restore(entry: DeletedNoteRow) {
        if (busy || entry.id.isEmpty()) return
        busy = true
        scope.launch {
            try {
                val res = repo.restoreTrashNote(entry.id)
                vm.reload(pull = true)
                if (res.conflict) {
                    toast("A note now lives at ${entry.path}; restored beside it as ${res.path}.")
                } else {
                    toast("Restored ${res.path}.")
                }
                val note = res.note
                if (note != null && !res.deferred) onOpenNote(note.id, note.title)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                toast(e.userMessage())
            } finally {
                busy = false
            }
        }
    }

    fun destroy(entry: DeletedNoteRow) {
        if (busy) return
        busy = true
        scope.launch {
            try {
                repo.destroyTrashNote(entry.id)
                vm.reload(pull = true)
                toast("Destroyed.")
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                toast(e.userMessage())
            } finally {
                busy = false
            }
        }
    }

    fun empty() {
        if (busy) return
        busy = true
        scope.launch {
            try {
                val res = repo.emptyTrash()
                vm.reload(pull = true)
                toast("Destroyed ${res.destroyed} ${if (res.destroyed == 1) "entry" else "entries"}.")
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                toast(e.userMessage())
            } finally {
                busy = false
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Trash") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                actions = {
                    if (!state.data.isNullOrEmpty()) {
                        TextButton(enabled = !busy, onClick = { emptying = true }) { Text("Empty") }
                    }
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
            val entries = state.data
            when {
                entries == null -> Placeholder(loading = state.loading, error = state.error, empty = null, onRetry = { vm.reload() })
                entries.isEmpty() -> Placeholder(
                    loading = false,
                    error = state.error,
                    empty = "Nothing in it. Deleted notes sit here for 30 days, then go for good.",
                    onRetry = { vm.reload() },
                )
                else -> LazyColumn(Modifier.fillMaxSize()) {
                    item(key = "count") {
                        Text(
                            "${entries.size} ${if (entries.size == 1) "note" else "notes"}. Kept for 30 days; emptying is forever.",
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
                        )
                    }
                    items(entries, key = { (it.trashPath ?: "") + it.id + it.deletedAt }) { e ->
                        TrashRow(
                            entry = e,
                            busy = busy,
                            onRestore = { if (e.id.isNotEmpty()) restoring = e },
                            onDestroy = { if (e.id.isNotEmpty()) destroying = e },
                        )
                        HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                    }
                    state.error?.let { item { ErrorLine(it) } }
                    item(key = "deleted-notes") {
                        Row(
                            Modifier.fillMaxWidth().clickable(onClick = onDeletedNotes).padding(horizontal = 20.dp, vertical = 14.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Icon(
                                YanaIcons.History,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.size(18.dp),
                            )
                            Spacer(Modifier.width(12.dp))
                            Column(Modifier.weight(1f)) {
                                Text("Deleted notes", style = MaterialTheme.typography.bodyLarge)
                                Text(
                                    "What the history alone remembers, from before the trash held it.",
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                            Icon(
                                Icons.AutoMirrored.Filled.KeyboardArrowRight,
                                contentDescription = null,
                                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }
        }
    }

    // The restore ask, held until it is answered.
    restoring?.let { entry ->
        AlertDialog(
            onDismissRequest = { if (!busy) restoring = null },
            title = { Text("Restore ${entry.title.ifEmpty { entry.path }}?") },
            text = {
                Text(
                    if (entry.hasFile) {
                        "The note returns to its original path, or to a free name beside whatever now lives there."
                    } else {
                        "The note returns as it was last committed, to its original path or a free name beside whatever now lives there."
                    },
                )
            },
            confirmButton = {
                TextButton(
                    enabled = !busy,
                    onClick = {
                        restoring = null
                        restore(entry)
                    },
                ) { Text("Restore") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { restoring = null }) { Text("Cancel") } },
        )
    }

    destroying?.let { entry ->
        AlertDialog(
            onDismissRequest = { if (!busy) destroying = null },
            title = { Text("Delete ${entry.title.ifEmpty { entry.path }} forever?") },
            text = {
                Text("The file, the edit history, and everything recoverable about this note are destroyed. This cannot be undone.")
            },
            confirmButton = {
                TextButton(
                    enabled = !busy,
                    onClick = {
                        destroying = null
                        destroy(entry)
                    },
                ) { Text("Delete forever") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { destroying = null }) { Text("Cancel") } },
        )
    }

    if (emptying) {
        val entries = state.data.orEmpty()
        AlertDialog(
            onDismissRequest = { if (!busy) emptying = false },
            title = { Text("Empty the trash? ${entries.size} ${if (entries.size == 1) "entry is" else "entries are"} destroyed forever.") },
            text = {
                Column {
                    Text("Files, edit histories, everything recoverable. This is the only permanent deletion, and it cannot be undone.")
                    Spacer(Modifier.height(8.dp))
                    entries.take(8).forEach { e ->
                        Text(
                            e.title.ifEmpty { e.path },
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            },
            confirmButton = {
                TextButton(
                    enabled = !busy,
                    onClick = {
                        emptying = false
                        empty()
                    },
                ) { Text("Empty trash") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { emptying = false }) { Text("Cancel") } },
        )
    }
}

/** One deleted note: where it lived, when it went, and its two ways out. */
@Composable
private fun TrashRow(entry: DeletedNoteRow, busy: Boolean, onRestore: () -> Unit, onDestroy: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().padding(start = 20.dp, end = 12.dp, top = 12.dp, bottom = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            YanaIcons.FileText,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(18.dp),
        )
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(
                entry.title.ifEmpty { entry.path },
                style = MaterialTheme.typography.titleSmall,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                buildString {
                    append(entry.path)
                    append(" · deleted ${formatTime(entry.deletedAt)}")
                    if (!entry.hasFile) append(" · history only")
                },
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        TextButton(enabled = !busy, onClick = onRestore) { Text("Restore") }
        TextButton(enabled = !busy, onClick = onDestroy) { Text("Delete") }
    }
}
