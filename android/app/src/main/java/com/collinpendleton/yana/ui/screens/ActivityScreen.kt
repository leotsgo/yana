package com.collinpendleton.yana.ui.screens

import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.Prefs
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.parseInstant
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.YanaIcons
import com.collinpendleton.yana.ui.activity.ActivityModel
import com.collinpendleton.yana.ui.activity.ActivityRow
import com.collinpendleton.yana.ui.activity.FeedWindow
import com.collinpendleton.yana.ui.activity.actionLabel
import com.collinpendleton.yana.ui.activity.displayName
import com.collinpendleton.yana.ui.activity.formatAgo
import com.collinpendleton.yana.ui.activity.formatSpan
import com.collinpendleton.yana.ui.activity.groupByDay
import com.collinpendleton.yana.ui.activity.restoreSummaryText
import com.collinpendleton.yana.ui.activity.RestoreDialog
import kotlinx.coroutines.launch

/**
 * What changed, by whom: the git history folded into feed entries — a
 * person's commit one row, an agent's overnight run one row — grouped
 * by day, the notes it names opening at a tap, and the "since I last
 * looked" line dividing what is new to this device. A restore returns
 * the space (or, for the owner, the tree) to an entry's commit,
 * previewed first, listing every note that comes back, changes, moves,
 * or goes.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ActivityScreen(
    repo: NoteRepository,
    prefs: Prefs,
    isOwner: Boolean,
    initialSpace: String = "",
    onBack: () -> Unit,
    onNote: (id: String, title: String) -> Unit,
) {
    val vm: ActivityModel = viewModel(key = if (initialSpace.isEmpty()) "activity" else "activity:$initialSpace") { ActivityModel() }
    val state by vm.state.collectAsStateWithLifecycle()
    val spaces by vm.spaces.collectAsStateWithLifecycle()
    val scope by vm.scope.collectAsStateWithLifecycle()
    val window by vm.window.collectAsStateWithLifecycle()
    val snackbar = remember { SnackbarHostState() }

    LaunchedEffect(Unit) { vm.start(repo, prefs, initialSpace) }

    // The restore controls belong only where the server would take
    // one: the shown space's feed says for that scope, and the tree is
    // the owner's move.
    val canRestore = if (scope.isNotEmpty()) state.restoreAllowed else isOwner
    var restoreFor by remember { mutableStateOf<ActivityRow?>(null) }
    val restoreLabel = if (scope.isNotEmpty()) "Restore $scope to here" else "Restore the tree to here"

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("What changed") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
            )
        },
        snackbarHost = { SnackbarHost(snackbar) },
    ) { pad ->
        Column(Modifier.padding(pad).fillMaxSize()) {
            Column(Modifier.fillMaxWidth().padding(horizontal = 20.dp)) {
                Text(
                    if (scope.isNotEmpty()) "Activity in $scope" else "Across every space you belong to",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(8.dp))
                Row(
                    Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    FeedMenu(
                        label = "Space",
                        value = scope.ifEmpty { "Every space" },
                        options = listOf("Every space" to "") +
                            spaces.map { it.displayName to it.name }.filter { it.second.isNotEmpty() },
                        active = scope.isNotEmpty(),
                        onPick = { vm.filter(repo, it) },
                    )
                    FeedMenu(
                        label = "Window",
                        value = window.label,
                        options = FeedWindow.entries.map { it.label to it.name },
                        active = window != FeedWindow.Seen,
                        onPick = { name -> vm.setWindow(repo, FeedWindow.entries.first { it.name == name }) },
                    )
                }
                Spacer(Modifier.height(8.dp))
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            }
            PullToRefreshBox(
                isRefreshing = state.refreshing,
                onRefresh = { vm.load(repo, pull = true) },
                modifier = Modifier.fillMaxSize(),
            ) {
                val rows = state.rows
                when {
                    rows == null -> Placeholder(loading = state.loading, error = state.error, empty = null, onRetry = { vm.load(repo) })
                    rows.isEmpty() -> Placeholder(loading = false, error = null, empty = "Nothing changed here in this window.", onRetry = { vm.load(repo) })
                    else -> {
                        val groups = remember(rows) { groupByDay(rows) }
                        // The marker's place: everything above the divider
                        // landed since this device last looked.
                        val seenIdx = vm.seenAt?.let { seen -> rows.indexOfFirst { (parseInstant(it.entry.to)?.toEpochMilli() ?: 0L) <= seen } } ?: -1
                        LazyColumn(Modifier.fillMaxSize()) {
                            groups.forEach { g ->
                                item(key = "day:${g.label}:${g.start}") {
                                    Text(
                                        g.label,
                                        style = MaterialTheme.typography.labelMedium,
                                        fontWeight = FontWeight.SemiBold,
                                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                                        modifier = Modifier.padding(horizontal = 20.dp, vertical = 10.dp),
                                    )
                                }
                                g.rows.forEachIndexed { i, row ->
                                    val at = g.start + i
                                    if (at == seenIdx) {
                                        item(key = "seen:$at") { SeenDivider() }
                                    }
                                    item(key = "row:$at:${row.entry.commit}") {
                                        FeedEntry(
                                            row = row,
                                            showSpace = scope.isEmpty(),
                                            canRestore = canRestore,
                                            restoreLabel = restoreLabel,
                                            onOpen = onNote,
                                            onRestore = { restoreFor = row },
                                        )
                                        HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                                    }
                                }
                            }
                            if (state.more) {
                                item(key = "older") {
                                    Box(Modifier.fillMaxWidth().padding(16.dp), contentAlignment = Alignment.Center) {
                                        FilterChip(
                                            selected = false,
                                            onClick = { vm.older(repo) },
                                            label = { Text(if (state.refreshing) "Loading…" else "Older") },
                                        )
                                    }
                                }
                            }
                            state.error?.let { item { ErrorLine(it) } }
                            item { Spacer(Modifier.height(16.dp)) }
                        }
                    }
                }
            }
        }
    }

    restoreFor?.let { row ->
        val scopeLauncher = rememberCoroutineScope()
        RestoreDialog(
            repo = repo,
            commit = row.entry.commit,
            space = scope,
            onDone = { summary ->
                restoreFor = null
                vm.restored(repo)
                scopeLauncher.launch { snackbar.showSnackbar(restoreSummaryText(summary)) }
            },
            onClose = { restoreFor = null },
        )
    }
}

/** The line between what is new to this device and everything already seen. */
@Composable
private fun SeenDivider() {
    Column(Modifier.fillMaxWidth().padding(vertical = 6.dp)) {
        HorizontalDivider(color = MaterialTheme.colorScheme.primary)
        Text(
            "You've seen everything below this line",
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp),
        )
        HorizontalDivider(color = MaterialTheme.colorScheme.primary)
    }
}

/** One feed entry: its author head and the notes it touched. */
@Composable
private fun FeedEntry(
    row: ActivityRow,
    showSpace: Boolean,
    canRestore: Boolean,
    restoreLabel: String,
    onOpen: (id: String, title: String) -> Unit,
    onRestore: () -> Unit,
) {
    val e = row.entry
    Column(Modifier.fillMaxWidth().padding(start = 20.dp, end = 12.dp, top = 12.dp, bottom = 10.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Surface(shape = MaterialTheme.shapes.small, color = MaterialTheme.colorScheme.surfaceContainerHigh) {
                Icon(
                    authorIcon(e.kind),
                    contentDescription = authorLabel(e.kind),
                    tint = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.padding(4.dp).size(15.dp),
                )
            }
            Spacer(Modifier.width(8.dp))
            Text(
                e.author.ifEmpty { "someone" },
                style = MaterialTheme.typography.titleSmall,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (e.commits > 1) {
                Spacer(Modifier.width(6.dp))
                Text(
                    "${e.commits} commits over ${formatSpan(e.from, e.to)}",
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Spacer(Modifier.width(6.dp))
            Text(
                formatAgo(e.to),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (showSpace) {
                Spacer(Modifier.width(6.dp))
                Text(
                    row.space,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Spacer(Modifier.weight(1f))
            if (canRestore && e.commit.isNotEmpty()) {
                IconButton(onClick = onRestore, modifier = Modifier.size(32.dp)) {
                    Icon(
                        YanaIcons.Restore,
                        contentDescription = restoreLabel,
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.size(17.dp),
                    )
                }
            }
        }
        if (e.changes.isNotEmpty()) {
            Spacer(Modifier.height(4.dp))
            e.changes.forEach { c ->
                Row(
                    Modifier.fillMaxWidth().padding(vertical = 2.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Surface(shape = MaterialTheme.shapes.small, color = changeChipColor(c.action)) {
                        Text(
                            actionLabel(c.action),
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurface,
                            modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
                        )
                    }
                    Spacer(Modifier.width(8.dp))
                    val name = c.displayName()
                    if (c.id != null && c.action != "deleted") {
                        Text(
                            name,
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.primary,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.weight(1f).clickable { onOpen(c.id, c.title.orEmpty()) },
                        )
                    } else {
                        Text(
                            name,
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.weight(1f),
                        )
                    }
                    if (c.action == "renamed" && !c.from.isNullOrEmpty()) {
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
}

@Composable
private fun changeChipColor(action: String) = when (action) {
    "added" -> MaterialTheme.colorScheme.primaryContainer
    "deleted" -> MaterialTheme.colorScheme.errorContainer
    "renamed" -> MaterialTheme.colorScheme.secondaryContainer
    else -> MaterialTheme.colorScheme.surfaceContainerHigh
}

/** One labeled dropdown chip of the filter row. */
@Composable
private fun FeedMenu(
    label: String,
    value: String,
    options: List<Pair<String, String>>,
    active: Boolean,
    onPick: (String) -> Unit,
) {
    var open by remember { mutableStateOf(false) }
    Box {
        FilterChip(
            selected = active,
            onClick = { open = true },
            label = { Text("$label: $value", maxLines = 1, overflow = TextOverflow.Ellipsis) },
        )
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            options.forEach { (name, picked) ->
                DropdownMenuItem(
                    text = { Text(name) },
                    onClick = {
                        open = false
                        onPick(picked)
                    },
                )
            }
        }
    }
}
