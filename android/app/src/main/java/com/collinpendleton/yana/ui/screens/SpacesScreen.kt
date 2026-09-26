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
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
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
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.R
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.AppendOutcome
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.TaskCount
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.Wordmark
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
    var captureBusy by remember { mutableStateOf(false) }

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

    fun capture(line: String) {
        val text = line.trim()
        if (text.isEmpty() || captureBusy) return
        captureBusy = true
        scope.launch {
            when (val outcome = app.capture.captureLine(text)) {
                is AppendOutcome.Done -> {
                    captureOpen = false
                    Toast.makeText(
                        context,
                        if (outcome.local) app.getString(R.string.capture_added_local)
                        else app.getString(R.string.capture_added),
                        Toast.LENGTH_SHORT,
                    ).show()
                }
                AppendOutcome.NotOnDevice ->
                    Toast.makeText(context, app.getString(R.string.capture_not_on_device), Toast.LENGTH_SHORT).show()
                AppendOutcome.NotFound ->
                    Toast.makeText(context, app.getString(R.string.capture_no_space), Toast.LENGTH_SHORT).show()
            }
            captureBusy = false
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
        CaptureDialog(busy = captureBusy, onAdd = { capture(it) }, onDismiss = { captureOpen = false })
    }
    Scaffold(
        topBar = {
            TopAppBar(
                title = { Wordmark() },
                actions = {
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

/** The capture prompt: one field, Enter to add, the web's palette prompt. */
@Composable
private fun CaptureDialog(busy: Boolean, onAdd: (String) -> Unit, onDismiss: () -> Unit) {
    var line by remember { mutableStateOf("") }
    androidx.compose.material3.AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.capture_title)) },
        text = {
            OutlinedTextField(
                value = line,
                onValueChange = { line = it },
                placeholder = { Text(stringResource(R.string.capture_hint)) },
                supportingText = { Text(stringResource(R.string.capture_support)) },
                singleLine = true,
                keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(imeAction = ImeAction.Done),
                keyboardActions = androidx.compose.foundation.text.KeyboardActions(onDone = { onAdd(line) }),
                enabled = !busy,
                modifier = Modifier.fillMaxWidth(),
            )
        },
        confirmButton = {
            TextButton(onClick = { onAdd(line) }, enabled = line.isNotBlank() && !busy) {
                Text(stringResource(R.string.capture_add))
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(android.R.string.cancel)) }
        },
    )
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
