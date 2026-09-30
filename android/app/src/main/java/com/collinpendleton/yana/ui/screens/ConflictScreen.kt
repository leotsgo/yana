package com.collinpendleton.yana.ui.screens

import android.widget.Toast
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.PrimaryTabRow
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.data.ConflictAction
import com.collinpendleton.yana.data.NoteMeta
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.YanaIcons
import com.collinpendleton.yana.ui.formatTime
import com.collinpendleton.yana.ui.history.DiffCounts
import com.collinpendleton.yana.ui.history.DiffLine
import com.collinpendleton.yana.ui.history.DiffLines
import com.collinpendleton.yana.ui.history.DiffParser
import java.time.Instant
import java.time.LocalDateTime
import java.time.ZoneOffset
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * One note's conflict copies: the diff of each against the note it was
 * parked beside, and the web's three ways out — keep this note (the
 * copy goes to the trash), keep the copy (its text becomes this note),
 * keep both (the copy is renamed to an ordinary note). Each choice
 * confirms first, lands as one commit, and the screen closes itself
 * when nothing is waiting anymore.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ConflictScreen(
    repo: NoteRepository,
    id: String,
    title: String,
    onBack: () -> Unit,
) {
    val vm: Loader<List<NoteMeta>> = viewModel(key = "conflicts:$id") {
        Loader(fetch = { repo.noteConflicts(id) })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val copies = state.data
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }

    var pick by rememberSaveable(id) { mutableIntStateOf(0) }
    var diff by remember { mutableStateOf<List<DiffLine>?>(null) }
    var diffError by remember { mutableStateOf<String?>(null) }
    var diffRetry by remember { mutableStateOf(0) }
    var asking by remember { mutableStateOf<ConflictAction?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    // The last resolution emptied the list: there is nothing left to
    // settle, so the screen goes the way the web's dialog closes.
    LaunchedEffect(copies) {
        if (copies != null && copies.isEmpty()) onBack()
    }

    val current = copies?.getOrNull(pick.coerceIn(0, (copies.size - 1).coerceAtLeast(0)))

    LaunchedEffect(current?.id, diffRetry) {
        val c = current ?: return@LaunchedEffect
        diff = null
        diffError = null
        try {
            val res = repo.conflictDiff(c.id)
            diff = DiffParser.parseHeaderless(res.diff)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            diffError = e.userMessage()
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(
                        when {
                            title.isNotEmpty() -> title
                            copies == null -> "Conflicts"
                            else -> "${copies.size} ${if (copies.size == 1) "conflict" else "conflicts"}"
                        },
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                actions = {
                    diff?.takeIf { it.isNotEmpty() }?.let { DiffCounts(it, Modifier.padding(end = 20.dp)) }
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
            )
        },
    ) { pad ->
        if (copies == null || current == null) {
            Column(Modifier.padding(pad).fillMaxSize()) {
                Placeholder(loading = state.loading, error = state.error, empty = "Nothing waiting.", onRetry = { vm.reload() })
            }
        } else {
            Column(Modifier.padding(pad).fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally) {
                if (copies.size > 1) {
                    PrimaryTabRow(selectedTabIndex = pick.coerceIn(0, copies.size - 1)) {
                        copies.forEachIndexed { i, c ->
                            Tab(
                                selected = i == pick.coerceIn(0, copies.size - 1),
                                onClick = { pick = i },
                                text = {
                                    Text(
                                        "${conflictWhen(c.path, c.mtime)} — ${c.title.ifEmpty { c.path }}",
                                        maxLines = 1,
                                        overflow = TextOverflow.Ellipsis,
                                    )
                                },
                            )
                        }
                    }
                }
                LazyColumn(Modifier.fillMaxWidth()) {
                    item {
                        Column(
                            Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 12.dp),
                            verticalArrangement = Arrangement.spacedBy(8.dp),
                        ) {
                            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                                Icon(
                                    YanaIcons.Alert,
                                    contentDescription = null,
                                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                                    modifier = Modifier.size(16.dp),
                                )
                                Text(
                                    "A conflict copy",
                                    style = MaterialTheme.typography.labelMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                            Text(
                                "${current.path} was parked beside this note. Keep this note, keep the copy, or keep both as an ordinary note.",
                                style = MaterialTheme.typography.bodyMedium,
                            )
                        }
                    }
                    val shown = diff
                    when {
                        shown != null && shown.isNotEmpty() -> item { DiffLines(shown, Modifier.fillMaxWidth()) }
                        shown != null -> item {
                            Placeholder(
                                loading = false,
                                error = null,
                                empty = "The two bodies are the same; only the names differ.",
                                onRetry = {},
                            )
                        }
                        diffError != null -> item {
                            Placeholder(loading = false, error = diffError, empty = null, onRetry = { diffRetry++ })
                        }
                        else -> item { Placeholder(loading = true, error = null, empty = null, onRetry = {}) }
                    }
                    item { Spacer(Modifier.size(16.dp)) }
                }
                Row(
                    Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 12.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    OutlinedButton(
                        enabled = !busy,
                        onClick = { asking = ConflictAction.Mine },
                        modifier = Modifier.weight(1f),
                    ) { Text("Keep mine", maxLines = 1) }
                    OutlinedButton(
                        enabled = !busy,
                        onClick = { asking = ConflictAction.Theirs },
                        modifier = Modifier.weight(1f),
                    ) { Text("Keep theirs", maxLines = 1) }
                    Button(
                        enabled = !busy,
                        onClick = { asking = ConflictAction.Both },
                        modifier = Modifier.weight(1f),
                    ) { Text("Keep both", maxLines = 1) }
                }
            }
        }
    }

    asking?.let { action ->
        val copy = current
        if (copy != null) {
            AlertDialog(
                onDismissRequest = { if (!busy) asking = null },
                title = { Text(confirmTitle(action)) },
                text = { Text(confirmBody(action)) },
                confirmButton = {
                    TextButton(
                        enabled = !busy,
                        onClick = {
                            busy = true
                            scope.launch {
                                try {
                                    val res = repo.resolveConflict(copy.id, action)
                                    asking = null
                                    val name = copy.title.ifEmpty { copy.path }
                                    toast(
                                        when (action) {
                                            ConflictAction.Mine -> "Kept this note. $name is in the trash."
                                            ConflictAction.Theirs -> "Kept the copy. Its text is this note now."
                                            ConflictAction.Both -> "Kept both. The copy is ${res.path ?: "renamed"} now."
                                        },
                                    )
                                    pick = 0
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
                    ) { Text(confirmTitle(action)) }
                },
                dismissButton = { TextButton(enabled = !busy, onClick = { asking = null }) { Text("Cancel") } },
            )
        }
    }
}

private fun confirmTitle(action: ConflictAction): String = when (action) {
    ConflictAction.Mine -> "Keep this note?"
    ConflictAction.Theirs -> "Keep the copy?"
    ConflictAction.Both -> "Keep both?"
}

private fun confirmBody(action: ConflictAction): String = when (action) {
    ConflictAction.Mine -> "The copy moves to the trash; this note stays as it is."
    ConflictAction.Theirs -> "The copy's text replaces this note; the copy moves to the trash."
    ConflictAction.Both -> "The copy is renamed to an ordinary note beside this one."
}

private val conflictStampRe = Regex("""\.conflict-(\d{8})[-T](\d{6})""")

/** When a copy was made, read from the timestamp in its name; the file's mtime when the name carries none. */
internal fun conflictWhen(path: String, mtime: String): String =
    conflictStamp(path)?.let { formatTime(it.toString()) } ?: formatTime(mtime)

/** The timestamp in a conflict copy's name as a UTC instant; null when there is none. */
internal fun conflictStamp(path: String): Instant? {
    val m = conflictStampRe.find(path) ?: return null
    val (d, t) = m.destructured
    return runCatching {
        LocalDateTime.of(
            d.substring(0, 4).toInt(),
            d.substring(4, 6).toInt(),
            d.substring(6, 8).toInt(),
            t.substring(0, 2).toInt(),
            t.substring(2, 4).toInt(),
            t.substring(4, 6).toInt(),
        ).toInstant(ZoneOffset.UTC)
    }.getOrNull()
}
