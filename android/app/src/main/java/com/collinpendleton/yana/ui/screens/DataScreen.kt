package com.collinpendleton.yana.ui.screens

import android.content.Intent
import android.widget.Toast
import androidx.core.content.FileProvider
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
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.Loader
import java.io.File
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * The data of the account from a phone: what can come back (the trash,
 * deleted notes, conflicts) and what leaves (a space as a zip, handed
 * to the share sheet).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DataScreen(
    app: YanaApp,
    onBack: () -> Unit,
    onDeletedNotes: () -> Unit = {},
    onTrash: () -> Unit = {},
    onConflicts: () -> Unit = {},
) {
    // The conflict count: a hint for the row, not a fact the screen
    // depends on — a failure reads as unknown.
    val conflicts: Loader<Int> = viewModel(key = "conflict-count") { Loader(fetch = { app.repo.conflicts().size }) }
    val conflictCount by conflicts.loaded.collectAsStateWithLifecycle()
    val spaces: Loader<List<Space>> = viewModel(key = "spaces-for-export") {
        Loader(fetch = { app.repo.spaces() })
    }
    val spaceList by spaces.loaded.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }
    var exporting by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Data") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
            )
        },
    ) { pad ->
        Column(
            Modifier.padding(pad).fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Column(Modifier.widthIn(max = 560.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                SettingsRow(
                    title = "Trash",
                    blurb = "Deleted notes kept for 30 days: restore, delete for good, empty.",
                    onClick = onTrash,
                )
                SettingsRow(
                    title = "Deleted notes",
                    blurb = "Notes whose files are gone, each restorable to where it lived.",
                    onClick = onDeletedNotes,
                )
                SettingsRow(
                    title = "Conflicts",
                    blurb = when (val n = conflictCount.data) {
                        null -> "Copies parked beside a note when two writes met the same path."
                        0 -> "No conflict copies wait."
                        1 -> "1 conflict copy waits — two writes met the same path."
                        else -> "$n conflict copies wait — two writes met the same path."
                    },
                    warn = (conflictCount.data ?: 0) > 0,
                    onClick = onConflicts,
                )
                SettingsRow(
                    title = "Export a space",
                    blurb = "A space's notes as a zip, through the share sheet.",
                    onClick = { exporting = true },
                )
                Spacer(Modifier.height(12.dp))
            }
        }
    }

    if (exporting) {
        AlertDialog(
            onDismissRequest = { if (!busy) exporting = false },
            title = { Text("Export a space") },
            text = {
                Column {
                    val list = spaceList.data?.filter { it.name.isNotEmpty() }.orEmpty()
                    if (list.isEmpty()) {
                        Text(
                            if (spaceList.data != null) "No spaces to export." else "The spaces list is not loaded. Try again.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    list.forEach { s ->
                        Row(
                            Modifier.fillMaxWidth().clickable(enabled = !busy) {
                                busy = true
                                scope.launch {
                                    try {
                                        val bytes = app.client.exportNotesZip(s.name)
                                        val file = File(context.cacheDir, "yana-${zipSlug(s.name)}.zip")
                                        file.writeBytes(bytes)
                                        val uri = FileProvider.getUriForFile(context, "${context.packageName}.files", file)
                                        val send = Intent(Intent.ACTION_SEND).apply {
                                            type = "application/zip"
                                            putExtra(Intent.EXTRA_STREAM, uri)
                                            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                                        }
                                        context.startActivity(Intent.createChooser(send, null))
                                        exporting = false
                                    } catch (e: CancellationException) {
                                        throw e
                                    } catch (e: Exception) {
                                        toast(e.userMessage())
                                    } finally {
                                        busy = false
                                    }
                                }
                            }.padding(vertical = 6.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            if (busy) {
                                CircularProgressIndicator(Modifier.padding(end = 12.dp).size(20.dp))
                            } else {
                                RadioButton(selected = false, onClick = null, modifier = Modifier.padding(start = 4.dp))
                            }
                            Column(Modifier.weight(1f)) {
                                Text(s.displayName, style = MaterialTheme.typography.bodyLarge)
                                Text(
                                    if (s.notes == 1) "1 note" else "${s.notes} notes",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                        }
                    }
                }
            },
            confirmButton = {},
            dismissButton = { TextButton(enabled = !busy, onClick = { exporting = false }) { Text("Cancel") } },
        )
    }
}

/** A flat, file-system-safe name for an exported zip. */
fun zipSlug(space: String): String = buildString {
    for (c in space) {
        append(if (c.isLetterOrDigit() || c == '.' || c == '-' || c == '_') c else '-')
    }
}.ifEmpty { "notes" }.take(60)

/** One settings row: a title, a line under it, and a chevron. */
@Composable
internal fun SettingsRow(
    title: String,
    blurb: String,
    onClick: () -> Unit,
    warn: Boolean = false,
) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            Text(
                blurb,
                style = MaterialTheme.typography.bodySmall,
                color = if (warn) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Icon(
            Icons.AutoMirrored.Filled.KeyboardArrowRight,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}
