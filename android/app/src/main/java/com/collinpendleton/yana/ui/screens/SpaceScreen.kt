package com.collinpendleton.yana.ui.screens

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
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
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.background
import com.collinpendleton.yana.Prefs
import androidx.compose.foundation.shape.CircleShape
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.TreeNode
import com.collinpendleton.yana.data.TreeRow
import com.collinpendleton.yana.data.noteCount
import com.collinpendleton.yana.data.visibleRows
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.YanaIcons

/** One space: its folder tree, folders opening in place, notes listed under them. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SpaceScreen(
    repo: NoteRepository,
    prefs: Prefs,
    space: String,
    label: String,
    onBack: () -> Unit,
    onNote: (id: String, title: String) -> Unit,
    onActivity: () -> Unit = {},
) {
    // The replica answers if the network cannot; pull-to-refresh syncs.
    val vm: Loader<List<TreeNode>> = viewModel(key = "space:$space") {
        Loader(
            fetch = { repo.tree(space) },
            refetch = { repo.sync(); repo.tree(space) },
        )
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    // The tree opens the way it was left, so a deep folder is one tap
    // away on the next visit instead of one per level.
    var expanded by rememberSaveable { mutableStateOf(prefs.openFolders(space)) }
    LaunchedEffect(expanded) { prefs.setOpenFolders(space, expanded) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(label, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                actions = {
                    IconButton(onClick = onActivity) { Icon(YanaIcons.History, contentDescription = "What changed") }
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
            val nodes = state.data
            if (nodes.isNullOrEmpty()) {
                Placeholder(
                    loading = state.loading,
                    error = state.error,
                    empty = "This space holds no notes yet.",
                    onRetry = { vm.reload() },
                )
            } else {
                val rows = visibleRows(nodes, expanded)
                LazyColumn(Modifier.fillMaxSize()) {
                    items(rows, key = { it.node.path }) { row ->
                        TreeRowItem(row) {
                            val n = row.node
                            if (n.isDir) expanded = if (row.expanded) expanded - n.path else expanded + n.path
                            else n.id?.let { onNote(it, n.label) }
                        }
                    }
                    state.error?.let { item { ErrorLine(it) } }
                }
            }
        }
    }
}

@Composable
private fun TreeRowItem(row: TreeRow, onClick: () -> Unit) {
    val n = row.node
    val turn by animateFloatAsState(if (row.expanded) 90f else 0f, label = "chevron")
    Row(
        Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(start = 12.dp + 20.dp * row.depth, end = 16.dp, top = 11.dp, bottom = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(24.dp), contentAlignment = Alignment.Center) {
            if (n.isDir) {
                Icon(
                    Icons.AutoMirrored.Filled.KeyboardArrowRight,
                    contentDescription = if (row.expanded) "Collapse" else "Expand",
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.rotate(turn),
                )
            } else {
                Box(Modifier.size(5.dp).clip(CircleShape).background(MaterialTheme.colorScheme.outline))
            }
        }
        Spacer(Modifier.width(8.dp))
        Text(
            if (n.isDir) n.name + "/" else n.label,
            style = MaterialTheme.typography.bodyLarge,
            fontWeight = if (n.isDir) FontWeight.Medium else FontWeight.Normal,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (n.isDir) {
            Text(
                noteCount(n).toString(),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
