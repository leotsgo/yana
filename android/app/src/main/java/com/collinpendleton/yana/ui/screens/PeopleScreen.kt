package com.collinpendleton.yana.ui.screens

import android.widget.Toast
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
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
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.CreateUserRequest
import com.collinpendleton.yana.data.PasswordRequest
import com.collinpendleton.yana.data.UserInfo
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.formatTime
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * The server's accounts, the owner's view: add a person with a starting
 * password, reset one, or remove one. The server refuses everything
 * here for anyone else, so the screen does not even open for them.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun PeopleScreen(
    app: YanaApp,
    onBack: () -> Unit,
) {
    val client = app.client
    val session by client.session.collectAsStateWithLifecycle()
    if (session?.isOwner != true) {
        Scaffold(
            topBar = {
                TopAppBar(
                    title = { Text("People") },
                    navigationIcon = {
                        IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                    },
                    colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
                )
            },
        ) { pad ->
            Placeholder(
                loading = false,
                error = null,
                empty = "The server's owner manages people. Ask them for the role you need.",
                onRetry = {},
                modifier = Modifier.padding(pad),
            )
        }
        return
    }

    val vm: Loader<List<UserInfo>> = viewModel { Loader(fetch = { client.api().users().users }) }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }
    var adding by remember { mutableStateOf(false) }
    var resetting by remember { mutableStateOf<UserInfo?>(null) }
    var removing by remember { mutableStateOf<UserInfo?>(null) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("People") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                actions = {
                    IconButton(onClick = { adding = true }) { Icon(Icons.Default.Add, contentDescription = "Add a person") }
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
            val people = state.data
            when {
                people == null -> Placeholder(loading = state.loading, error = state.error, empty = null, onRetry = { vm.reload() })
                people.isEmpty() -> Placeholder(loading = false, error = null, empty = "No accounts but yours.", onRetry = { vm.reload() })
                else -> LazyColumn(Modifier.fillMaxSize()) {
                    items(people, key = { it.id }) { p ->
                        PersonRow(p, isSelf = p.id == session?.userId, onReset = { resetting = p }, onRemove = { removing = p })
                        HorizontalDivider(Modifier.padding(start = 20.dp), color = MaterialTheme.colorScheme.outlineVariant)
                    }
                    state.error?.let { item { ErrorLine(it) } }
                    item { Spacer(Modifier.height(16.dp)) }
                }
            }
        }
    }

    if (adding) {
        var username by remember { mutableStateOf("") }
        var password by remember { mutableStateOf("") }
        val weak = password.isNotEmpty() && password.length < 8
        AlertDialog(
            onDismissRequest = { if (!busy) adding = false },
            title = { Text("Add a person") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    OutlinedTextField(
                        value = username,
                        onValueChange = { username = it },
                        label = { Text("Username") },
                        singleLine = true,
                    )
                    OutlinedTextField(
                        value = password,
                        onValueChange = { password = it },
                        label = { Text("Starting password") },
                        isError = weak,
                        supportingText = if (weak) ({ Text("At least 8 characters.") }) else null,
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    )
                }
            },
            confirmButton = {
                TextButton(
                    enabled = !busy && username.isNotBlank() && password.length >= 8,
                    onClick = {
                        busy = true
                        scope.launch {
                            try {
                                client.api().createUser(CreateUserRequest(username.trim(), password))
                                adding = false
                                vm.reload(pull = true)
                                toast("Added ${username.trim()}.")
                            } catch (e: CancellationException) {
                                throw e
                            } catch (e: Exception) {
                                toast(e.userMessage())
                            } finally {
                                busy = false
                            }
                        }
                    },
                ) { Text("Add") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { adding = false }) { Text("Cancel") } },
        )
    }

    resetting?.let { p ->
        var password by remember { mutableStateOf("") }
        val weak = password.isNotEmpty() && password.length < 8
        AlertDialog(
            onDismissRequest = { if (!busy) resetting = null },
            title = { Text("Reset ${p.username}'s password?") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Text("Their other devices sign out; this one keeps its session.")
                    OutlinedTextField(
                        value = password,
                        onValueChange = { password = it },
                        label = { Text("New password") },
                        isError = weak,
                        supportingText = if (weak) ({ Text("At least 8 characters.") }) else null,
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    )
                }
            },
            confirmButton = {
                TextButton(
                    enabled = !busy && password.length >= 8,
                    onClick = {
                        busy = true
                        scope.launch {
                            try {
                                client.api().setPassword(p.id, PasswordRequest(password))
                                resetting = null
                                toast("Password reset for ${p.username}.")
                            } catch (e: CancellationException) {
                                throw e
                            } catch (e: Exception) {
                                toast(e.userMessage())
                            } finally {
                                busy = false
                            }
                        }
                    },
                ) { Text("Reset") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { resetting = null }) { Text("Cancel") } },
        )
    }

    removing?.let { p ->
        AlertDialog(
            onDismissRequest = { if (!busy) removing = null },
            title = { Text("Remove ${p.username}?") },
            text = { Text("Their sessions end and their account is gone. Notes they wrote stay.") },
            confirmButton = {
                TextButton(
                    enabled = !busy,
                    onClick = {
                        busy = true
                        scope.launch {
                            try {
                                client.api().deleteUser(p.id)
                                removing = null
                                vm.reload(pull = true)
                                toast("Removed ${p.username}.")
                            } catch (e: CancellationException) {
                                throw e
                            } catch (e: Exception) {
                                toast(e.userMessage())
                            } finally {
                                busy = false
                            }
                        }
                    },
                ) { Text("Remove") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { removing = null }) { Text("Cancel") } },
        )
    }
}

/** One account: its name, when it was made, and the owner's two actions on it. */
@Composable
private fun PersonRow(p: UserInfo, isSelf: Boolean, onReset: () -> Unit, onRemove: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().padding(start = 20.dp, end = 12.dp, top = 12.dp, bottom = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                p.username + if (p.isOwner) " · the server's owner" else "",
                style = MaterialTheme.typography.titleSmall,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                if (p.createdAt.isNotEmpty()) "added ${formatTime(p.createdAt)}" else "",
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (!isSelf) {
            TextButton(onClick = onReset) { Text("Password") }
            TextButton(onClick = onRemove) { Text("Remove") }
        }
    }
}
