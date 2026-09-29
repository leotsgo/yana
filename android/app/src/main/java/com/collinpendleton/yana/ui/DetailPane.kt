package com.collinpendleton.yana.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.detectHorizontalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.dp
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.ui.screens.NoteScreen

/**
 * What the detail side of a large screen holds: the note a list picked,
 * with where a tasks row opened it and whether it asked for the editor.
 * The web's tab record, cut down to what a phone-shaped stack needs.
 */
data class NoteSpec(val id: String, val title: String, val line: Int = -1, val edit: Boolean = false)

/** A [NoteSpec] as saveable strings: id, title, line, edit. */
fun encodeNoteSpec(spec: NoteSpec): List<String> =
    listOf(spec.id, spec.title, spec.line.toString(), spec.edit.toString())

/** Reads back what [encodeNoteSpec] wrote; null when it is not one. */
fun decodeNoteSpec(parts: List<String>): NoteSpec? {
    if (parts.size != 4) return null
    val line = parts[2].toIntOrNull() ?: return null
    val edit = parts[3].toBooleanStrictOrNull() ?: return null
    return NoteSpec(parts[0], parts[1], line, edit)
}

/** The beside pane's share of the detail area, kept where the web keeps its divider. */
fun clampDivider(f: Float): Float = f.coerceIn(0.3f, 0.7f)

/**
 * The note side of a large screen: the note a list picked on the left,
 * and — at expanded width — a second note beside it over a draggable
 * divider, one reading and one editing allowed. A wikilink opens in
 * the pane it was tapped from, the web's focused pane; the note's menu
 * carries Open beside, which sets this note beside itself in the other
 * mode (read beside edit) and closes it when it is already there.
 */
@Composable
fun DetailPane(
    app: YanaApp,
    main: NoteSpec,
    onMain: (NoteSpec) -> Unit,
    beside: NoteSpec?,
    onBeside: (NoteSpec?) -> Unit,
    fraction: Float,
    onFraction: (Float) -> Unit,
    splitAllowed: Boolean,
    onTag: (String) -> Unit,
    onHistory: (id: String, title: String) -> Unit,
    onConflicts: (id: String, title: String) -> Unit,
    onClose: () -> Unit,
    modifier: Modifier = Modifier,
) {
    BoxWithConstraints(modifier) {
        val maxPx = with(LocalDensity.current) { maxWidth.toPx() }
        // Open beside toggles, the web's split key: the note goes
        // beside itself in the other mode, and the copy that is there
        // closes.
        val showingBeside = beside != null && splitAllowed
        fun toggleBeside(editing: Boolean) {
            onBeside(if (showingBeside) null else NoteSpec(main.id, main.title, -1, !editing))
        }
        if (showingBeside && beside != null) {
            Row(Modifier.fillMaxSize()) {
                PaneNote(
                    app, main, "detail", Modifier.weight(1f - clampDivider(fraction)).fillMaxHeight(),
                    onOpen = onMain, onTag = onTag, onHistory = onHistory, onConflicts = onConflicts,
                    onBack = onClose,
                    onOpenBeside = ::toggleBeside,
                    besideOpen = true,
                )
                DividerHandle(fraction, maxPx, onFraction)
                PaneNote(
                    app, beside, "beside", Modifier.weight(clampDivider(fraction)).fillMaxHeight(),
                    onOpen = onBeside, onTag = onTag, onHistory = onHistory, onConflicts = onConflicts,
                    onBack = { onBeside(null) },
                    onOpenBeside = { _ -> onBeside(null) },
                    besideOpen = true,
                )
            }
        } else {
            PaneNote(
                app, main, "detail", Modifier.fillMaxSize(),
                onOpen = onMain, onTag = onTag, onHistory = onHistory, onConflicts = onConflicts,
                onBack = onClose,
                onOpenBeside = if (splitAllowed) ::toggleBeside else null,
                besideOpen = false,
            )
        }
    }
}

/** One note pane: a full [NoteScreen] bound to the pane's slot. */
@Composable
private fun PaneNote(
    app: YanaApp,
    spec: NoteSpec,
    pane: String,
    modifier: Modifier,
    onOpen: (NoteSpec) -> Unit,
    onTag: (String) -> Unit,
    onHistory: (id: String, title: String) -> Unit,
    onConflicts: (id: String, title: String) -> Unit,
    onBack: () -> Unit,
    onOpenBeside: ((editing: Boolean) -> Unit)?,
    besideOpen: Boolean,
) {
    androidx.compose.runtime.key(spec.id) {
        NoteScreen(
            repo = app.repo,
            sync = app.syncEngine,
            id = spec.id,
            title = spec.title,
            atLine = spec.line,
            startEditing = spec.edit,
            pane = pane,
            onBack = onBack,
            onOpenNote = { id -> onOpen(NoteSpec(id, "", -1, false)) },
            onTag = onTag,
            onHistory = onHistory,
            onConflicts = onConflicts,
            onOpenBeside = onOpenBeside,
            besideOpen = besideOpen,
            modifier = modifier,
        )
    }
}

/**
 * The divider between the two notes: a narrow strip that follows the
 * finger horizontally and resets to the middle on a double tap, the
 * web's drag handle.
 */
@Composable
private fun DividerHandle(fraction: Float, maxPx: Float, onFraction: (Float) -> Unit) {
    val outline = MaterialTheme.colorScheme.outlineVariant
    val grip = MaterialTheme.colorScheme.onSurfaceVariant
    val current = rememberUpdatedState(fraction)
    Box(
        Modifier
            .width(12.dp)
            .fillMaxHeight()
            .background(outline)
            .pointerInput(maxPx) {
                detectHorizontalDragGestures { change, drag ->
                    change.consume()
                    val besidePx = current.value * maxPx - drag
                    onFraction(clampDivider(besidePx / maxPx))
                }
            }
            .pointerInput(Unit) {
                detectTapGestures(onDoubleTap = { onFraction(0.5f) })
            },
        contentAlignment = Alignment.Center,
    ) {
        androidx.compose.foundation.layout.Spacer(
            Modifier
                .size(width = 3.dp, height = 36.dp)
                .background(grip, MaterialTheme.shapes.extraSmall),
        )
    }
}

/** What the note side says before anything is open. */
@Composable
fun DetailPlaceholder(modifier: Modifier = Modifier) {
    Column(
        modifier.padding(horizontal = 32.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Text(
            "Nothing open",
            style = MaterialTheme.typography.titleMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            "Pick a note from the list beside it.",
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}
