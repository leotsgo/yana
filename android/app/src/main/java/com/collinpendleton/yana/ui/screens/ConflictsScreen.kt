package com.collinpendleton.yana.ui.screens

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
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.collinpendleton.yana.data.ConflictEntry
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.YanaIcons
import com.collinpendleton.yana.ui.formatTime

/**
 * Every conflict copy in the account's spaces, the Data page's list:
 * what was parked where, when, and beside which note. A copy whose
 * original survives opens the resolve screen; one whose original is
 * gone is the plain note it now is. The tree keeps these out of the
 * ordinary rows — this is where they answer for themselves.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ConflictsScreen(
    repo: NoteRepository,
    onBack: () -> Unit,
    onResolve: (id: String, title: String) -> Unit = { _, _ -> },
    onOpenNote: (id: String, title: String) -> Unit = { _, _ -> },
) {
    val vm: Loader<List<ConflictEntry>> = viewModel { Loader(fetch = { repo.conflicts() }) }
    val state by vm.loaded.collectAsStateWithLifecycle()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Conflicts") },
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
                entries.isEmpty() -> Placeholder(loading = false, error = null, empty = "No conflicts waiting.", onRetry = { vm.reload() })
                else -> LazyColumn(Modifier.fillMaxSize()) {
                    item {
                        Text(
                            "Copies parked beside a note when two writes met the same path: a restore over a newer note, or a save over a file that changed underneath it.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
                        )
                    }
                    items(entries, key = { it.note.id }) { e ->
                        ConflictRow(e, onResolve = onResolve, onOpen = onOpenNote)
                        HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                    }
                    item { Spacer(Modifier.height(16.dp)) }
                }
            }
        }
    }
}

/** One waiting copy: where it sits, when it landed, and the note it belongs to. */
@Composable
private fun ConflictRow(
    entry: ConflictEntry,
    onResolve: (id: String, title: String) -> Unit,
    onOpen: (id: String, title: String) -> Unit,
) {
    val note = entry.note
    Row(
        Modifier.fillMaxWidth().clickable(enabled = entry.of != null) {
            entry.of?.let { onResolve(it.id, it.title) }
        }.padding(start = 20.dp, end = 12.dp, top = 12.dp, bottom = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            YanaIcons.Alert,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(18.dp),
        )
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(
                note.title.ifEmpty { note.path },
                style = MaterialTheme.typography.titleSmall,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                "${note.path} · parked ${conflictWhen(note.path, note.mtime)} · " +
                    (entry.of?.let { "beside ${it.title.ifEmpty { it.path }}" } ?: "its original is gone"),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (entry.of != null) {
            TextButton(onClick = { entry.of.let { onResolve(it.id, it.title) } }) { Text("Resolve") }
        } else {
            TextButton(onClick = { onOpen(note.id, note.title) }) { Text("Open") }
        }
    }
}
