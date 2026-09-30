package com.collinpendleton.yana.ui.screens

import android.widget.Toast
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
 * Every note whose file is gone, with what a restore would recover it
 * from — the trash copy, the retained edits, or the history — and the
 * restore that brings it back to where it lived (or a free name beside
 * whatever took its path). The list lives under Settings → Data until
 * the data screen (12r) and the trash (12o) land and carry their own
 * doors to it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DeletedNotesScreen(
    repo: NoteRepository,
    onBack: () -> Unit,
    onOpenNote: (id: String, title: String) -> Unit = { _, _ -> },
) {
    val vm: Loader<List<DeletedNoteRow>> = viewModel { Loader(fetch = { repo.deletedNotes() }) }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }
    var restoring by remember { mutableStateOf<DeletedNoteRow?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Deleted notes") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
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
                entries.isEmpty() -> Placeholder(loading = false, error = null, empty = "Nothing deleted to bring back.", onRetry = { vm.reload() })
                else -> LazyColumn(Modifier.fillMaxSize()) {
                    items(entries, key = { it.id + it.deletedAt }) { e ->
                        DeletedRow(e) { restoring = e }
                        HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                    }
                    state.error?.let { item { ErrorLine(it) } }
                    item { Spacer(Modifier.height(16.dp)) }
                }
            }
        }
    }

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
                        busy = true
                        scope.launch {
                            try {
                                val res = repo.restoreDeleted(entry.id)
                                restoring = null
                                vm.reload(pull = true)
                                if (res.conflict) {
                                    toast("A note now lives at ${entry.path}; restored beside it as ${res.path}.")
                                } else {
                                    toast("Restored ${res.path} from ${if (res.from == "history") "the history" else "the trash"}.")
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
                    },
                ) { Text("Restore") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { restoring = null }) { Text("Cancel") } },
        )
    }
}

/** One deleted note: where it lived, when it went, what a restore recovers it from. */
@Composable
private fun DeletedRow(entry: DeletedNoteRow, onRestore: () -> Unit) {
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
                "${entry.path} · deleted ${formatTime(entry.deletedAt)} · from ${recoverSource(entry)}",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        TextButton(onClick = onRestore) { Text("Restore") }
    }
}

private fun recoverSource(e: DeletedNoteRow): String = when {
    e.hasFile -> "trash copy"
    e.hasSidecar -> "recent edits"
    else -> "the history"
}
