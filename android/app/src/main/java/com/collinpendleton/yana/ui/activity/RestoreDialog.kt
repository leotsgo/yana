package com.collinpendleton.yana.ui.activity

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.PitPreview
import com.collinpendleton.yana.data.RestoreSummary
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.YanaIcons
import com.collinpendleton.yana.ui.formatTime
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * The point-in-time restore: what restoring to a feed entry's commit
 * would do, listed exactly, before anything is touched. The preview
 * comes over the same endpoint the restore runs, so what the list says
 * is what the restore does; what stands now is committed and tagged
 * first, so the restore itself can be undone.
 */
@Composable
fun RestoreDialog(
    repo: NoteRepository,
    commit: String,
    space: String,
    onDone: (RestoreSummary) -> Unit,
    onClose: () -> Unit,
) {
    var preview by remember { mutableStateOf<PitPreview?>(null) }
    var error by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    LaunchedEffect(commit, space) {
        try {
            preview = repo.pitPreview(commit, space)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = e.userMessage()
        }
    }

    val tree = space.isEmpty()
    val title = if (tree) "Restore the tree to here" else "Restore $space to here"

    AlertDialog(
        onDismissRequest = { if (!busy) onClose() },
        title = { Text(title) },
        text = {
            Column {
                val p = preview
                when {
                    error != null -> Text(error!!, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
                    p == null -> Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        CircularProgressIndicator(Modifier.height(18.dp).width(18.dp))
                        Text("Reading what the restore would do…", style = MaterialTheme.typography.bodyMedium)
                    }
                    else -> {
                        Text(
                            buildString {
                                append(if (tree) "The tree" else "This space")
                                append(" returns to how it stood at ")
                                append(p.subject.ifEmpty { "that commit" })
                                if (p.author.isNotEmpty()) append(" — ${p.author}, ${formatTime(p.date)}")
                                else if (p.date.isNotEmpty()) append(", ${formatTime(p.date)}")
                                append(".")
                            },
                            style = MaterialTheme.typography.bodyMedium,
                        )
                        if (p.changes.isEmpty()) {
                            Spacer(Modifier.height(8.dp))
                            Text("Nothing changes: it already stands here.", style = MaterialTheme.typography.bodyMedium)
                        } else {
                            Spacer(Modifier.height(8.dp))
                            Text(countList(p), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            Spacer(Modifier.height(4.dp))
                            Column(
                                Modifier
                                    .heightIn(max = 280.dp)
                                    .verticalScroll(rememberScrollState()),
                            ) {
                                p.changes.forEach { c ->
                                    Row(
                                        Modifier.fillMaxWidth().padding(vertical = 3.dp),
                                        verticalAlignment = Alignment.CenterVertically,
                                    ) {
                                        Surface(shape = MaterialTheme.shapes.small, color = actionColor(c.action)) {
                                            Text(
                                                pitActionLabel(c.action),
                                                style = MaterialTheme.typography.labelSmall,
                                                color = MaterialTheme.colorScheme.onSurface,
                                                modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
                                            )
                                        }
                                        Spacer(Modifier.width(8.dp))
                                        Text(
                                            c.title?.ifEmpty { null } ?: c.path,
                                            style = MaterialTheme.typography.bodySmall,
                                            maxLines = 1,
                                            overflow = TextOverflow.Ellipsis,
                                            modifier = Modifier.weight(1f),
                                        )
                                        if (c.action == "moved" && !c.from.isNullOrEmpty()) {
                                            Text(
                                                "from ${c.from}",
                                                style = MaterialTheme.typography.labelSmall,
                                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                                maxLines = 1,
                                                overflow = TextOverflow.Ellipsis,
                                            )
                                        }
                                    }
                                }
                            }
                        }
                        Spacer(Modifier.height(8.dp))
                        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                        Spacer(Modifier.height(8.dp))
                        Text(
                            "What stands now is committed and tagged first, and everything this removes moves to the trash — so the restore itself can be undone. Open editors converge on the restored text.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        },
        confirmButton = {
            TextButton(
                enabled = !busy && preview != null && preview?.changes?.isNotEmpty() == true,
                onClick = {
                    busy = true
                    scope.launch {
                        try {
                            onDone(repo.pitRestore(commit, space))
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: Exception) {
                            error = e.userMessage()
                        } finally {
                            busy = false
                        }
                    }
                },
            ) {
                Icon(YanaIcons.Restore, contentDescription = null, modifier = Modifier.height(16.dp).width(16.dp))
                Spacer(Modifier.width(6.dp))
                Text(if (busy) "Restoring…" else "Restore")
            }
        },
        dismissButton = { TextButton(enabled = !busy, onClick = onClose) { Text("Cancel") } },
    )
}

private fun pitActionLabel(action: String): String = when (action) {
    "added" -> "added"
    "deleted" -> "deleted"
    "moved" -> "moved"
    else -> "edited"
}

@Composable
private fun actionColor(action: String) = when (action) {
    "added" -> MaterialTheme.colorScheme.primaryContainer
    "deleted" -> MaterialTheme.colorScheme.errorContainer
    "moved" -> MaterialTheme.colorScheme.secondaryContainer
    else -> MaterialTheme.colorScheme.surfaceContainerHigh
}

private fun countList(p: PitPreview): String {
    val parts = ArrayList<String>()
    if (p.added > 0) parts += "${p.added} added"
    if (p.changed > 0) parts += "${p.changed} changed"
    if (p.deleted > 0) parts += "${p.deleted} deleted"
    if (p.moved > 0) parts += "${p.moved} moved"
    val total = p.changes.size
    return "$total ${if (total == 1) "path" else "paths"}: ${parts.joinToString(", ")}."
}

/** The toast line for a restore that ran, the web's own words. */
fun restoreSummaryText(s: RestoreSummary): String {
    val parts = ArrayList<String>()
    if (s.added > 0) parts += "${s.added} added"
    if (s.changed > 0) parts += "${s.changed} changed"
    if (s.deleted > 0) parts += "${s.deleted} deleted"
    if (s.moved > 0) parts += "${s.moved} moved"
    return if (parts.isEmpty()) "It already stood here; nothing changed." else "Restored: ${parts.joinToString(", ")}."
}
