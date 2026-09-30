package com.collinpendleton.yana.ui.screens

import android.widget.Toast
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
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
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
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
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.MemberSpec
import com.collinpendleton.yana.data.SpaceDetail
import com.collinpendleton.yana.data.SpaceMemberRow
import com.collinpendleton.yana.data.UpdateSpaceRequest
import com.collinpendleton.yana.data.UserInfo
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.Loader
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** The roles a member can hold: the server's three, in rising order. */
val MemberRoles = listOf("viewer", "editor", "owner")

/**
 * One space's sharing: its label and the member list for a space
 * owner, and a plain read of your own role for everyone else. Every
 * save writes the label and the whole member list at once — the one
 * write the server takes.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SpaceSettingsScreen(
    app: YanaApp,
    space: String,
    label: String,
    onBack: () -> Unit,
) {
    val client = app.client
    val session by client.session.collectAsStateWithLifecycle()
    val root = space.isEmpty()
    val vm: Loader<SpaceDetail> = viewModel(key = space) {
        Loader(fetch = { if (root) SpaceDetail(name = "", label = "/") else client.api().spaceDetail(space) })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val detail = state.data
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }
    var renaming by remember { mutableStateOf(false) }
    var editing by remember { mutableStateOf<SpaceMemberRow?>(null) }
    var removing by remember { mutableStateOf<SpaceMemberRow?>(null) }
    var adding by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    /** Saves the label with whatever member list it is given. */
    fun save(label: String, members: List<SpaceMemberRow>, then: () -> Unit) {
        busy = true
        scope.launch {
            try {
                client.api().updateSpace(space, UpdateSpaceRequest(label, members.map { MemberSpec(it.username?.ifEmpty { null } ?: it.user, it.role) }))
                then()
                vm.reload(pull = true)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                toast(e.userMessage())
            } finally {
                busy = false
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(if (root) "The root" else detail?.label?.ifEmpty { label } ?: label, maxLines = 1, overflow = TextOverflow.Ellipsis) },
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
                if (detail == null && state.loading) {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.Center) { CircularProgressIndicator() }
                } else if (detail == null) {
                    Text(state.error ?: "", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                    TextButton(onClick = { vm.reload() }) { Text("Try again") }
                } else {
                    Section("Space")
                    Field("Folder", if (root) "/ — the root of the tree" else space)
                    Field("Your role", detail.role.ifEmpty { "unknown" })
                    if (!root && detail.label.isNotEmpty()) Field("Label", detail.label)

                    if (root) {
                        HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                        Text(
                            "The root of the tree belongs to the server's owner and everyone the owner is. " +
                                "Make a space to share a folder of it with someone.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    } else if (detail.role != "owner") {
                        HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                        Text(
                            "Only a space owner changes a space's label or members. An editor writes; a viewer reads.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    } else {
                        HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                            Text("Members", style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            TextButton(enabled = !busy, onClick = { adding = true }) { Text("Add") }
                        }
                        if (detail.members.isEmpty()) {
                            Text(
                                "No members are listed. Add yourself by name to say who owns the space outright.",
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                        detail.members.forEach { m ->
                            MemberRow(m, onEdit = { editing = m }, onRemove = { removing = m })
                            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                        }
                        Text(
                            "A viewer reads, an editor writes, an owner shares. Saving writes the whole list at once.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(top = 8.dp),
                        )
                        Spacer(Modifier.height(8.dp))
                        OutlinedButton(enabled = !busy, onClick = { renaming = true }) { Text("Rename") }
                    }
                    Spacer(Modifier.height(8.dp))
                }
            }
        }
    }

    if (renaming && detail != null) {
        var text by remember { mutableStateOf(detail.label.ifEmpty { label }) }
        AlertDialog(
            onDismissRequest = { if (!busy) renaming = false },
            title = { Text("Rename ${detail.label.ifEmpty { space }}") },
            text = {
                Column {
                    OutlinedTextField(value = text, onValueChange = { text = it }, label = { Text("Label") }, singleLine = true)
                    Text(
                        "The label the space shows. Its folder keeps the name it has.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
            },
            confirmButton = {
                TextButton(
                    enabled = !busy && text.isNotBlank(),
                    onClick = { save(text.trim(), detail.members) { renaming = false } },
                ) { Text("Save") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { renaming = false }) { Text("Cancel") } },
        )
    }

    editing?.let { m ->
        var role by remember(m) { mutableStateOf(if (m.role in MemberRoles) m.role else "viewer") }
        AlertDialog(
            onDismissRequest = { if (!busy) editing = null },
            title = { Text(m.displayName) },
            text = {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    Text("What ${m.displayName} may do in this space.")
                    SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                        MemberRoles.forEachIndexed { i, r ->
                            SegmentedButton(
                                selected = role == r,
                                onClick = { role = r },
                                shape = SegmentedButtonDefaults.itemShape(i, MemberRoles.size),
                            ) { Text(r) }
                        }
                    }
                }
            },
            confirmButton = {
                TextButton(
                    enabled = !busy && role != m.role,
                    onClick = {
                        detail?.let { d ->
                            save(d.label, d.members.map { if (it.user == m.user && it.id == m.id) it.copy(role = role) else it }) { editing = null }
                        }
                    },
                ) { Text("Save") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { editing = null }) { Text("Cancel") } },
        )
    }

    removing?.let { m ->
        AlertDialog(
            onDismissRequest = { if (!busy) removing = null },
            title = { Text("Remove ${m.displayName}?") },
            text = { Text("They keep every other space they belong to; this one stops appearing for them.") },
            confirmButton = {
                TextButton(
                    enabled = !busy,
                    onClick = {
                        detail?.let { d ->
                            save(d.label, d.members.filter { !(it.user == m.user && it.id == m.id) }) { removing = null }
                        }
                    },
                ) { Text("Remove") }
            },
            dismissButton = { TextButton(enabled = !busy, onClick = { removing = null }) { Text("Cancel") } },
        )
    }

    if (adding && detail != null) {
        AddMemberDialog(
            isServerOwner = session?.isOwner == true,
            busy = busy,
            existing = detail.members.map { it.displayName.lowercase() }.toSet(),
            onPickUser = { client.api().users().users },
            onAdd = { name, role ->
                save(detail.label, detail.members + SpaceMemberRow(user = name, role = role)) { adding = false }
            },
            onDismiss = { adding = false },
        )
    }
}

/** Adding a member: a username — picked from the server's accounts when the owner does it — and a role. */
@Composable
private fun AddMemberDialog(
    isServerOwner: Boolean,
    busy: Boolean,
    existing: Set<String>,
    onPickUser: suspend () -> List<UserInfo>,
    onAdd: (name: String, role: String) -> Unit,
    onDismiss: () -> Unit,
) {
    var name by remember { mutableStateOf("") }
    var role by remember { mutableStateOf("viewer") }
    var people by remember { mutableStateOf<List<UserInfo>?>(null) }
    if (isServerOwner && people == null) {
        LaunchedEffect(Unit) {
            people = runCatching { onPickUser().sortedBy { it.username.lowercase() } }.getOrNull()
        }
    }
    val suggestions = people.orEmpty()
        .filter { it.username.lowercase().contains(name.trim().lowercase()) && it.username.lowercase() !in existing }
        .take(5)
    AlertDialog(
        onDismissRequest = { if (!busy) onDismiss() },
        title = { Text("Add a member") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("Username") },
                    singleLine = true,
                )
                if (suggestions.isNotEmpty()) {
                    Column(Modifier.heightIn(max = 180.dp)) {
                        suggestions.forEach { p ->
                            Row(
                                Modifier.fillMaxWidth().clickable { name = p.username }.padding(vertical = 8.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Text(
                                    p.username + if (p.isOwner) " · the server's owner" else "",
                                    style = MaterialTheme.typography.bodyMedium,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                )
                            }
                            HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                        }
                    }
                }
                SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                    MemberRoles.forEachIndexed { i, r ->
                        SegmentedButton(
                            selected = role == r,
                            onClick = { role = r },
                            shape = SegmentedButtonDefaults.itemShape(i, MemberRoles.size),
                        ) { Text(r) }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(
                enabled = !busy && name.isNotBlank(),
                onClick = { onAdd(name.trim(), role) },
            ) { Text("Add") }
        },
        dismissButton = { TextButton(enabled = !busy, onClick = onDismiss) { Text("Cancel") } },
    )
}

/** One member: who they are and what they may do here. */
@Composable
private fun MemberRow(m: SpaceMemberRow, onEdit: () -> Unit, onRemove: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().padding(vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(
            Modifier.weight(1f).clickable(onClick = onEdit).padding(vertical = 6.dp),
        ) {
            Text(m.displayName, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                if (m.id == null) "named in the space's file; no account yet" else m.role,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Text(m.role, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(end = 4.dp))
        TextButton(onClick = onRemove) { Text("Remove") }
    }
}
