package com.collinpendleton.yana.ui.screens

import android.widget.Toast
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
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.Settings
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.ui.ShellInsets
import com.collinpendleton.yana.R
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.TaskCount
import com.collinpendleton.yana.ui.CaptureLineDialog
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.BrandMark
import com.collinpendleton.yana.ui.YanaIcons
import com.collinpendleton.yana.ui.activity.ActivityRow
import com.collinpendleton.yana.ui.activity.FeedWindow
import com.collinpendleton.yana.ui.activity.mergePages
import com.collinpendleton.yana.ui.activity.sinceIso
import com.collinpendleton.yana.ui.activity.whatsChangedText
import kotlinx.coroutines.launch

/** Home: the spaces this account can see. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SpacesScreen(
    app: YanaApp,
    onSpace: (Space) -> Unit,
    onSearch: () -> Unit,
    onSettings: () -> Unit,
    onTasks: () -> Unit = {},
    onActivity: () -> Unit = {},
    onNote: (id: String, title: String) -> Unit = { _, _ -> },
    onSwitcher: () -> Unit = {},
    onTags: () -> Unit = {},
) {
    val repo = app.repo
    val scope = rememberCoroutineScope()
    val context = LocalContext.current
    // The replica answers if the network cannot; pull-to-refresh syncs.
    val vm: Loader<List<Space>> = viewModel {
        Loader(fetch = { repo.spaces() }, refetch = { repo.sync(); repo.spaces() })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()

    // Today and Capture, the two rows the phone opens most: one makes
    // or opens the daily note, the other takes a line onto its end
    // without opening anything.
    var openingToday by remember { mutableStateOf(false) }
    var captureOpen by remember { mutableStateOf(false) }

    fun openToday() {
        if (openingToday) return
        openingToday = true
        scope.launch {
            val today = app.capture.todayNote()
            openingToday = false
            if (today == null) {
                Toast.makeText(context, app.getString(R.string.today_failed), Toast.LENGTH_SHORT).show()
            } else {
                onNote(today.id, today.path.substringAfterLast('/').removeSuffix(".md"))
            }
        }
    }

    // The open-task count that feeds the home screen; the cached count
    // answers offline, with whatever age it has.
    val countVm: Loader<TaskCount?> = viewModel(key = "task-count") {
        Loader(fetch = { repo.openTaskCount() })
    }
    val countState by countVm.loaded.collectAsStateWithLifecycle()

    // The "What changed" line: what landed since this device last
    // looked at the feed. It reads the history but never marks
    // anything seen; opening the feed does that. With history off it
    // stays quiet.
    val spaces = state.data
    val recents by app.prefs.recents.collectAsStateWithLifecycle()
    val pins by app.prefs.pins.collectAsStateWithLifecycle()
    var whats by remember { mutableStateOf<List<ActivityRow>?>(null) }
    LaunchedEffect(spaces?.map { it.name }.orEmpty().joinToString("\u0000")) {
        val names = spaces.orEmpty().map { it.name }.filter { it.isNotEmpty() }.take(8)
        if (names.isEmpty()) {
            whats = null
        } else {
            val since = sinceIso(FeedWindow.Seen, app.prefs.activitySeen())
            whats = runCatching {
                mergePages(names.map { it to repo.activity(space = it, since = since, limit = 30) }).rows
            }.getOrNull()
        }
    }

    if (captureOpen) {
        CaptureLineDialog(app) { captureOpen = false }
    }
    Scaffold(
        contentWindowInsets = ShellInsets,
        topBar = {
            TopAppBar(
                title = { BrandMark(logo = 28.dp, size = 20.sp) },
                actions = {
                    IconButton(onClick = onSwitcher) { Icon(YanaIcons.Command, contentDescription = "Switcher") }
                    IconButton(onClick = onSearch) { Icon(Icons.Default.Search, contentDescription = "Search") }
                    IconButton(onClick = onSettings) { Icon(Icons.Default.Settings, contentDescription = "Settings") }
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
            )
        },
    ) { pad ->
        PullToRefreshBox(
            isRefreshing = state.refreshing,
            onRefresh = {
                vm.reload(pull = true)
                countVm.reload(pull = true)
            },
            modifier = Modifier.padding(pad).fillMaxSize(),
        ) {
            if (spaces.isNullOrEmpty()) {
                Placeholder(
                    loading = state.loading,
                    error = state.error,
                    empty = "No spaces yet. Make one in settings on the web and it shows up here.",
                    onRetry = { vm.reload() },
                )
            } else {
                LazyColumn(Modifier.fillMaxSize()) {
                    whats?.let { rows ->
                        item {
                            WhatsChangedRow(text = whatsChangedText(rows, app.prefs.activitySeen())) { onActivity() }
                            HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                        }
                    }
                    item {
                        TodayRow(opening = openingToday) { openToday() }
                        HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                    }
                    item {
                        CaptureRow { captureOpen = true }
                        HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                    }
                    item {
                        TasksRow(count = countState.data) { onTasks() }
                        HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                    }
                    item {
                        TagsRow { onTags() }
                        HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                    }
                    if (pins.isNotEmpty()) {
                        item { SectionLabel("Pinned") }
                        items(pins, key = { "pin:" + it.id }) { p ->
                            RecentRow(p.title) { onNote(p.id, p.title) }
                        }
                    }
                    if (recents.isNotEmpty()) {
                        item { SectionLabel("Recent") }
                        items(recents.take(RECENTS_ON_HOME), key = { "recent:" + it.id }) { r ->
                            RecentRow(r.title) { onNote(r.id, r.title) }
                        }
                    }
                    item {
                        Text(
                            "Spaces",
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(start = 20.dp, top = 8.dp, bottom = 4.dp),
                        )
                    }
                    items(spaces, key = { it.name }) { space ->
                        SpaceRow(space) { onSpace(space) }
                        HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                    }
                    state.error?.let { item { ErrorLine(it) } }
                }
            }
        }
    }
}

private const val RECENTS_ON_HOME = 5

@Composable
private fun SectionLabel(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.labelMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(start = 20.dp, top = 8.dp, bottom = 4.dp),
    )
}

/** A note opened lately: one tap back into it, no tree to walk. */
@Composable
private fun RecentRow(title: String, onClick: () -> Unit) {
    Text(
        title,
        style = MaterialTheme.typography.bodyLarge,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
        modifier = Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 20.dp, vertical = 11.dp),
    )
}

/** The feed's entry from home: what changed since this device last looked. */
@Composable
private fun WhatsChangedRow(text: String, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(start = 20.dp, end = 12.dp, top = 14.dp, bottom = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Surface(shape = MaterialTheme.shapes.small, color = MaterialTheme.colorScheme.primaryContainer) {
            Icon(
                YanaIcons.History,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onPrimaryContainer,
                modifier = Modifier.padding(5.dp),
            )
        }
        Spacer(Modifier.width(12.dp))
        Text(
            text,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurface,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** The tasks entry: every open box across the spaces, counted. */
@Composable
private fun TasksRow(count: TaskCount?, onClick: () -> Unit) {    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(start = 20.dp, end = 12.dp, top = 14.dp, bottom = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Surface(shape = MaterialTheme.shapes.small, color = MaterialTheme.colorScheme.primaryContainer) {
            Icon(
                Icons.Default.Check,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onPrimaryContainer,
                modifier = Modifier.padding(5.dp),
            )
        }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text("Tasks", style = MaterialTheme.typography.titleMedium)
            Text(
                when (val n = count?.count) {
                    null -> "Every open box across your spaces"
                    0 -> "Nothing open"
                    1 -> "1 open box"
                    else -> "$n open boxes"
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** Today: the daily note, opened or made. */
@Composable
private fun TodayRow(opening: Boolean, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick, enabled = !opening).padding(start = 20.dp, end = 12.dp, top = 14.dp, bottom = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Surface(shape = MaterialTheme.shapes.small, color = MaterialTheme.colorScheme.primaryContainer) {
            Icon(
                YanaIcons.Calendar,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onPrimaryContainer,
                modifier = Modifier.padding(5.dp),
            )
        }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text("Today", style = MaterialTheme.typography.titleMedium)
            Text(
                if (opening) "Opening…" else "Today's note, made if it is not there yet",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** Capture: one line onto today's note, without opening it. */
@Composable
private fun CaptureRow(onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(start = 20.dp, end = 12.dp, top = 14.dp, bottom = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Surface(shape = MaterialTheme.shapes.small, color = MaterialTheme.colorScheme.primaryContainer) {
            Icon(
                YanaIcons.Zap,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onPrimaryContainer,
                modifier = Modifier.padding(5.dp),
            )
        }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text("Capture", style = MaterialTheme.typography.titleMedium)
            Text(
                "One line onto the end of today's note",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** The tags page's entry: every #tag and the notes carrying it. */
@Composable
private fun TagsRow(onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(start = 20.dp, end = 12.dp, top = 14.dp, bottom = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Surface(shape = MaterialTheme.shapes.small, color = MaterialTheme.colorScheme.primaryContainer) {
            Text(
                "#",
                style = MaterialTheme.typography.titleMedium,
                color = MaterialTheme.colorScheme.onPrimaryContainer,
                modifier = Modifier.padding(horizontal = 9.dp, vertical = 1.dp),
            )
        }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text("Tags", style = MaterialTheme.typography.titleMedium)
            Text(
                "Every #tag and the notes carrying it",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
private fun SpaceRow(space: Space, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(start = 20.dp, end = 12.dp, top = 14.dp, bottom = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(space.displayName, style = MaterialTheme.typography.titleMedium)
            Text(
                if (space.name.isEmpty()) notes(space.notes) + " in the root" else notes(space.notes),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

@Composable
internal fun ErrorLine(message: String) {
    Text(
        message,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.error,
        modifier = Modifier.padding(20.dp),
    )
}

internal fun notes(n: Int) = if (n == 1) "1 note" else "$n notes"
