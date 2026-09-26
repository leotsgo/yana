package com.collinpendleton.yana.ui.screens

import android.widget.Toast
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
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
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.collinpendleton.yana.BuildConfig
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.GuideRequest
import com.collinpendleton.yana.data.TreeNode
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.Wordmark
import com.collinpendleton.yana.ui.openUrl
import com.collinpendleton.yana.ui.theme.ThemeMode
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/**
 * Settings the phone's way: the sections a thumb reaches (account,
 * sharing, people, appearance, data, help) and rows that open the
 * signed-in server on the web for the work that wants a keyboard
 * (agents, backups, site export, conventions).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(
    app: YanaApp,
    onBack: () -> Unit,
    onAccount: () -> Unit = {},
    onSpacesSettings: () -> Unit = {},
    onPeople: () -> Unit = {},
    onData: () -> Unit = {},
    onOpenNote: (id: String, title: String) -> Unit = { _, _ -> },
) {
    val client = app.client
    val session by client.session.collectAsStateWithLifecycle()
    val mode by app.prefs.themeMode.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }
    var helpBusy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()

    /** Opens the web app at one of its settings sections. */
    fun openWeb(section: String) {
        val server = session?.server?.trimEnd('/') ?: return
        openUrl(context, "$server/settings/$section")
    }

    /** Opens the Start here note, making it when the server does not have it. */
    fun openStartHere() {
        if (helpBusy) return
        helpBusy = true
        scope.launch {
            try {
                val spaces = app.repo.spaces()
                val trees = spaces.associate { it.name to app.repo.tree(it.name) }
                val found = findStartHere(trees.values)
                if (found?.id != null) {
                    onOpenNote(found.id, found.title?.ifEmpty { null } ?: "Start here")
                } else {
                    val space = spaces.firstOrNull { it.name.isNotEmpty() }?.name ?: ""
                    val guide = client.api().guide(GuideRequest(space))
                    if (guide.id != null) {
                        onOpenNote(guide.id, "Start here")
                    } else {
                        toast("The Start here note was just written; it appears after the server's next scan.")
                    }
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                toast(e.userMessage())
            } finally {
                helpBusy = false
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("Settings") },
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
                Section("Account")
                SettingsRow(
                    title = "Account",
                    blurb = "Your password, and every device signed in.",
                    onClick = onAccount,
                )

                HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                Section("Sharing")
                SettingsRow(
                    title = "Spaces and sharing",
                    blurb = "Who can see and edit each space.",
                    onClick = onSpacesSettings,
                )
                if (session?.isOwner == true) {
                    SettingsRow(
                        title = "People",
                        blurb = "The accounts on this server.",
                        onClick = onPeople,
                    )
                }

                HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                Section("Appearance")
                SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                    val modes = ThemeMode.entries
                    modes.forEachIndexed { i, m ->
                        SegmentedButton(
                            selected = mode == m,
                            onClick = { app.prefs.setThemeMode(m) },
                            shape = SegmentedButtonDefaults.itemShape(i, modes.size),
                        ) { Text(m.name) }
                    }
                }

                HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                Section("Capture")
                CaptureSettings(app)

                HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                Section("Data")
                SettingsRow(
                    title = "Data",
                    blurb = "Deleted notes, conflicts, and a space as a zip.",
                    onClick = onData,
                )

                HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                Section("Help")
                SettingsRow(
                    title = "Start here",
                    blurb = "The guide note: how links, pictures, tasks and tags work.",
                    onClick = { openStartHere() },
                )

                HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                Section("On the web")
                if (session?.isOwner == true) {
                    SettingsRow(
                        title = "Agents",
                        blurb = "Keys for tools that read and write notes over MCP.",
                        onClick = { openWeb("agents") },
                    )
                    SettingsRow(
                        title = "Backups",
                        blurb = "The history pushed to other repositories, and restore from one.",
                        onClick = { openWeb("data") },
                    )
                }
                SettingsRow(
                    title = "Site export",
                    blurb = "A space as a site that stands on its own, from the web's data page.",
                    onClick = { openWeb("data") },
                )
                SettingsRow(
                    title = "Space conventions",
                    blurb = "CONVENTIONS.md, the brief a space gives its agents — the web's agents page.",
                    onClick = { openWeb("agents") },
                )

                HorizontalDivider(Modifier.padding(vertical = 8.dp), color = MaterialTheme.colorScheme.outlineVariant)
                Section("About")
                About()
            }
        }
    }
}

/** The Start here note across the loaded trees — the same find the web's help makes. */
fun findStartHere(trees: Iterable<List<TreeNode>>): TreeNode? {
    fun walk(nodes: List<TreeNode>): TreeNode? {
        for (n in nodes) {
            if (!n.isDir && n.name == "Start here.md") return n
            walk(n.children)?.let { return it }
        }
        return null
    }
    return trees.asSequence().mapNotNull { walk(it) }.firstOrNull()
}

/**
 * Where fast capture lands: the space Today, Capture, and the inbox
 * work in, and the inbox folder under each space. Kept on this device,
 * the way the web keeps its defaults in the browser.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun CaptureSettings(app: YanaApp) {
    var spaces by remember { mutableStateOf<List<String>>(emptyList()) }
    var daily by remember { mutableStateOf("") }
    var folders by remember { mutableStateOf<Map<String, String>>(emptyMap()) }

    LaunchedEffect(Unit) {
        spaces = runCatching { app.repo.spaces().map { it.name }.filter { it.isNotEmpty() } }.getOrDefault(emptyList())
        daily = app.prefs.dailySpace()
        folders = spaces.associateWith { app.prefs.inboxFolder(it) }
    }

    Text(
        "Today, Capture, and the quick tiles work in one space; a new note from them lands in the space's inbox folder.",
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    Text("Capture space", style = MaterialTheme.typography.labelLarge)
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        FilterChip(
            selected = daily.isEmpty(),
            onClick = {
                daily = ""
                app.prefs.setDailySpace("")
            },
            label = { Text("First space") },
        )
        for (s in spaces) {
            FilterChip(
                selected = daily == s,
                onClick = {
                    daily = s
                    app.prefs.setDailySpace(s)
                },
                label = { Text(s) },
            )
        }
    }
    Text("Inbox folder", style = MaterialTheme.typography.labelLarge)
    for (s in spaces) {
        OutlinedTextField(
            value = folders[s] ?: "",
            onValueChange = { v ->
                folders = folders + (s to v)
                app.prefs.setInboxFolder(s, v)
            },
            label = { Text(s) },
            singleLine = true,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun About() {
    Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
        Row(verticalAlignment = Alignment.Top) {
            Wordmark(size = 30.sp)
            Text("1", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.primary)
        }
        Text(
            "Plain files on your server, edited from any device. Version ${BuildConfig.VERSION_NAME}.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        HorizontalDivider(Modifier.padding(top = 16.dp).widthIn(max = 48.dp), color = MaterialTheme.colorScheme.outline)
        Text(
            "1. Yet Another Notes App.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}
