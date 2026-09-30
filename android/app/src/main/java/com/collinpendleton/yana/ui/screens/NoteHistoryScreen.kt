package com.collinpendleton.yana.ui.screens

import android.widget.Toast
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
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
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
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
import com.collinpendleton.yana.data.HistoryEntry
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.YanaIcons
import com.collinpendleton.yana.ui.formatTime
import com.collinpendleton.yana.ui.history.DiffCounts
import com.collinpendleton.yana.ui.history.DiffView
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * A note's revisions from the server's git history: who changed it and
 * when, renames followed. A revision opens its diff — read as a
 * wrapped, tinted list at phone width — and Restore writes the old
 * text back as a live edit, so the open editor converges on the
 * restored text and the history shows who restored it. Until the
 * details sheet lands (12k) this is the history's own screen.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NoteHistoryScreen(
    repo: NoteRepository,
    id: String,
    title: String,
    onBack: () -> Unit,
) {
    val vm: Loader<List<HistoryEntry>> = viewModel(key = "history:$id") {
        Loader(fetch = { repo.noteHistory(id) })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val entries = state.data
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }

    /** The diff this revision opens: itself against the newest, or the newest against the one before it. */
    var diffFor by remember { mutableStateOf<Int?>(null) }
    var diff by remember { mutableStateOf<String?>(null) }
    var diffError by remember { mutableStateOf<String?>(null) }
    var diffRetry by remember { mutableStateOf(0) }
    var restoring by remember { mutableStateOf<HistoryEntry?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    // A diff closes back to the list before the screen itself goes.
    BackHandler(enabled = diffFor != null) { diffFor = null }

    LaunchedEffect(diffFor, entries, diffRetry) {
        val at = diffFor ?: return@LaunchedEffect
        val list = entries ?: return@LaunchedEffect
        if (at >= list.size) {
            diffFor = null
            return@LaunchedEffect
        }
        // Entries arrive newest first: an older revision diffs against
        // the newest (what changed since it stood), the newest against
        // the one before it (what it changed).
        val from = if (at == 0) list.getOrNull(1) ?: list[0] else list[at]
        val to = list[0]
        diff = null
        diffError = null
        try {
            diff = repo.noteHistoryDiff(id, from.hash, to.hash)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            diffError = e.userMessage()
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(if (diffFor == null) "History" else "Diff", maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = {
                    IconButton(onClick = { if (diffFor == null) onBack() else diffFor = null }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back")
                    }
                },
                actions = {
                    if (diffFor != null) (diff ?: "").takeIf { it.isNotBlank() }?.let { DiffCounts(it, Modifier.padding(end = 20.dp)) }
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
            )
        },
    ) { pad ->
        if (diffFor != null) {
            val shown = diff
            Column(Modifier.padding(pad).fillMaxSize()) {
                if (title.isNotEmpty()) {
                    Text(
                        title,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp),
                    )
                }
                when {
                    shown != null && shown.isNotBlank() -> DiffView(shown, Modifier.fillMaxSize())
                    shown != null -> Placeholder(loading = false, error = null, empty = "No changes between these revisions.", onRetry = {})
                    diffError != null -> Placeholder(loading = false, error = diffError, empty = null, onRetry = { diffRetry++ })
                    else -> Placeholder(loading = true, error = null, empty = null, onRetry = {})
                }
            }
        } else {
            PullToRefreshBox(
                isRefreshing = state.refreshing,
                onRefresh = { vm.reload(pull = true) },
                modifier = Modifier.padding(pad).fillMaxSize(),
            ) {
                if (entries == null) {
                    Placeholder(loading = state.loading, error = state.error, empty = null, onRetry = { vm.reload() })
                } else if (entries.isEmpty()) {
                    Placeholder(loading = false, error = null, empty = "No revisions yet. The tree is committed on a timer.", onRetry = { vm.reload() })
                } else {
                    LazyColumn(Modifier.fillMaxSize()) {
                        item {
                            Text(
                                "Each row is one revision. Tap one to read its diff.",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
                            )
                        }
                        itemsIndexed(entries, key = { _, e -> e.hash }) { i, e ->
                            RevisionRow(e, onOpen = { diffFor = i }, onRestore = { restoring = e })
                            HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                        }
                        state.error?.let { item { ErrorLine(it) } }
                        item { Spacer(Modifier.height(16.dp)) }
                    }
                }
            }
        }
    }

    restoring?.let { entry ->
        AlertDialog(
            onDismissRequest = { if (!busy) restoring = null },
            title = { Text("Restore the version from ${formatTime(entry.date)}?") },
            text = { Text("The current text becomes a new edit in the history. Open editors change to the restored text.") },
            confirmButton = {
                TextButton(
                    enabled = !busy,
                    onClick = {
                        busy = true
                        scope.launch {
                            try {
                                repo.restoreRevision(id, entry.hash, entry.path)
                                restoring = null
                                toast("Restored. The note reloads in a moment.")
                                vm.reload(pull = true)
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

/** One revision: who made it and when, its subject, and the restore beside it. */
@Composable
private fun RevisionRow(entry: HistoryEntry, onOpen: () -> Unit, onRestore: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onOpen).padding(start = 20.dp, end = 12.dp, top = 12.dp, bottom = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Surface(shape = MaterialTheme.shapes.small, color = MaterialTheme.colorScheme.surfaceContainerHigh) {
                    Icon(
                        authorIcon(entry.kind),
                        contentDescription = authorLabel(entry.kind),
                        tint = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.padding(4.dp).size(15.dp),
                    )
                }
                Text(
                    entry.name.ifEmpty { entry.email.ifEmpty { "someone" } },
                    style = MaterialTheme.typography.titleSmall,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Spacer(Modifier.width(4.dp))
                Text(
                    formatTime(entry.date),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            if (entry.subject.isNotEmpty()) {
                Text(
                    entry.subject,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        TextButton(onClick = onRestore) { Text("Restore") }
    }
}

/** Who is behind a revision: a person, an agent, or the files. */
internal fun authorIcon(kind: String) = when (kind) {
    "agent" -> YanaIcons.Code
    "filesystem" -> YanaIcons.Folder
    else -> YanaIcons.User
}

internal fun authorLabel(kind: String) = when (kind) {
    "agent" -> "An agent made this revision"
    "filesystem" -> "This revision arrived on the files"
    else -> "A person made this revision"
}
