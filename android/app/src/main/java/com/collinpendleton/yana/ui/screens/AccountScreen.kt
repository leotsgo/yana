package com.collinpendleton.yana.ui.screens

import android.widget.Toast
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
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
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.PasswordRequest
import com.collinpendleton.yana.data.SessionInfo
import com.collinpendleton.yana.data.normalizeServerUrl
import com.collinpendleton.yana.data.display
import com.collinpendleton.yana.data.parseInstant
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.formatTime
import java.time.Instant
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * The account as a phone handles it: the password, every device
 * signed in with its label, and the revoke that ends any of them.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AccountScreen(
    app: YanaApp,
    onBack: () -> Unit,
) {
    val client = app.client
    val session by client.session.collectAsStateWithLifecycle()
    val vm: Loader<List<SessionInfo>> = viewModel { Loader(fetch = { client.api().sessions().sessions }) }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }
    var changing by remember { mutableStateOf(false) }
    var revoking by remember { mutableStateOf<SessionInfo?>(null) }
    var busy by remember { mutableStateOf(false) }
    var confirming by remember { mutableStateOf(false) }
    var signingOut by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Account") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
            )
        },
    ) { pad ->
        Column(
            Modifier.padding(pad).fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Column(Modifier.widthIn(max = 560.dp).fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                session?.let { s ->
                    Section("Signed in as")
                    Field("Account", s.username + if (s.isOwner) ", the server's owner" else "")
                    Field("Server", normalizeServerUrl(s.server)?.display() ?: s.server)
                    state.data?.firstOrNull { it.current }?.let {
                        Field("This device", it.label.ifEmpty { "unnamed" })
                    }
                    OutlinedButton(onClick = { changing = true }) { Text("Change password") }
                    Text(
                        "Changing it signs out every other device; this one stays.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }

                HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                Section("Devices")
                Text(
                    "Every device signed in as this account, newest first. Revoking one signs it out on its next request.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                val list = state.data?.let { liveSessions(it) }
                when {
                    list == null && state.loading -> Row(Modifier.fillMaxWidth().padding(top = 8.dp), horizontalArrangement = Arrangement.Center) {
                        CircularProgressIndicator(Modifier.size(22.dp))
                    }
                    list == null && state.error != null -> {
                        Text(state.error!!, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                        TextButton(onClick = { vm.reload() }) { Text("Try again") }
                    }
                    list != null -> list.forEach { s ->
                        SessionRow(s) { revoking = s }
                        HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                    }
                }

                HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                Text(
                    "Signing out ends this device's session on the server. Other devices stay signed in.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                OutlinedButton(onClick = { confirming = true }, enabled = !signingOut) { Text("Sign out") }
                Spacer(Modifier.padding(bottom = 8.dp))
            }
        }
    }

    if (changing) {
        var password by remember { mutableStateOf("") }
        var confirm by remember { mutableStateOf("") }
        val weak = password.isNotEmpty() && password.length < 8
        val differs = confirm.isNotEmpty() && confirm != password
        AlertDialog(
            onDismissRequest = { if (!busy) changing = false },
            title = { Text("Change password") },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    OutlinedTextField(
                        value = password,
                        onValueChange = { password = it },
                        label = { Text("New password") },
                        isError = weak,
                        supportingText = if (weak) ({ Text("At least 8 characters.") }) else null,
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    )
                    OutlinedTextField(
                        value = confirm,
                        onValueChange = { confirm = it },
                        label = { Text("Again") },
                        isError = differs,
                        supportingText = if (differs) ({ Text("The two do not match.") }) else null,
                        singleLine = true,
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                    )
                }
            },
            confirmButton = {
                TextButton(
                    enabled = !busy && password.length >= 8 && password == confirm,
                    onClick = {
                        busy = true
                        val id = session?.userId ?: return@TextButton
                        scope.launch {
                            try {
                                client.api().setPassword(id, PasswordRequest(password))
                                changing = false
                                toast("Password changed. Other devices are signed out.")
                            } catch (e: CancellationException) {
                                throw e
                            } catch (e: Exception) {
                                toast(e.userMessage())
                            } finally {
                                busy = false
                            }
                        }
                    },
                ) { Text("Change") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { changing = false }) { Text("Cancel") } },
        )
    }

    revoking?.let { s ->
        AlertDialog(
            onDismissRequest = { if (!busy) revoking = null },
            title = { Text(if (s.current) "Sign this device out?" else "Sign out ${s.label.ifEmpty { "this device" }}?") },
            text = {
                Text(
                    if (s.current) {
                        "The session this device holds ends now; notes stay on the server."
                    } else {
                        "That device signs out on its next request."
                    },
                )
            },
            confirmButton = {
                TextButton(
                    enabled = !busy,
                    onClick = {
                        busy = true
                        scope.launch {
                            try {
                                client.api().revokeSession(s.id)
                                if (s.current) {
                                    revoking = null
                                    client.signOut()
                                } else {
                                    revoking = null
                                    vm.reload(pull = true)
                                    toast("Signed out ${s.label.ifEmpty { "that device" }}.")
                                }
                            } catch (e: CancellationException) {
                                throw e
                            } catch (e: Exception) {
                                revoking = null
                                toast(e.userMessage())
                            } finally {
                                busy = false
                            }
                        }
                    },
                ) { Text("Sign out") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { revoking = null }) { Text("Cancel") } },
        )
    }

    if (confirming) {
        AlertDialog(
            onDismissRequest = { confirming = false },
            title = { Text("Sign out of this device?") },
            text = { Text("Notes stay on the server. Sign in again to see them here.") },
            confirmButton = {
                TextButton(onClick = {
                    confirming = false
                    signingOut = true
                    scope.launch { client.signOut() }
                }) { Text("Sign out") }
            },
            dismissButton = { TextButton(onClick = { confirming = false }) { Text("Cancel") } },
        )
    }
}

/** One signed-in device: its label, when it signed in, when it was last used. */
@Composable
private fun SessionRow(s: SessionInfo, onRevoke: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(
                s.label.ifEmpty { "unnamed" } + if (s.current) " · this device" else "",
                style = MaterialTheme.typography.titleSmall,
            )
            val used = s.lastUsedAt.ifEmpty { s.createdAt }
            Text(
                buildString {
                    if (used.isNotEmpty()) append("last used ").append(formatTime(used))
                    if (s.createdAt.isNotEmpty()) {
                        if (isNotEmpty()) append(" · ")
                        append("signed in ").append(formatTime(s.createdAt))
                    }
                },
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        TextButton(onClick = onRevoke) { Text("Revoke") }
    }
}

/** The sessions worth listing: not revoked, not lapsed — the web's same filter. */
fun liveSessions(sessions: List<SessionInfo>, now: Instant = Instant.now()): List<SessionInfo> =
    sessions.filter { s ->
        val expires = s.expiresAt.takeIf { it.isNotEmpty() }?.let { parseInstant(it) }
        s.revokedAt == null && (expires == null || expires.isAfter(now))
    }

@Composable
internal fun Section(title: String) {
    Text(title, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
}

@Composable
internal fun Field(label: String, value: String) {
    Column {
        Text(label, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(value, style = MaterialTheme.typography.bodyLarge)
    }
}
