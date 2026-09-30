package com.collinpendleton.yana.ui.htmlnote

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.data.Note
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.ui.theme.Mono

/**
 * The body of an HTML note: the rendered note in a sandboxed WebView on
 * the content origin, or its source in a plain text editor with explicit
 * saves. HTML does not merge, so saves are whole-file and
 * last-write-wins; when the version on disk moved aside, the notice
 * names where it went and offers the way in to settle it. Trust shows
 * as a read-only badge and is changed on the web.
 */
@Composable
fun HtmlNotePane(
    repo: NoteRepository,
    note: Note,
    modifier: Modifier = Modifier,
    onConflicts: () -> Unit = {},
) {
    val vm: HtmlNoteModel = viewModel(key = "htmlnote:${note.id}") { HtmlNoteModel() }
    val view by vm.view.collectAsStateWithLifecycle()
    val saving by vm.saving.collectAsStateWithLifecycle()
    val savedMessage by vm.message.collectAsStateWithLifecycle()

    var editing by rememberSaveable(note.id) { mutableStateOf(false) }
    var source by remember(note.id) { mutableStateOf(note.source ?: "") }
    var baseHash by remember(note.id) { mutableStateOf(note.contentHash) }
    var dirty by remember(note.id) { mutableStateOf(false) }
    var conflictCopy by remember(note.id) { mutableStateOf<String?>(null) }

    LaunchedEffect(note.id) { vm.load(repo, note.id) }
    // A refresh that brings a newer file adopts it, unless there are unsaved edits.
    LaunchedEffect(note.id, note.updatedAt) {
        if (!dirty) {
            source = note.source ?: ""
            baseHash = note.contentHash
        }
    }

    Column(modifier.fillMaxSize().padding(horizontal = 20.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            TrustBadge(note.trusted)
            Spacer(Modifier.weight(1f))
            if (editing) {
                TextButton(
                    enabled = dirty && !saving,
                    onClick = {
                        vm.save(repo, note.id, source, baseHash) { hash, copy ->
                            baseHash = hash ?: baseHash
                            dirty = false
                            conflictCopy = copy
                        }
                    },
                ) { Text("Save") }
            }
            SingleChoiceSegmentedButtonRow {
                SegmentedButton(
                    selected = !editing,
                    onClick = { editing = false },
                    shape = SegmentedButtonDefaults.itemShape(0, 2),
                ) { Text("View") }
                SegmentedButton(
                    selected = editing,
                    onClick = { editing = true },
                    shape = SegmentedButtonDefaults.itemShape(1, 2),
                ) { Text("Source") }
            }
        }
        Text("Trust is set on the web.", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        conflictCopy?.let { parked ->
            // The parked copy is a conflict beside this note now; the
            // notice names it and Resolve goes in to settle it.
            Surface(shape = MaterialTheme.shapes.small, color = MaterialTheme.colorScheme.secondaryContainer) {
                Row(
                    Modifier.fillMaxWidth().padding(start = 12.dp, end = 4.dp, top = 4.dp, bottom = 4.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        "Saved. The version that was on disk moved to $parked.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSecondaryContainer,
                        modifier = Modifier.weight(1f),
                    )
                    TextButton(onClick = {
                        conflictCopy = null
                        onConflicts()
                    }) { Text("Resolve") }
                }
            }
        }
        val line = savedMessage ?: view.message
        if (line != null) {
            Text(line, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Box(Modifier.weight(1f).fillMaxWidth()) {
            val viewUrl = view.url
            when {
                editing -> OutlinedTextField(
                    value = source,
                    onValueChange = {
                        source = it
                        dirty = true
                    },
                    modifier = Modifier.fillMaxSize(),
                    textStyle = TextStyle(fontFamily = Mono, fontSize = MaterialTheme.typography.bodySmall.fontSize),
                )
                viewUrl != null -> NoteWebView(viewUrl, Modifier.fillMaxSize())
                view.loading -> CircularProgressIndicator(Modifier.align(Alignment.Center))
                else -> Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
                    SelectionContainer {
                        Text(
                            source.ifBlank { "This note is empty." },
                            style = TextStyle(fontFamily = Mono, fontSize = MaterialTheme.typography.bodySmall.fontSize),
                        )
                    }
                }
            }
        }
    }
}

/** The read-only trust badge: scripts run in trusted notes, and the flag changes on the web. */
@Composable
private fun TrustBadge(trusted: Boolean) {
    val (label, container, content) = if (trusted) {
        Triple("Trusted", MaterialTheme.colorScheme.tertiaryContainer, MaterialTheme.colorScheme.onTertiaryContainer)
    } else {
        Triple("Sanitized", MaterialTheme.colorScheme.secondaryContainer, MaterialTheme.colorScheme.onSecondaryContainer)
    }
    Surface(shape = MaterialTheme.shapes.small, color = container) {
        Text(
            label,
            style = MaterialTheme.typography.labelMedium,
            color = content,
            modifier = Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
        )
    }
}
