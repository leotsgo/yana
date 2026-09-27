package com.collinpendleton.yana.ui.screens

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
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
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.ui.ShellInsets
import com.collinpendleton.yana.data.NoteMeta
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder

/**
 * Every tag across the account's notes with the count of notes carrying
 * it — the web's tags page. The replica answers offline, so the list is
 * whatever the last sync saw.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TagsScreen(
    repo: NoteRepository,
    onBack: () -> Unit,
    onTag: (String) -> Unit,
) {
    val vm: Loader<List<com.collinpendleton.yana.data.TagCount>> = viewModel(key = "tags") {
        Loader(fetch = { repo.tags() }, refetch = { repo.sync(); repo.tags() })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val tags = state.data

    Scaffold(
        contentWindowInsets = ShellInsets,
        topBar = {
            TopAppBar(
                title = { Text("Tags") },
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
            if (tags == null) {
                Placeholder(loading = state.loading, error = state.error, empty = null, onRetry = { vm.reload() })
            } else if (tags.isEmpty()) {
                Column(Modifier.fillMaxSize().padding(32.dp)) {
                    Text(
                        "No tags yet. Write #anything in a note and it shows up here, with every note that carries it.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            } else {
                Column(Modifier.fillMaxSize()) {
                    Text(
                        "${tags.size} ${if (tags.size == 1) "tag" else "tags"} across your notes",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
                    )
                    TagCloud(tags, onTag)
                }
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun TagCloud(tags: List<com.collinpendleton.yana.data.TagCount>, onTag: (String) -> Unit) {
    FlowRow(
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
        modifier = Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 8.dp),
    ) {
        tags.forEach { t ->
            Surface(
                shape = MaterialTheme.shapes.small,
                color = MaterialTheme.colorScheme.primaryContainer,
                modifier = Modifier.clickable { onTag(t.tag) },
            ) {
                Row(
                    Modifier.padding(horizontal = 10.dp, vertical = 5.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                ) {
                    Text(
                        "#${t.tag}",
                        style = MaterialTheme.typography.labelLarge,
                        color = MaterialTheme.colorScheme.onPrimaryContainer,
                    )
                    Text(
                        t.count.toString(),
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onPrimaryContainer,
                    )
                }
            }
        }
    }
}

/**
 * One tag's notes, in path order — the web's tag page. Offline the
 * replica answers with the notes it carries.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TagScreen(
    repo: NoteRepository,
    tag: String,
    onBack: () -> Unit,
    onAllTags: () -> Unit,
    onNote: (id: String, title: String) -> Unit,
) {
    val vm: Loader<List<NoteMeta>> = viewModel(key = "tag:$tag") {
        Loader(fetch = { repo.tagNotes(tag) })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val notes = state.data

    Scaffold(
        contentWindowInsets = ShellInsets,
        topBar = {
            TopAppBar(
                title = { Text("#$tag") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                actions = {
                    TextButton(onClick = onAllTags) { Text("All tags") }
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
            if (notes == null) {
                Placeholder(loading = state.loading, error = state.error, empty = null, onRetry = { vm.reload() })
            } else if (notes.isEmpty()) {
                Column(Modifier.fillMaxSize().padding(32.dp)) {
                    Text(
                        "No note carries #$tag right now.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            } else {
                LazyColumn(Modifier.fillMaxSize()) {
                    item {
                        Text(
                            "${notes.size} ${if (notes.size == 1) "note" else "notes"}",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
                        )
                    }
                    items(notes, key = { it.id }) { n ->
                        TagNoteRow(n) { onNote(n.id, n.title.ifEmpty { n.path.substringAfterLast('/') }) }
                    }
                }
            }
        }
    }
}

@Composable
private fun TagNoteRow(note: NoteMeta, onClick: () -> Unit) {
    Column(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 20.dp, vertical = 10.dp),
    ) {
        Text(
            note.title.ifEmpty { note.path.substringAfterLast('/') },
            style = MaterialTheme.typography.bodyLarge,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (note.space.isNotEmpty()) {
                Text(
                    note.space,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Spacer(Modifier.width(8.dp))
            }
            Text(
                note.path,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
        }
        if (note.preview.isNotEmpty()) {
            Text(
                note.preview,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}
