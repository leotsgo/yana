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
import androidx.compose.foundation.lazy.rememberLazyListState
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
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.foundation.background
import com.collinpendleton.yana.ui.ShellInsets
import com.collinpendleton.yana.Prefs
import androidx.compose.foundation.shape.CircleShape
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.TreeNode
import com.collinpendleton.yana.data.TreeRow
import com.collinpendleton.yana.data.noteCount
import com.collinpendleton.yana.data.visibleRows
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.YanaIcons

/**
 * The tree: every space the account holds, each collapsible, its
 * folders opening in place and its notes under them — the web's tree
 * drawer on a phone. Folders sit closed until they are opened; the
 * opened folders and the collapsed spaces are remembered, per device,
 * the moment they change, so a kill and relaunch shows the same shape.
 * [focus] names the space a tap on home arrived for: it starts open and
 * in view.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SpaceScreen(
    repo: NoteRepository,
    prefs: Prefs,
    focus: String,
    label: String,
    onBack: () -> Unit,
    onNote: (id: String, title: String) -> Unit,
    onActivity: () -> Unit = {},
    onSwitcher: () -> Unit = {},
) {
    // The replica answers if the network cannot; pull-to-refresh syncs.
    val vm: Loader<List<Space>> = viewModel(key = "spaces-tree") {
        Loader(fetch = { repo.spaces() }, refetch = { repo.sync(); repo.spaces() })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val spaces = state.data

    // A space the tree was opened for starts open; from then on the
    // closed set is remembered.
    var closed by remember { mutableStateOf(prefs.closedSpaces() - focus) }
    val trees = remember { mutableStateMapOf<String, List<TreeNode>>() }
    val folders = remember { mutableStateMapOf<String, Set<String>>() }
    var treeError by remember { mutableStateOf<String?>(null) }
    val open = spaces.orEmpty().filter { it.name !in closed }
    val list = rememberLazyListState()

    // The trees of the open spaces, loaded once each; the replica
    // answers offline.
    LaunchedEffect(open.joinToString("\u0000") { it.name }) {
        for (s in open) {
            if (!trees.containsKey(s.name)) {
                runCatching { repo.tree(s.name) }
                    .onSuccess { trees[s.name] = it }
                    .onFailure { if (treeError == null) treeError = it.message ?: "Could not load the tree." }
            }
        }
    }

    fun rowsOf(space: Space): List<TreeRow> {
        val nodes = trees[space.name] ?: return emptyList()
        val expanded = folders[space.name] ?: prefs.openFolders(space.name)
        return visibleRows(nodes, expanded)
    }

    // A focused space lands in view once its rows exist.
    LaunchedEffect(focus, spaces?.size, trees.size) {
        if (focus.isEmpty()) return@LaunchedEffect
        val names = spaces.orEmpty()
        val idx = names.indexOfFirst { it.name == focus }
        if (idx >= 0) {
            val header = (0 until idx).sumOf { i ->
                val s = names[i]
                1 + if (s.name !in closed) maxOf(1, rowsOf(s).size) else 0
            }
            runCatching { list.scrollToItem(header) }
        }
    }

    fun setSpaceOpen(name: String, openNow: Boolean) {
        closed = if (openNow) closed - name else closed + name
        prefs.setClosedSpaces(closed)
    }

    fun toggleFolder(space: Space, row: TreeRow) {
        val n = row.node
        val current = folders.getOrPut(space.name) { prefs.openFolders(space.name) }
        val next = if (row.expanded) current - n.path else current + n.path
        folders[space.name] = next
        prefs.setOpenFolders(space.name, next)
    }

    Scaffold(
        contentWindowInsets = ShellInsets,
        topBar = {
            TopAppBar(
                title = { Text(label.ifEmpty { "Notes" }, maxLines = 1, overflow = TextOverflow.Ellipsis) },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                actions = {
                    IconButton(onClick = onSwitcher) { Icon(YanaIcons.Command, contentDescription = "Switcher") }
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
            if (spaces.isNullOrEmpty()) {
                Placeholder(
                    loading = state.loading,
                    error = state.error,
                    empty = "No notes yet. Make one, or drop a markdown file into the notes directory; it shows up on the next scan.",
                    onRetry = { vm.reload() },
                )
            } else {
                LazyColumn(Modifier.fillMaxSize(), state = list) {
                    spaces.forEach { space ->
                        val isOpen = space.name !in closed
                        item(key = "space:" + space.name) {
                            SpaceHeader(space, isOpen) { setSpaceOpen(space.name, !isOpen) }
                        }
                        if (isOpen) {
                            val nodes = trees[space.name]
                            when {
                                nodes == null -> item(key = "space-loading:" + space.name) { PendingRow() }
                                nodes.isEmpty() -> item(key = "space-empty:" + space.name) {
                                    Text(
                                        "Nothing here yet.",
                                        style = MaterialTheme.typography.bodyMedium,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        modifier = Modifier.padding(start = 32.dp, top = 4.dp, bottom = 12.dp),
                                    )
                                }
                                else -> items(rowsOf(space), key = { space.name + "\u0000" + it.node.path }) { row ->
                                    TreeRowItem(row, depth = row.depth + 1) {
                                        if (row.node.isDir) toggleFolder(space, row)
                                        else row.node.id?.let { onNote(it, row.node.label) }
                                    }
                                }
                            }
                        }
                    }
                    treeError?.let { item { ErrorLine(it) } }
                    state.error?.let { item { ErrorLine(it) } }
                }
            }
        }
    }
}

@Composable
private fun SpaceHeader(space: Space, open: Boolean, onClick: () -> Unit) {
    val turn by animateFloatAsState(if (open) 90f else 0f, label = "chevron")
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(start = 8.dp, end = 16.dp, top = 14.dp, bottom = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(24.dp), contentAlignment = Alignment.Center) {
            Icon(
                Icons.AutoMirrored.Filled.KeyboardArrowRight,
                contentDescription = if (open) "Collapse" else "Expand",
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.rotate(turn),
            )
        }
        Spacer(Modifier.width(8.dp))
        Text(
            space.displayName,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.Medium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        Text(
            notes(space.notes),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun PendingRow() {
    Text(
        "…",
        style = MaterialTheme.typography.bodyLarge,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 32.dp, top = 4.dp, bottom = 8.dp),
    )
}

@Composable
private fun TreeRowItem(row: TreeRow, depth: Int, onClick: () -> Unit) {
    val n = row.node
    val turn by animateFloatAsState(if (row.expanded) 90f else 0f, label = "chevron")
    Row(
        Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(start = 12.dp + 20.dp * depth, end = 16.dp, top = 11.dp, bottom = 11.dp),
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
