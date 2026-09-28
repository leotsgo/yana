package com.collinpendleton.yana.ui.screens

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.collinpendleton.yana.data.NewNoteKit
import com.collinpendleton.yana.data.NewNoteKit.baseOf
import com.collinpendleton.yana.data.NewNoteKit.dirOf
import com.collinpendleton.yana.ui.YanaIcons
import kotlinx.coroutines.launch

/**
 * The folder picker: the folders one level under the one being
 * browsed, tapped to walk into and out of, the folders used lately at
 * the top, and a row that makes a new folder where the browser sits.
 * Move here picks the browsed folder itself — the root picks a
 * space's top level, never the loose root notes.
 *
 * @param here The folder the browsing starts in.
 * @param exclude Folders that cannot be picked or walked into (a
 * folder being moved excludes itself and everything under it).
 * @param createFolder Makes a folder, reporting whether it went
 * through; null keeps the new-folder row away.
 */
@Composable
fun FolderPickerDialog(
    title: String,
    here: String,
    dirs: List<String>,
    spaces: List<String>,
    recents: List<String>,
    exclude: (String) -> Boolean = { false },
    confirmLabel: String = "Move here",
    createFolder: (suspend (path: String) -> Boolean)? = null,
    onPick: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    var browse by remember { mutableStateOf(here) }
    var making by remember { mutableStateOf(false) }
    var name by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    val kids = NewNoteKit.children(browse, spaces, dirs).filter { !exclude(it) }
    val known = dirs.toSet()
    val recent = recents.filter { it != here && it != browse && it in known && !exclude(it) && it != "" }

    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text(title, maxLines = 1, overflow = TextOverflow.Ellipsis) },
        text = {
            Column {
                Text(
                    browse.ifEmpty { "Spaces" }.let { if (it == "Spaces") it else "$it/" },
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Spacer(Modifier.height(4.dp))
                if (making) {
                    OutlinedTextField(
                        value = name,
                        onValueChange = { name = it },
                        placeholder = { Text("Name for the new folder") },
                        singleLine = true,
                        enabled = !busy,
                        modifier = Modifier.fillMaxWidth(),
                    )
                } else {
                    LazyColumn(Modifier.fillMaxWidth().heightIn(max = 360.dp)) {
                        if (browse.isNotEmpty()) {
                            item(key = "up") {
                                val parent = dirOf(browse)
                                PickerRow(
                                    label = if (parent.isEmpty()) "Up to the spaces" else "Up to ${baseOf(parent)}/",
                                    detail = parent.ifEmpty { "the spaces" },
                                    icon = { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = null, modifier = Modifier.size(18.dp)) },
                                ) {
                                    browse = parent
                                }
                            }
                        }
                        if (recent.isNotEmpty()) {
                            item(key = "recent-head") { PickerHead("Recent") }
                            items(recent, key = { "recent:$it" }) { p ->
                                PickerRow(label = baseOf(p) + "/", detail = p, icon = { Icon(YanaIcons.Folder, contentDescription = null, modifier = Modifier.size(18.dp)) }) {
                                    browse = p
                                }
                            }
                        }
                        if (kids.isNotEmpty() || recent.isNotEmpty()) {
                            item(key = "folders-head") { PickerHead("Folders") }
                        }
                        items(kids, key = { it }) { p ->
                            PickerRow(label = baseOf(p) + "/", icon = { Icon(YanaIcons.Folder, contentDescription = null, modifier = Modifier.size(18.dp)) }) {
                                browse = p
                            }
                        }
                        if (kids.isEmpty() && recent.isEmpty()) {
                            item(key = "none") {
                                Text(
                                    "Nothing under this folder.",
                                    style = MaterialTheme.typography.bodyMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                        }
                        if (createFolder != null && browse.isNotEmpty()) {
                            item(key = "new-folder") {
                                PickerRow(
                                    label = "New folder in ${baseOf(browse)}/",
                                    icon = { Icon(YanaIcons.Plus, contentDescription = null, tint = MaterialTheme.colorScheme.primary, modifier = Modifier.size(18.dp)) },
                                    primary = true,
                                ) {
                                    making = true
                                }
                            }
                        }
                    }
                }
            }
        },
        confirmButton = {
            when {
                making -> TextButton(
                    enabled = !busy && name.isNotBlank() && createFolder != null,
                    onClick = {
                        val clean = NewNoteKit.dirName(name.trim())
                        if (clean.isEmpty() || createFolder == null) return@TextButton
                        val path = "$browse/$clean"
                        busy = true
                        scope.launch {
                            val ok = runCatching { createFolder(path) }.getOrDefault(false)
                            busy = false
                            if (ok) onPick(path) else making = false
                        }
                    },
                ) { Text("Create") }
                else -> TextButton(enabled = !exclude(browse), onClick = { onPick(browse) }) { Text(confirmLabel) }
            }
        },
        dismissButton = {
            TextButton(enabled = !busy, onClick = { if (making) making = false else onDismiss() }) {
                Text(if (making) "Back" else "Cancel")
            }
        },
    )
}

/** One row of the folder picker: an icon, a label, and the path it names. */
@Composable
private fun PickerRow(
    label: String,
    icon: @Composable () -> Unit,
    primary: Boolean = false,
    onPick: () -> Unit,
) {
    PickerRow(label, null, icon, primary, onPick)
}

@Composable
private fun PickerRow(
    label: String,
    detail: String?,
    icon: @Composable () -> Unit,
    primary: Boolean = false,
    onPick: () -> Unit,
) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onPick).padding(vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        icon()
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(
                label,
                style = MaterialTheme.typography.bodyLarge,
                color = if (primary) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (detail != null) {
                Text(
                    detail,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

@Composable
private fun PickerHead(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.labelMedium,
        fontWeight = FontWeight.Medium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 8.dp, bottom = 2.dp),
    )
}
