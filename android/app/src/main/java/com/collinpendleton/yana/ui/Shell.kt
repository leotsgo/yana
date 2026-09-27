package com.collinpendleton.yana.ui

import android.widget.Toast
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.collinpendleton.yana.R
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.AppendOutcome
import kotlinx.coroutines.launch

/**
 * The bottom bar's window on the editor: NoteScreen raises it while it
 * holds the editor, and the bar steps out of the way, the way the web's
 * phone bar does while editing.
 */
object ShellState {
    val editing = kotlinx.coroutines.flow.MutableStateFlow(false)
}

/**
 * One tab of the bottom bar, the web's phone set: Notes (the tree),
 * Search, Capture, Today, Tasks with the open count, and New. Home is
 * not among them — the wordmark and the back gesture are home, exactly
 * the web's arrangement, where the wordmark is home and the bar is not.
 */
enum class BottomTab(val label: String) {
    Notes("Notes"),
    Search("Search"),
    Capture("Capture"),
    Today("Today"),
    Tasks("Tasks"),
    New("New"),
}

/**
 * The bar itself: a flat row of icon-and-label buttons, Capture in the
 * accent color, Tasks carrying the open count. [selected] marks the
 * tab the screen under the bar belongs to, when it names one.
 */
@Composable
fun YanaBottomBar(
    selected: BottomTab?,
    taskCount: Int?,
    onTab: (BottomTab) -> Unit,
) {
    Surface(color = MaterialTheme.colorScheme.surfaceContainer, tonalElevation = 3.dp) {
        Row(
            Modifier.fillMaxWidth().navigationBarsPadding().height(64.dp),
            horizontalArrangement = Arrangement.SpaceEvenly,
        ) {
            tabButton(BottomTab.Notes, YanaIcons.Folder, selected == BottomTab.Notes, null, false, onTab)
            tabButton(BottomTab.Search, null, selected == BottomTab.Search, null, false, onTab)
            tabButton(BottomTab.Capture, YanaIcons.Zap, false, null, true, onTab)
            tabButton(BottomTab.Today, YanaIcons.Calendar, false, null, false, onTab)
            tabButton(
                BottomTab.Tasks,
                YanaIcons.CheckSquare,
                selected == BottomTab.Tasks,
                taskCount?.takeIf { it > 0 },
                false,
                onTab,
            )
            tabButton(BottomTab.New, YanaIcons.Plus, false, null, false, onTab)
        }
    }
}

@Composable
private fun androidx.compose.foundation.layout.RowScope.tabButton(
    tab: BottomTab,
    icon: ImageVector?,
    on: Boolean,
    badge: Int?,
    accent: Boolean,
    onTab: (BottomTab) -> Unit,
) {
    val tint = if (accent) MaterialTheme.colorScheme.primary
    else if (on) MaterialTheme.colorScheme.primary
    else MaterialTheme.colorScheme.onSurfaceVariant
    Column(
        Modifier
            .weight(1f)
            .fillMaxWidth()
            .clickable { onTab(tab) }
            .padding(top = 8.dp, bottom = 6.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Box {
            val glyph = icon ?: Icons.Default.Search
            androidx.compose.material3.Icon(glyph, contentDescription = tab.label, tint = tint, modifier = Modifier.size(22.dp))
            if (badge != null) {
                Surface(
                    shape = MaterialTheme.shapes.extraSmall,
                    color = MaterialTheme.colorScheme.primary,
                    modifier = Modifier.align(Alignment.TopEnd).offset(x = 10.dp, y = (-4).dp),
                ) {
                    Text(
                        if (badge > 99) "99+" else badge.toString(),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onPrimary,
                        modifier = Modifier.padding(horizontal = 3.dp, vertical = 1.dp),
                    )
                }
            }
        }
        Spacer(Modifier.height(2.dp))
        Text(
            tab.label,
            style = MaterialTheme.typography.labelSmall,
            color = tint,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/**
 * The capture prompt, shared by home's Capture row and the bar's
 * Capture tab: one field, Add or Enter, the outcome as a toast, the
 * web's palette prompt.
 */
@Composable
fun CaptureLineDialog(app: YanaApp, onDismiss: () -> Unit) {
    var line by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val context = LocalContext.current

    fun add() {
        val text = line.trim()
        if (text.isEmpty() || busy) return
        busy = true
        scope.launch {
            when (val outcome = app.capture.captureLine(text)) {
                is AppendOutcome.Done -> {
                    onDismiss()
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
            busy = false
        }
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.capture_title)) },
        text = {
            OutlinedTextField(
                value = line,
                onValueChange = { line = it },
                placeholder = { Text(stringResource(R.string.capture_hint)) },
                supportingText = { Text(stringResource(R.string.capture_support)) },
                singleLine = true,
                keyboardOptions = androidx.compose.foundation.text.KeyboardOptions(imeAction = androidx.compose.ui.text.input.ImeAction.Done),
                keyboardActions = androidx.compose.foundation.text.KeyboardActions(onDone = { add() }),
                enabled = !busy,
                modifier = Modifier.fillMaxWidth(),
            )
        },
        confirmButton = {
            TextButton(onClick = { add() }, enabled = line.isNotBlank() && !busy) {
                Text(stringResource(R.string.capture_add))
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(android.R.string.cancel)) }
        },
    )
}
