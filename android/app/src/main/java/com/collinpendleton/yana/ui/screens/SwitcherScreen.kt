package com.collinpendleton.yana.ui.screens

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.ui.ShellInsets
import com.collinpendleton.yana.Prefs
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.Switcher
import com.collinpendleton.yana.data.SwitcherNote
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.YanaIcons

/**
 * The switcher: a search-as-you-type list of every note over the
 * replica, the web's quick palette. An empty box lists recents first;
 * typing fuzzy-matches titles and paths, a #word (or tag:, path:,
 * space:, is:) narrows the rows the tree can judge, and a name nothing
 * carries offers to create it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SwitcherScreen(
    repo: NoteRepository,
    prefs: Prefs,
    onBack: () -> Unit,
    onNote: (id: String, title: String) -> Unit,
    onCreate: (name: String) -> Unit,
) {
    // The pool is the replica's every note — the switcher is a local
    // list, so airplane mode answers the same as online.
    val vm: Loader<List<SwitcherNote>> = viewModel(key = "switcher") {
        Loader(fetch = { repo.allNotes().map { it.toSwitcherNote() } })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val recents by prefs.recents.collectAsStateWithLifecycle()

    var query by remember { mutableStateOf("") }
    val notes = state.data.orEmpty()
    val rows = Switcher.rows(notes, recents.map { it.id }.toSet(), query)
    val create = Switcher.createFor(rows, query)

    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }

    Scaffold(
        contentWindowInsets = ShellInsets,
        topBar = {
            TopAppBar(
                title = {
                    TextField(
                        value = query,
                        onValueChange = { query = it },
                        placeholder = { Text("Open a note, or type #tag") },
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
                        modifier = Modifier.fillMaxWidth().focusRequester(focus),
                    )
                },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
            )
        },
    ) { pad ->
        if (notes.isEmpty()) {
            Placeholder(
                loading = state.loading,
                error = state.error,
                empty = "No notes yet.",
                onRetry = { vm.reload() },
                modifier = Modifier.padding(pad),
            )
        } else {
            Column(Modifier.padding(pad).fillMaxSize()) {
                LazyColumn(Modifier.fillMaxSize()) {
                    items(rows, key = { it.id }) { note ->
                        SwitcherRow(note) { onNote(note.id, note.label) }
                    }
                    if (create != null) {
                        item(key = "create") {
                            CreateRow(create) { onCreate(create) }
                        }
                    }
                    if (rows.isEmpty() && create == null) {
                        item { NothingMatches() }
                    }
                }
            }
        }
    }
}

@Composable
private fun SwitcherRow(note: SwitcherNote, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 20.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                note.label.ifEmpty { note.path.substringAfterLast('/') },
                style = MaterialTheme.typography.bodyLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                note.detail,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

@Composable
private fun CreateRow(name: String, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 20.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(YanaIcons.Plus, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
        Spacer(Modifier.width(12.dp))
        Text(
            "Create \"$name\"",
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.primary,
        )
    }
}

@Composable
private fun NothingMatches() {
    Text(
        "Nothing carries that.",
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(20.dp),
    )
}

/** The wire's note metadata as the switcher's row. */
private fun com.collinpendleton.yana.data.NoteMeta.toSwitcherNote() =
    SwitcherNote(
        id = id,
        space = space,
        path = path,
        title = title,
        kind = kind,
        tags = tags,
    )
