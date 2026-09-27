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
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.Checkbox
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
import androidx.compose.material3.SnackbarResult
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.ui.ShellInsets
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.TagCount
import com.collinpendleton.yana.data.TaskNote
import com.collinpendleton.yana.data.TaskRow
import com.collinpendleton.yana.data.TaskScope
import com.collinpendleton.yana.data.TickOutcome
import com.collinpendleton.yana.data.rt.RtEvent
import com.collinpendleton.yana.data.rt.RtStatus
import com.collinpendleton.yana.data.rt.SyncEngine
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.tasks.TaskTextParser
import com.collinpendleton.yana.ui.tasks.TasksModel
import com.collinpendleton.yana.ui.theme.Mono
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** How long a run of change signals waits before the listing refetches. */
private const val LIVE_DEBOUNCE_MS = 800L

/**
 * The tasks page: every open box across a space (or every space the
 * account belongs to), grouped by the note it lives in, ticked in place
 * through the same write the web's page and the in-note checkbox use.
 * While the screen is open the engine watches the listed spaces and the
 * list follows changes as they happen; offline, the last fetched list
 * shows with its age and a tick queues for the next sync.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TasksScreen(
    repo: NoteRepository,
    sync: SyncEngine,
    onBack: () -> Unit,
    onNote: (id: String, title: String, line: Int) -> Unit,
    initialSpace: String = "",
) {
    val vm: TasksModel = viewModel { TasksModel() }
    val state by vm.state.collectAsStateWithLifecycle()
    val scope by vm.scope.collectAsStateWithLifecycle()
    val spaces by vm.spaces.collectAsStateWithLifecycle()
    val tags by vm.tags.collectAsStateWithLifecycle()
    val folders by vm.folders.collectAsStateWithLifecycle()
    val snackbar = remember { SnackbarHostState() }

    LaunchedEffect(Unit) { vm.start(repo, initialSpace) }

    // The listed spaces are watched while the screen is open; a change
    // in one of them refetches the listing, debounced — the web page's
    // own live rule. The own tick arrives here too.
    DisposableEffect(Unit) {
        onDispose { sync.watchSpaces(emptySet()) }
    }
    val targets = vm.watchTargets
    LaunchedEffect(targets) { sync.watchSpaces(targets.toSet()) }
    LaunchedEffect(Unit) {
        var refetch: Job? = null
        launch {
            sync.events.collect { event ->
                if (event is RtEvent.Changed) {
                    refetch?.cancel()
                    refetch = launch {
                        delay(LIVE_DEBOUNCE_MS)
                        vm.load(repo)
                    }
                }
            }
        }
    }
    // A connection coming back replays the queue (a tick from airplane
    // mode) and refetches the listing.
    LaunchedEffect(Unit) {
        var prev: RtStatus? = null
        sync.status.collect { s ->
            val wasOffline = prev == RtStatus.Offline
            prev = s
            if (wasOffline && s != RtStatus.Offline) vm.reconnected(repo)
        }
    }
    // The tick's snackbar: Ticked with Undo, the refusal's reason, the queue's word.
    LaunchedEffect(Unit) {
        vm.feedback.collect { f ->
            when (val outcome = f.outcome) {
                is TickOutcome.Done -> {
                    val res = snackbar.showSnackbar(if (f.to) "Ticked." else "Unticked.", actionLabel = "Undo")
                    if (res == SnackbarResult.ActionPerformed) vm.tick(repo, f.task, !f.to)
                }
                is TickOutcome.Queued -> snackbar.showSnackbar("Offline. The tick goes out with the next sync.")
                is TickOutcome.Refused -> snackbar.showSnackbar(outcome.message)
            }
        }
    }

    Scaffold(
        contentWindowInsets = ShellInsets,
        topBar = {
            TopAppBar(
                title = { Text("Tasks") },
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
                    subtitle(scope, state.rows?.size),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                val fetchedAt = state.fetchedAt
                if (!state.fromServer && fetchedAt != null) {
                    Text(
                        "Offline — the list as of ${ageOf(fetchedAt)}.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Spacer(Modifier.height(8.dp))
                FilterBar(
                    scope = scope,
                    spaces = spaces,
                    tags = tags,
                    folders = folders,
                    onFilter = { space, tag, path, done -> vm.filter(repo, space, tag, path, done) },
                )
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
                    rows.isEmpty() -> Placeholder(loading = false, error = null, empty = emptyCopy(scope), onRetry = { vm.load(repo) })
                    else -> {
                        val groups = remember(rows) { groupByNote(rows) }
                        LazyColumn(Modifier.fillMaxSize()) {
                            groups.forEach { g ->
                                item(key = "head:${g.note.id}") {
                                    GroupHead(group = g) { line ->
                                        onNote(g.note.id, g.note.title.ifEmpty { g.note.path }, line)
                                    }
                                }
                                items(g.rows, key = { "${it.note.id}:${it.line}" }) { t ->
                                    TaskRowLine(task = t, onTick = { to -> vm.tick(repo, t, to) }) {
                                        onNote(t.note.id, t.note.title.ifEmpty { t.note.path }, t.line)
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
}

/** The filter row: space, folder, tag, and the open/done toggle. */
@Composable
private fun FilterBar(
    scope: TaskScope,
    spaces: List<Space>,
    tags: List<TagCount>,
    folders: List<String>,
    onFilter: (space: String, tag: String, path: String, done: Boolean) -> Unit,
) {
    val spaceOptions = remember(spaces, scope.space) {
        val names = spaces.map { it.name }.toMutableSet()
        if (scope.space.isNotEmpty()) names.add(scope.space)
        names.toList()
    }
    Row(
        Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()),
        horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        FilterMenu(
            label = "Space",
            value = scope.space.ifEmpty { "Every space" },
            options = listOf("Every space" to "") +
                spaceOptions.map { name -> (spaces.firstOrNull { it.name == name }?.displayName ?: name) to name },
            active = scope.space.isNotEmpty(),
            onPick = { onFilter(it, scope.tag, scope.path, scope.done) },
        )
        FilterMenu(
            label = "Folder",
            value = scope.path.ifEmpty { "Every folder" },
            options = listOf("Every folder" to "") + folders.map { folderLabel(it, scope.space) to it },
            active = scope.path.isNotEmpty(),
            onPick = { onFilter(scope.space, scope.tag, it, scope.done) },
        )
        FilterMenu(
            label = "Tag",
            value = scope.tag.ifEmpty { "Any tag" },
            options = listOf("Any tag" to "") + tags.map { "#${it.tag} (${it.count})" to it.tag },
            active = scope.tag.isNotEmpty(),
            onPick = { onFilter(scope.space, it, scope.path, scope.done) },
        )
        FilterMenu(
            label = "Show",
            value = if (scope.done) "Done, last 30 days" else "Open",
            options = listOf("Open" to "open", "Done, last 30 days" to "done"),
            active = scope.done,
            onPick = { onFilter(scope.space, scope.tag, scope.path, it == "done") },
        )
    }
}

/** One labeled dropdown chip of the filter row. */
@Composable
private fun FilterMenu(
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

/** One note's block header: its title and path, opening the note at its first task. */
@Composable
private fun GroupHead(group: NoteGroup, onOpen: (line: Int) -> Unit) {
    val first = group.rows.firstOrNull()
    Row(
        Modifier.fillMaxWidth()
            .clickable { onOpen(first?.line ?: -1) }
            .padding(start = 20.dp, end = 12.dp, top = 16.dp, bottom = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                group.note.title.ifEmpty { group.note.path },
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                group.note.path,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Text(
            group.rows.size.toString(),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** One task row: the box, the line's styled text, and the heading it sits under. */
@Composable
private fun TaskRowLine(task: TaskRow, onTick: (Boolean) -> Unit, onOpen: () -> Unit) {
    Row(
        Modifier.fillMaxWidth()
            .clickable(onClick = onOpen)
            .padding(start = (20 + task.indent.coerceAtMost(6) * 12).dp, end = 12.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Checkbox(
            checked = task.done,
            onCheckedChange = { onTick(it) },
            modifier = Modifier.width(32.dp).height(32.dp),
        )
        Column(Modifier.weight(1f)) {
            val parsed = remember(task.text) { TaskTextParser.parse(task.text) }
            Text(
                taskAnnotated(parsed),
                style = MaterialTheme.typography.bodyMedium,
                color = if (task.done) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.onSurface,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            if (task.heading.isNotEmpty()) {
                Text(
                    task.heading,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
    }
}

/** A parsed task line as styled text: bold, italics, code, strikethrough. */
private fun taskAnnotated(parsed: TaskTextParser.Parsed): AnnotatedString = buildAnnotatedString {
    append(parsed.text)
    for (s in parsed.spans) {
        val style = when (s.style) {
            TaskTextParser.Style.Bold -> SpanStyle(fontWeight = FontWeight.Bold)
            TaskTextParser.Style.Italic -> SpanStyle(fontStyle = FontStyle.Italic)
            TaskTextParser.Style.Code -> SpanStyle(fontFamily = Mono)
            TaskTextParser.Style.Strike -> SpanStyle(textDecoration = TextDecoration.LineThrough)
        }
        addStyle(style, s.start, s.end.coerceAtMost(parsed.text.length))
    }
}

/** One note's tasks in the server's order. */
private data class NoteGroup(
    val note: TaskNote,
    val rows: List<TaskRow>,
)

/** Groups a listing by note, keeping the server's order. */
private fun groupByNote(rows: List<TaskRow>): List<NoteGroup> {
    val out = ArrayList<NoteGroup>()
    val byNote = LinkedHashMap<String, NoteGroup>()
    for (t in rows) {
        val g = byNote.getOrPut(t.note.id) { NoteGroup(t.note, emptyList()).also(out::add) }
        byNote[t.note.id] = g.copy(rows = g.rows + t)
    }
    return out
}

private fun subtitle(scope: TaskScope, count: Int?): String {
    val place = when {
        scope.space.isNotEmpty() && scope.path.isNotEmpty() -> "under ${scope.space}/${scope.path}"
        scope.space.isNotEmpty() -> "in ${scope.space}"
        else -> "across your spaces"
    }
    val what = if (scope.done) "Boxes completed" else "Open boxes"
    val n = if (count != null && !scope.done) " — $count" else ""
    return "$what $place$n"
}

private fun emptyCopy(scope: TaskScope): String = when {
    scope.done -> "Nothing was completed here in the last 30 days."
    scope.tag.isNotEmpty() || scope.path.isNotEmpty() -> "No open boxes here by that filter."
    else -> "No open boxes. Write `- [ ]` on a line to make one."
}

/** A folder filter's label, relative to the space when one is picked. */
private fun folderLabel(dir: String, space: String): String =
    (if (space.isNotEmpty() && dir.startsWith("$space/")) dir.substring(space.length + 1) else dir) + "/"

/** A fetch time as an age: just now, minutes, hours, days. */
internal fun ageOf(epochMs: Long, now: Long = System.currentTimeMillis()): String {
    val mins = ((now - epochMs) / 60_000).toInt()
    return when {
        mins < 1 -> "just now"
        mins < 60 -> "${mins}m ago"
        mins < 60 * 24 -> "${mins / 60}h ago"
        else -> "${mins / (60 * 24)}d ago"
    }
}
