package com.collinpendleton.yana.ui.screens

import android.content.Intent
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.content.FileProvider
import com.collinpendleton.yana.data.Note
import com.collinpendleton.yana.data.YanaClient
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.YanaIcons
import java.io.File
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * Send the note to someone, from the note's menu: the markdown text
 * through the share sheet, or the standalone HTML export — one
 * self-contained file, the same bytes the web's export serves — as an
 * attachment.
 */
@OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class)
@Composable
fun SendNoteSheet(
    client: YanaClient,
    note: Note,
    body: String?,
    onToast: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var busy by remember(note.id) { mutableStateOf(false) }

    val title = note.title.ifEmpty { note.path.substringAfterLast('/') }

    fun sendText() {
        val text = body ?: return
        val send = Intent(Intent.ACTION_SEND).apply {
            type = "text/plain"
            putExtra(Intent.EXTRA_SUBJECT, title)
            putExtra(Intent.EXTRA_TEXT, text)
        }
        runCatching { context.startActivity(Intent.createChooser(send, null)) }
            .onFailure { onToast("No app took the share.") }
    }

    fun sendHtml() {
        if (busy) return
        busy = true
        scope.launch {
            try {
                val bytes = client.exportNoteHtml(note.id)
                val file = File(context.cacheDir, "yana-${zipSlug(title)}.html")
                withContext(Dispatchers.IO) { file.writeBytes(bytes) }
                val uri = FileProvider.getUriForFile(context, context.packageName + ".files", file)
                val send = Intent(Intent.ACTION_SEND).apply {
                    type = "text/html"
                    putExtra(Intent.EXTRA_STREAM, uri)
                    addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                }
                runCatching { context.startActivity(Intent.createChooser(send, null)) }
                    .onFailure { onToast("No app took the share.") }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                onToast(e.userMessage())
            } finally {
                busy = false
            }
        }
    }

    ModalBottomSheet(onDismissRequest = { if (!busy) onDismiss() }) {
        Column(
            Modifier.fillMaxWidth().padding(horizontal = 20.dp),
            verticalArrangement = Arrangement.spacedBy(4.dp),
        ) {
            Text("Send the note", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(bottom = 8.dp))
            if (body != null) {
                Row(
                    Modifier.fillMaxWidth().clickable(enabled = !busy) { sendText() }.padding(vertical = 12.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Icon(YanaIcons.FileText, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
                    Column {
                        Text("Markdown text", style = MaterialTheme.typography.bodyLarge)
                        Text(
                            "The note's own text, as written.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
                HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
            }
            Row(
                Modifier.fillMaxWidth().clickable(enabled = !busy) { sendHtml() }.padding(vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                if (busy) {
                    CircularProgressIndicator(Modifier.size(22.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(2.dp))
                } else {
                    Icon(YanaIcons.Globe, contentDescription = null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Column {
                    Text("HTML file", style = MaterialTheme.typography.bodyLarge)
                    Text(
                        "One self-contained page, ready to keep or send on.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Spacer(Modifier.padding(bottom = 12.dp))
        }
    }
}
