package com.collinpendleton.yana.ui.screens

import android.widget.Toast
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
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
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.collinpendleton.yana.Prefs
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.NewNoteKit
import com.collinpendleton.yana.data.NewNoteKit.baseOf
import com.collinpendleton.yana.data.NewNoteKit.dirOf
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.ParsedPath
import com.collinpendleton.yana.data.PickerNote
import com.collinpendleton.yana.data.fuzzy
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.Loader
import com.collinpendleton.yana.ui.Placeholder
import com.collinpendleton.yana.ui.ShellInsets
import com.collinpendleton.yana.ui.YanaIcons
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** What the picker judges paths against: the spaces, the folders, the notes. */
private data class PickerPool(
    val spaces: List<String>,
    val dirs: List<String>,
    val notes: List<PickerNote>,
    /** The folder the input starts in, with its trailing slash. */
    val seed: String,
)

/** One row of the picker's list. */
private sealed interface PickerRow {
    data class Open(val note: PickerNote) : PickerRow

    data class Near(val note: PickerNote) : PickerRow

    data class Folder(val path: String, val recent: Boolean) : PickerRow

    data class Up(val path: String) : PickerRow
}

/**
 * The new-note picker: the place and the name are chosen before the
 * note is made, the web's newnote.tsx on a phone. The input holds a
 * path — everything up to the last slash is the folder, the rest is
 * the name — and the list under it walks into folders and back out.
 * A name that names a note that is there opens that note instead of
 * making a second; an empty name makes an untitled note in the folder
 * and focuses its title. The folders used lately on this device sit
 * at the top until something is typed.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun NewNoteScreen(
    app: YanaApp,
    repo: NoteRepository,
    prefs: Prefs,
    start: String,
    onBack: () -> Unit,
    onOpen: (id: String, title: String) -> Unit,
    onCreated: (id: String, title: String) -> Unit,
) {
    // The pool is the replica's: airplane mode answers the same as
    // online, the existing-note guard included.
    val vm: Loader<PickerPool> = viewModel(key = "newnote-picker") {
        Loader(fetch = {
            val spaces = repo.spaces().map { it.name }.filter { it.isNotEmpty() }
            val dirs = repo.folderList()
            val notes = repo.allNotes().map { PickerNote(it.id, it.path, it.title) }
            val known: (String) -> Boolean = { p -> p in dirs || p in spaces }
            val last = (listOf(prefs.lastFolder()) + prefs.recentFolders.value + listOf(start))
                .firstOrNull { it.isNotEmpty() && known(it) }
                ?: spaces.firstOrNull().orEmpty()
            PickerPool(spaces, dirs, notes, if (last.isEmpty()) "" else "$last/")
        })
    }
    val state by vm.loaded.collectAsStateWithLifecycle()
    val pool = state.data
    val recents by prefs.recentFolders.collectAsStateWithLifecycle()
    val context = LocalContext.current
    val scope = rememberCoroutineScope()

    // Null until the pool seeds it; from then on the person's own.
    var text by remember { mutableStateOf<String?>(null) }
    var busy by remember { mutableStateOf(false) }
    val focus = remember { FocusRequester() }

    LaunchedEffect(pool) {
        if (pool != null && text == null) {
            text = pool.seed
            runCatching { focus.requestFocus() }
        }
    }

    val typed = text ?: ""
    val space = pool?.seed.orEmpty().substringBefore('/', "")
    val parsed: ParsedPath? = pool?.let { NewNoteKit.parsePath(typed, space, it.spaces, it.dirs) }
    val exists = if (parsed == null || parsed.error != null) {
        null
    } else {
        pool?.let { NewNoteKit.existingNote(it.notes, parsed.dir, parsed.name) }
    }

    val rows: List<PickerRow> = if (pool == null || parsed == null) {
        emptyList()
    } else {
        pickerRows(pool, parsed, exists, recents, typed == pool.seed)
    }

    fun open(note: PickerNote) {
        onOpen(note.id, note.title.ifEmpty { baseOf(note.path) })
    }

    fun create(force: Boolean) {
        val p = parsed ?: return
        val n = p.name
        if (busy) return
        if (!force && exists != null) {
            open(exists)
            return
        }
        if (p.error != null) return
        busy = true
        scope.launch {
            try {
                val name = when {
                    n.isEmpty() -> null
                    force -> NewNoteKit.freeName(pool!!.notes, p.dir, n)
                    else -> NewNoteKit.noteFile(n)
                }
                val note = app.capture.newNoteIn(p.dir, name)
                if (note == null) {
                    Toast.makeText(context, "No space to make the note in.", Toast.LENGTH_SHORT).show()
                } else {
                    onCreated(note.id, note.title)
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                Toast.makeText(context, e.userMessage(), Toast.LENGTH_SHORT).show()
            } finally {
                busy = false
            }
        }
    }

    Scaffold(
        contentWindowInsets = ShellInsets,
        topBar = {
            TopAppBar(
                title = { Text("New note") },
                navigationIcon = {
                    IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "Back") }
                },
                actions = {
                    if (exists != null || parsed?.error == null) {
                        TextButton(enabled = !busy, onClick = { create(false) }) {
                            Text(if (exists != null) "Open" else "Create")
                        }
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(containerColor = MaterialTheme.colorScheme.background),
            )
        },
    ) { pad ->
        if (pool == null) {
            Placeholder(loading = state.loading, error = state.error, empty = null, onRetry = { vm.reload() }, modifier = Modifier.padding(pad))
            return@Scaffold
        }
        Column(Modifier.padding(pad).fillMaxSize()) {
            OutlinedTextField(
                value = typed,
                onValueChange = { text = it },
                placeholder = { Text("space/folder/name") },
                singleLine = true,
                enabled = !busy,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Go),
                keyboardActions = KeyboardActions(onGo = { create(false) }),
                modifier = Modifier.fillMaxWidth().padding(horizontal = 20.dp).focusRequester(focus),
            )
            Row(
                Modifier.fillMaxWidth().padding(start = 20.dp, end = 12.dp, top = 2.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f)) {
                    val what = when {
                        parsed!!.error != null -> parsed.error
                        exists != null -> "Opens ${exists.path}"
                        parsed.name.isNotEmpty() -> "Creates ${parsed.dir}/${NewNoteKit.noteFile(parsed.name)}"
                        else -> "Creates an untitled note in ${parsed.dir}/"
                    }
                    Text(
                        what,
                        style = MaterialTheme.typography.labelSmall,
                        color = if (parsed.error != null) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                    val missing = parsed.missing
                    if (missing.isNotEmpty() && parsed.error == null) {
                        Text(
                            missing.joinToString("  ") { "${baseOf(it)}/ new folder" },
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
                if (exists != null) {
                    TextButton(enabled = !busy, onClick = { create(true) }) { Text("Create anyway") }
                }
            }
            LazyColumn(Modifier.fillMaxSize()) {
                items(rows, key = { rowKey(it) }) { row ->
                    when (row) {
                        is PickerRow.Open -> NoteRow("Open ${row.note.title.ifEmpty { baseOf(row.note.path) }}", row.note.path) { open(row.note) }
                        is PickerRow.Near -> NoteRow(row.note.title.ifEmpty { baseOf(row.note.path) }, row.note.path) { open(row.note) }
                        is PickerRow.Up -> {
                            val label = if (row.path.isEmpty()) "Up to the spaces" else "Up to ${baseOf(row.path)}/"
                            FolderRow(label, row.path, up = true) { text = if (row.path.isEmpty()) "" else "${row.path}/" }
                        }
                        is PickerRow.Folder ->
                            FolderRow(baseOf(row.path) + "/", row.path, recent = row.recent) { text = "${row.path}/" }
                    }
                }
            }
        }
    }
}

/** The rows under what is typed: the note that is there, the ones near
 * it, the way up, and the folders — the recents first while nothing
 * is typed. */
private fun pickerRows(
    pool: PickerPool,
    parsed: ParsedPath,
    exists: PickerNote?,
    recents: List<String>,
    untouched: Boolean,
): List<PickerRow> {
    val out = ArrayList<PickerRow>()
    if (exists != null) out.add(PickerRow.Open(exists))
    if (parsed.name.isNotEmpty() && parsed.error == null) {
        val near = ArrayList<Pair<PickerNote, Double>>()
        for (n in pool.notes) {
            if (n === exists || dirOf(n.path) != parsed.dir) continue
            val m = fuzzy(parsed.name, baseOf(n.path))
            if (m != null) near.add(n to m.score)
        }
        near.sortByDescending { it.second }
        for ((n, _) in near.take(if (exists != null) 4 else 3)) out.add(PickerRow.Near(n))
    }
    if (parsed.browse != null) {
        out.add(PickerRow.Up(dirOf(parsed.browse)))
    }
    val listed = HashSet<String>()
    val known = pool.dirs.toSet()
    val recentKnown = recents.filter { it in known }
    if (untouched) {
        for (p in recentKnown) out.add(PickerRow.Folder(p, recent = true))
    }
    if (parsed.browse != null) {
        val kids = NewNoteKit.children(parsed.browse, pool.spaces, pool.dirs)
        val picked = if (parsed.name.isEmpty()) {
            kids
        } else {
            kids.mapNotNull { k -> fuzzy(parsed.name, baseOf(k))?.let { k to it.score } }
                .sortedByDescending { it.second }
                .map { it.first }
        }
        for (k in picked.take(60)) {
            listed.add(k)
            out.add(PickerRow.Folder(k, recent = false))
        }
    }
    if (parsed.name.isNotEmpty()) {
        val elsewhere = { paths: List<String> ->
            paths.filter { it.isNotEmpty() && it != parsed.browse && it !in listed }
                .mapNotNull { p -> fuzzy(parsed.name, p)?.let { p to it.score } }
                .sortedByDescending { it.second }
                .map { it.first }
        }
        for (p in elsewhere(recentKnown).take(4)) {
            listed.add(p)
            out.add(PickerRow.Folder(p, recent = true))
        }
        for (p in elsewhere(pool.dirs).take(12)) out.add(PickerRow.Folder(p, recent = false))
    }
    return out
}

private fun rowKey(row: PickerRow): String = when (row) {
    is PickerRow.Open -> "open:" + row.note.id
    is PickerRow.Near -> "near:" + row.note.id
    is PickerRow.Up -> "up:" + row.path
    is PickerRow.Folder -> (if (row.recent) "recent:" else "folder:") + row.path
}

/** A folder row: the icon, the name with its slash, and the way in. */
@Composable
private fun FolderRow(label: String, path: String, up: Boolean = false, recent: Boolean = false, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(start = 20.dp, end = 16.dp, top = 11.dp, bottom = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            if (up) Icons.AutoMirrored.Filled.ArrowBack else YanaIcons.Folder,
            contentDescription = null,
            tint = if (recent || up) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.size(18.dp),
        )
        Spacer(Modifier.width(12.dp))
        Text(
            label,
            style = MaterialTheme.typography.bodyLarge,
            fontWeight = if (up || recent) FontWeight.Medium else FontWeight.Normal,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (path.isNotEmpty()) {
            Icon(
                Icons.AutoMirrored.Filled.KeyboardArrowRight,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/** A note row: the title and the path it lives at. */
@Composable
private fun NoteRow(label: String, detail: String, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(start = 20.dp, end = 16.dp, top = 11.dp, bottom = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(YanaIcons.FileText, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.size(18.dp))
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(label, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                detail,
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}
