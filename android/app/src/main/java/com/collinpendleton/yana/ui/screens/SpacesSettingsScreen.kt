package com.collinpendleton.yana.ui.screens

import android.widget.Toast
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.filled.Add
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.material3.pulltorefresh.PullToRefreshBox
import androidx.compose.runtime.Composable
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
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.CreateSpaceRequest
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.launch

/** One row of the sharing list: the space beside the role this account holds in it. */
data class SpaceRoleRow(val space: Space, val role: String)

/**
 * Every space this account can see with the role it holds in each, and
 * the making of new ones. Renaming and membership live one tap down, in
 * the space's own screen.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SpacesSettingsScreen(
    app: YanaApp,
    onBack: () -> Unit,
    onSpace: (name: String, label: String) -> Unit,
) {
    val client = app.client
    val vm: Loader<List<SpaceRoleRow>> = viewModel {
        Loader(fetch = { spacesWithRoles(client.api().spaces().spaces) { client.api().spaceDetail(it) } })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }
    var creating by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Spaces and sharing") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                actions = {
                    IconButton(onClick = { creating = true }) { Icon(Icons.Default.Add, contentDescription = "Make a space") }
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
            val rows = state.data
            when {
                rows == null -> Placeholder(loading = state.loading, error = state.error, empty = null, onRetry = { vm.reload() })
                rows.isEmpty() -> Placeholder(loading = false, error = null, empty = "No spaces yet; make one with the plus.", onRetry = { vm.reload() })
                else -> LazyColumn(Modifier.fillMaxSize()) {
                    items(rows, key = { it.space.name }) { row ->
                        SpaceRoleListRow(row) { onSpace(row.space.name, row.space.displayName) }
                        HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                    }
                    state.error?.let { item { ErrorLine(it) } }
                    item { Spacer(Modifier.height(16.dp)) }
                }
            }
        }
    }

    if (creating) {
        var name by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { if (!busy) creating = false },
            title = { Text("Make a space") },
            text = {
                Column {
                    OutlinedTextField(
                        value = name,
                        onValueChange = { name = it },
                        label = { Text("Name") },
                        singleLine = true,
                    )
                    Text(
                        "One word of letters, digits, dash or underscore — it names the space's folder. You become its owner.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
            },
            confirmButton = {
                TextButton(
                    enabled = !busy && name.isNotBlank(),
                    onClick = {
                        busy = true
                        scope.launch {
                            try {
                                client.api().createSpace(CreateSpaceRequest(name.trim()))
                                creating = false
                                vm.reload(pull = true)
                                toast("Made ${name.trim()}.")
                            } catch (e: CancellationException) {
                                throw e
                            } catch (e: Exception) {
                                toast(e.userMessage())
                            } finally {
                                busy = false
                            }
                        }
                    },
                ) { Text("Make") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { creating = false }) { Text("Cancel") } },
        )
    }
}

@Composable
private fun SpaceRoleListRow(row: SpaceRoleRow, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(start = 20.dp, end = 12.dp, top = 14.dp, bottom = 14.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(row.space.displayName, style = MaterialTheme.typography.titleSmall)
            Text(
                roleLine(row),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

private fun roleLine(row: SpaceRoleRow): String {
    val notes = if (row.space.notes == 1) "1 note" else "${row.space.notes} notes"
    val role = when (row.role) {
        "owner", "editor", "viewer" -> "you are its ${row.role}"
        else -> "your role here is unknown"
    }
    return "$role · $notes"
}

/**
 * The spaces beside the role this account holds in each. The role is
 * one small read per space — the same one the web's sharing page makes
 * — and a space that will not say leaves the row with an honest
 * unknown rather than taking the list down.
 */
internal suspend fun spacesWithRoles(spaces: List<Space>, detail: suspend (String) -> com.collinpendleton.yana.data.SpaceDetail): List<SpaceRoleRow> =
    coroutineScope {
        spaces.map { s ->
            async {
                val role = if (s.name.isEmpty()) {
                    // Only the owner's list carries the root, and the root is the owner's.
                    "owner"
                } else {
                    runCatching { detail(s.name).role }.getOrDefault("")
                }
                SpaceRoleRow(s, role)
            }
        }.awaitAll()
    }
