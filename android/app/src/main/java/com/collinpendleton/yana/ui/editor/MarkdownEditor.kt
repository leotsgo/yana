package com.collinpendleton.yana.ui.editor

import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isShiftPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.text.TextMeasurer
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.drawText
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.FileProvider
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.collinpendleton.yana.data.AssetNames
import com.collinpendleton.yana.data.ImageAssets
import com.collinpendleton.yana.data.ImageSource
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.assetBaseOf
import com.collinpendleton.yana.data.rt.PeerCursor
import com.collinpendleton.yana.data.rt.SyncEngine
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.YanaIcons
import java.io.File
import java.io.IOException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import retrofit2.HttpException

/**
 * The markdown editor: a plain text field bound to the note's document
 * through a diff — each change the field reports becomes one
 * replacement on the document, and each change the document reports
 * (someone else typing, an undo, a redo) comes back with the cursor
 * mapped through it rather than reset. Other people's selections and
 * carets draw over the text in their colors, and the local cursor
 * broadcasts on a 50ms throttle. The field grows to its text and the
 * container scrolls, which keeps the overlay aligned with the layout.
 */
@Composable
fun MarkdownEditor(
    sync: SyncEngine,
    handle: SyncEngine.NoteHandle,
    noteId: String,
    modifier: Modifier = Modifier,
    /** The note's tree path; the image action uploads beside it. Null hides the action. */
    notePath: String? = null,
    repo: NoteRepository? = null,
    images: ImageAssets? = null,
    /** A scope that outlives this screen, so an upload in flight survives navigation. */
    workScope: CoroutineScope? = null,
) {
    var field by remember(noteId) { mutableStateOf<TextFieldValue?>(null) }
    var layout by remember(noteId) { mutableStateOf<TextLayoutResult?>(null) }
    val bursts = remember(noteId) { UndoBursts() }
    val throttle = remember(noteId) { CursorThrottle(50) }
    val measurer = rememberTextMeasurer()
    val context = LocalContext.current
    val toast: (String) -> Unit = { msg -> Toast.makeText(context, msg, Toast.LENGTH_SHORT).show() }

    val presence by handle.presence.collectAsStateWithLifecycle()
    val undoDepth by handle.undoDepth.collectAsStateWithLifecycle()
    val redoDepth by handle.redoDepth.collectAsStateWithLifecycle()
    val liveText by handle.text.collectAsStateWithLifecycle()
    val ready by handle.ready.collectAsStateWithLifecycle()

    // Seed the field once the text is this device's document — an
    // empty document included, which is what a note composed offline
    // opens as.
    LaunchedEffect(ready, liveText) {
        if (field == null && ready) {
            field = TextFieldValue(liveText)
        }
    }

    // Document changes: apply the text, map the selection through the
    // change's hunks. Our own typing arrives as an echo and is skipped.
    LaunchedEffect(noteId) {
        var lastSeq = -1L
        handle.edits.collect { change ->
            val (seq, edit) = change ?: return@collect
            if (seq <= lastSeq) return@collect
            lastSeq = seq
            val current = field ?: return@collect
            if (edit.local && edit.text == current.text) return@collect
            val hunks = SelectionMapper.parse(edit.delta)
            val sel = current.selection
            val (start, end) = SelectionMapper.mapSelection(hunks, sel.min, sel.max, edit.text.length)
            field = TextFieldValue(edit.text, TextRange(start, end))
        }
    }

    // The trailing edge of the cursor throttle.
    LaunchedEffect(noteId) {
        while (true) {
            delay(10)
            throttle.due()?.let { sync.sendCursor(noteId, it[0], it[1]) }
        }
    }

    fun change(next: TextFieldValue) {
        val current = field
        field = next
        if (current != null) {
            val op = TextFieldDiff.diff(current.text, next.text)
            if (op != null) {
                sync.editOp(noteId, op.pos, op.del, op.insert)
                bursts.onEdit()
            }
        }
        throttle.offer(next.selection.min, next.selection.max)?.let { sync.sendCursor(noteId, it[0], it[1]) }
    }

    fun undo() {
        val steps = bursts.nextUndoSteps()
        if (steps <= 0) {
            sync.undo(noteId)
            return
        }
        repeat(steps) { sync.undo(noteId) }
        bursts.onUndone()
    }

    fun redo() {
        val steps = bursts.nextRedoSteps()
        if (steps <= 0) {
            sync.redo(noteId)
            return
        }
        repeat(steps) { sync.redo(noteId) }
        bursts.onRedone()
    }

    // --- images ---------------------------------------------------------------

    /** Applies one marker→replacement swap through the diff, wherever the marker sits now. */
    fun applyUploadResult(marker: String, replacement: String) {
        val current = field ?: return
        val (text, cursor) = AssetNames.replaceMarker(current.text, marker, replacement, current.selection.min)
        if (text == current.text) return
        change(TextFieldValue(text, TextRange(cursor)))
    }

    /**
     * One photo into the note at the cursor: the marker goes in now as
     * one op, the upload runs behind it, and the marker becomes the
     * link — or leaves, with the reason — when the server answers.
     * Offline, the bytes queue and the link is written for the name
     * asked for.
     */
    fun insertImage(source: ImageSource) {
        val path = notePath
        val base = if (path != null) assetBaseOf(path) else ""
        val repository = repo
        val assets = images
        val scope = workScope
        if (path == null || base.isEmpty() || repository == null || assets == null || scope == null) return
        scope.launch {
            val prepared = runCatching { assets.prepare(source) }.getOrNull()
            if (prepared == null) {
                withContext(Dispatchers.Main) { toast("Could not read that photo.") }
                return@launch
            }
            val marker = AssetNames.marker(prepared.name)
            withContext(Dispatchers.Main) {
                val current = field ?: return@withContext
                val at = current.selection.min
                val text = current.text.substring(0, at) + marker + current.text.substring(at)
                change(TextFieldValue(text, TextRange(at + marker.length)))
            }
            try {
                val res = repository.uploadAsset("$base/_assets/${prepared.name}", prepared.bytes, prepared.mime)
                val name = res.name.ifEmpty { prepared.name }
                withContext(Dispatchers.Main) { applyUploadResult(marker, AssetNames.imageLink(name)) }
            } catch (e: CancellationException) {
                throw e
            } catch (e: IOException) {
                assets.queueOffline(prepared, base, noteId, repository)
                withContext(Dispatchers.Main) {
                    applyUploadResult(marker, AssetNames.imageLink(prepared.name))
                    toast("Offline. ${prepared.name} uploads when the connection returns.")
                }
            } catch (e: HttpException) {
                withContext(Dispatchers.Main) {
                    applyUploadResult(marker, "")
                    toast(e.userMessage())
                }
            }
        }
    }

    var imageMenu by remember(noteId) { mutableStateOf(false) }
    var cameraTarget by remember(noteId) { mutableStateOf<File?>(null) }

    val picker = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        if (uri != null) insertImage(ImageSource.Picked(uri))
    }
    val camera = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { ok ->
        val f = cameraTarget
        cameraTarget = null
        if (ok && f != null && f.exists()) insertImage(ImageSource.Capture(f))
    }

    fun takePhoto() {
        val dir = File(context.cacheDir, "camera").apply { mkdirs() }
        val f = File(dir, "photo-${AssetNames.stamp()}.jpg")
        cameraTarget = f
        val uri = FileProvider.getUriForFile(context, context.packageName + ".files", f)
        runCatching { camera.launch(uri) }.onFailure {
            cameraTarget = null
            toast("No camera app took the request.")
        }
    }

    // Leaving the editor withdraws the cursor from the room.
    DisposableEffect(noteId) {
        onDispose { sync.sendCursorRemoval(noteId) }
    }

    Column(modifier.fillMaxSize()) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 20.dp),
            horizontalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            for (peer in presence.values.sortedBy { it.name ?: "" }) {
                PeerChip(peer)
            }
        }
        Box(
            Modifier
                .weight(1f)
                .fillMaxWidth()
                .verticalScroll(rememberScrollState()),
        ) {
            val value = field
            if (value != null) {
                BasicTextField(
                    value = value,
                    onValueChange = ::change,
                    textStyle = MaterialTheme.typography.bodyLarge.copy(color = MaterialTheme.colorScheme.onSurface),
                    cursorBrush = SolidColor(MaterialTheme.colorScheme.primary),
                    onTextLayout = { layout = it },
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 20.dp, vertical = 8.dp)
                        .onPreviewKeyEvent { e ->
                            if (e.type != KeyEventType.KeyDown) return@onPreviewKeyEvent false
                            val z = e.key == Key.Z
                            val y = e.key == Key.Y
                            if (!e.isCtrlPressed || !(z || y)) return@onPreviewKeyEvent false
                            if (z && e.isShiftPressed || y) redo() else undo()
                            true
                        },
                )
                PresenceOverlay(
                    peers = presence.values.toList(),
                    layout = layout,
                    measurer = measurer,
                    modifier = Modifier.matchParentSize(),
                )
            } else {
                Text(
                    "Opening the document…",
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 20.dp, vertical = 8.dp),
                )
            }
        }
        Surface(color = MaterialTheme.colorScheme.surfaceContainer) {
            Row(
                Modifier.fillMaxWidth().heightIn(min = 56.dp).padding(horizontal = 8.dp),
                horizontalArrangement = Arrangement.spacedBy(4.dp),
            ) {
                if (notePath != null && repo != null && images != null && workScope != null) {
                    Box {
                        IconButton(onClick = { imageMenu = true }) {
                            Icon(
                                YanaIcons.Image,
                                contentDescription = "Insert image",
                                tint = MaterialTheme.colorScheme.onSurface,
                            )
                        }
                        DropdownMenu(expanded = imageMenu, onDismissRequest = { imageMenu = false }) {
                            DropdownMenuItem(
                                text = { Text("Choose photo") },
                                onClick = {
                                    imageMenu = false
                                    picker.launch(
                                        PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly),
                                    )
                                },
                            )
                            DropdownMenuItem(
                                text = { Text("Take photo") },
                                onClick = {
                                    imageMenu = false
                                    takePhoto()
                                },
                            )
                        }
                    }
                }
                Spacer(Modifier.weight(1f))
                TextButton(onClick = ::undo, enabled = undoDepth > 0) {
                    Text("Undo", style = MaterialTheme.typography.labelLarge)
                }
                TextButton(onClick = ::redo, enabled = redoDepth > 0) {
                    Text("Redo", style = MaterialTheme.typography.labelLarge)
                }
            }
        }
    }
}

/** A name in its color: the identity half of presence. */
@Composable
private fun PeerChip(peer: PeerCursor, modifier: Modifier = Modifier) {
    val color = peerColor(peer.color)
    Surface(
        shape = MaterialTheme.shapes.small,
        color = color.copy(alpha = 0.14f),
        modifier = modifier.padding(top = 6.dp),
    ) {
        Text(
            peer.name ?: "someone",
            style = MaterialTheme.typography.labelMedium,
            color = color,
            modifier = Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
        )
    }
}

/** Other people's selections and carets, drawn over the text layout. */
@Composable
private fun PresenceOverlay(
    peers: List<PeerCursor>,
    layout: TextLayoutResult?,
    measurer: TextMeasurer,
    modifier: Modifier = Modifier,
) {
    Canvas(modifier.clipToBounds()) {
        val l = layout ?: return@Canvas
        val caretWidth = 2.dp.toPx()
        for (peer in peers) {
            val head = peer.head ?: continue
            val color = peerColor(peer.color)
            if (peer.selection && peer.anchor != null) {
                val start = minOf(peer.anchor, head)
                val end = maxOf(peer.anchor, head)
                val firstLine = l.getLineForOffset(start)
                val lastLine = l.getLineForOffset(end)
                for (line in firstLine..lastLine) {
                    val left = if (line == firstLine) l.getHorizontalPosition(start, true) else l.getLineLeft(line)
                    val right = if (line == lastLine) l.getHorizontalPosition(end, true) else l.getLineRight(line)
                    val top = l.getLineTop(line)
                    drawRect(
                        color = peerColor(peer.colorLight),
                        topLeft = Offset(left, top),
                        size = Size(maxOf(right - left, 0f), l.getLineBottom(line) - top),
                    )
                }
            }
            val caret = l.getCursorRect(head)
            drawRect(
                color = color,
                topLeft = Offset(caret.left - caretWidth / 2, caret.top),
                size = Size(caretWidth, caret.height),
            )
            val name = peer.name ?: continue
            val label = measurer.measure(
                AnnotatedString(name, SpanStyle(background = color, color = Color.White, fontSize = 10.sp)),
            )
            val chipTop = (caret.top - label.size.height - 2.dp.toPx()).coerceAtLeast(0f)
            drawText(label, topLeft = Offset(caret.left, chipTop))
        }
    }
}

/** Parses the palette's hex; a neutral gray when a peer sends something else. */
private fun peerColor(hex: String): Color =
    runCatching { Color(android.graphics.Color.parseColor(hex)) }.getOrDefault(Color(0xFF666666))
