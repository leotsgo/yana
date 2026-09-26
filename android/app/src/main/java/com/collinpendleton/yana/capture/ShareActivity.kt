package com.collinpendleton.yana.capture

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.collinpendleton.yana.MainActivity
import com.collinpendleton.yana.R
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.AppendOutcome
import com.collinpendleton.yana.data.CaptureKit
import com.collinpendleton.yana.data.SearchResult
import com.collinpendleton.yana.data.replica.RecentNoteRow
import com.collinpendleton.yana.ui.screens.NoteScreen
import com.collinpendleton.yana.ui.theme.ThemeMode
import com.collinpendleton.yana.ui.theme.YanaTheme
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * The share target: what another app handed over, one preview, and two
 * ways in — a new note in the inbox, or a block appended to a note
 * picked from the recent list (with search over the whole replica).
 * The activity is light on purpose; the under-three-second budget runs
 * from the share sheet to a frame that can take typing. A new note
 * opens the editor right here, and backing out returns to the app that
 * shared.
 */
class ShareActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        CapturePerf.mark("share-open")
        val title = intent.getStringExtra(Intent.EXTRA_TITLE) ?: intent.getStringExtra(Intent.EXTRA_SUBJECT) ?: ""
        val rawText = intent.getStringExtra(Intent.EXTRA_TEXT) ?: ""
        val bareUrl = Regex("^https?://\\S+$").find(rawText.trim())
        val text = if (bareUrl != null) "" else rawText
        val url = bareUrl?.value ?: ""
        val app = application as YanaApp
        setContent {
            val mode by app.prefs.themeMode.collectAsStateWithLifecycle()
            val dark = when (mode) {
                ThemeMode.System -> isSystemInDarkTheme()
                ThemeMode.Light -> false
                ThemeMode.Dark -> true
            }
            YanaTheme(mode) { SharePage(app, title, text, url) }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun SharePage(app: YanaApp, title: String, text: String, url: String) {
    val context = LocalContext.current
    val session by app.client.session.collectAsStateWithLifecycle()
    val block = remember(title, text, url) { CaptureKit.shareBlock(title, text, url) }
    val hasShare = block.isNotBlank()
    var busy by remember { mutableStateOf(false) }
    var opened by remember { mutableStateOf<Pair<String, String>?>(null) }
    var added by remember { mutableStateOf<String?>(null) }
    var failed by remember { mutableStateOf<String?>(null) }
    var picking by remember { mutableStateOf(false) }

    LaunchedEffect(Unit) { CapturePerf.done("share-open") }

    fun addNewNote() {
        if (busy) return
        busy = true
        CapturePerf.mark("share-edit")
        app.appScope.launch {
            val note = app.capture.newInboxNote(block)
            busy = false
            if (note == null) {
                failed = app.getString(R.string.capture_no_space)
            } else {
                opened = note.id to note.title
            }
        }
    }

    val openedPair = opened
    if (openedPair != null) {
        // The editor, in place: the note is in the replica already, its
        // document ready to type in, and backing out returns to the
        // app that shared.
        NoteScreen(
            repo = app.repo,
            sync = app.syncEngine,
            id = openedPair.first,
            title = openedPair.second,
            startEditing = true,
            perfLabel = "share-edit",
            onBack = { (context as? Activity)?.finish() },
        )
        return
    }

    Scaffold(
        topBar = {
            TopAppBar(title = { Text(stringResource(R.string.share_title)) })
        },
    ) { pad ->
        Column(
            Modifier.fillMaxSize().padding(pad).padding(horizontal = 20.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            if (session == null) {
                Text(stringResource(R.string.share_signed_out), style = MaterialTheme.typography.bodyLarge)
                Button(onClick = {
                    context.startActivity(Intent(context, MainActivity::class.java))
                    (context as? Activity)?.finish()
                }) { Text(stringResource(R.string.share_open_app)) }
                return@Column
            }
            when {
                added != null -> AddedPage(added!!)
                else -> {
                    if (hasShare) {
                        Surface(
                            shape = MaterialTheme.shapes.small,
                            color = MaterialTheme.colorScheme.surfaceContainer,
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            Text(
                                block,
                                style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.onSurface,
                                modifier = Modifier.padding(12.dp),
                            )
                        }
                    } else {
                        Text(
                            stringResource(R.string.share_empty),
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    failed?.let {
                        Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium)
                    }
                    if (picking) {
                        NotePicker(
                            app = app,
                            block = block,
                            busy = busy,
                            onBusy = { busy = it },
                            onAdded = { added = it },
                            onFailed = { failed = it },
                        )
                    } else {
                        Button(onClick = ::addNewNote, enabled = hasShare && !busy) {
                            if (busy) {
                                CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                                Spacer(Modifier.width(10.dp))
                            } else {
                                Icon(Icons.Default.Add, contentDescription = null, modifier = Modifier.size(18.dp))
                                Spacer(Modifier.width(10.dp))
                            }
                            Text(stringResource(R.string.share_new_note))
                        }
                        OutlinedButton(onClick = { picking = true }, enabled = hasShare && !busy) {
                            Icon(Icons.Default.Search, contentDescription = null, modifier = Modifier.size(18.dp))
                            Spacer(Modifier.width(10.dp))
                            Text(stringResource(R.string.share_pick_note))
                        }
                    }
                }
            }
        }
    }
}

/** The block landed; a word and a beat, then back to the app that shared. */
@Composable
private fun AddedPage(path: String) {
    val context = LocalContext.current
    LaunchedEffect(path) {
        delay(900)
        (context as? Activity)?.finish()
    }
    Row(
        Modifier.fillMaxWidth().padding(top = 32.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(Icons.Default.Check, contentDescription = null, tint = MaterialTheme.colorScheme.primary)
        Column {
            Text(stringResource(R.string.share_added), style = MaterialTheme.typography.titleMedium)
            Text(
                path,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** The recent list with search over everything the replica holds. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun NotePicker(
    app: YanaApp,
    block: String,
    busy: Boolean,
    onBusy: (Boolean) -> Unit,
    onAdded: (String) -> Unit,
    onFailed: (String?) -> Unit,
) {
    var query by remember { mutableStateOf("") }
    var recents by remember { mutableStateOf<List<RecentNoteRow>>(emptyList()) }
    var hits by remember { mutableStateOf<List<SearchResult>>(emptyList()) }

    LaunchedEffect(Unit) {
        recents = runCatching { app.capture.recentNotes(30) }.getOrDefault(emptyList())
    }
    LaunchedEffect(query) {
        delay(200)
        hits = if (query.trim().length < 2) {
            emptyList()
        } else {
            runCatching { app.repo.search(query.trim(), null) }.getOrDefault(emptyList())
        }
    }

    Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        OutlinedTextField(
            value = query,
            onValueChange = { query = it },
            placeholder = { Text(stringResource(R.string.share_search)) },
            singleLine = true,
            modifier = Modifier.fillMaxWidth(),
        )
        if (busy) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                Text(stringResource(R.string.share_adding), style = MaterialTheme.typography.bodyMedium)
            }
        }
        LazyColumn(Modifier.weight(1f)) {
            if (hits.isEmpty()) {
                items(recents, key = { "r:" + it.id }) { n ->
                    PickRow(n.title.ifEmpty { n.path }, n.path) { pick(app, n.id, block, onBusy, onAdded, onFailed) }
                }
            } else {
                items(hits, key = { "h:" + it.id }) { h ->
                    PickRow(h.title.ifEmpty { h.path }, h.path) { pick(app, h.id, block, onBusy, onAdded, onFailed) }
                }
            }
        }
    }
}

private fun pick(
    app: YanaApp,
    id: String,
    block: String,
    onBusy: (Boolean) -> Unit,
    onAdded: (String) -> Unit,
    onFailed: (String?) -> Unit,
) {
    onBusy(true)
    app.appScope.launch {
        val outcome = app.capture.appendBlock(id, block)
        val path = app.capture.storePathOf(id) ?: ""
        val local = (outcome as? AppendOutcome.Done)?.local == true
        kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.Main) {
            when (outcome) {
                is AppendOutcome.Done -> {
                    if (local) {
                        Toast.makeText(app, app.getString(R.string.share_added_local), Toast.LENGTH_SHORT).show()
                    }
                    onAdded(path)
                }
                AppendOutcome.NotOnDevice -> onFailed(app.getString(R.string.share_not_on_device))
                AppendOutcome.NotFound -> onFailed(app.getString(R.string.share_not_found))
            }
            onBusy(false)
        }
    }
}

/** One row of the picker: the name it goes by, where it lives. */
@Composable
private fun PickRow(title: String, path: String, onClick: () -> Unit) {
    Column(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(vertical = 10.dp),
    ) {
        Text(title, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text(
            path,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}
