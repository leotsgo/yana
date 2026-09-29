package com.collinpendleton.yana.ui.screens

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import com.collinpendleton.yana.data.NoteRepository
import com.collinpendleton.yana.data.PublicLink
import com.collinpendleton.yana.data.userMessage
import com.collinpendleton.yana.ui.YanaIcons
import com.collinpendleton.yana.ui.formatTime
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

/** The expiry choices a new link gets, the web's three. */
private val EXPIRIES = listOf("1d" to "1 day", "1w" to "1 week", "never" to "Never")

/**
 * The note's public link, from the note's menu: the live address with
 * Copy and the phone's share sheet, or the making of one — the web's
 * expiry choices — and Revoke for the one that lives. The link is
 * server state; [onChanged] reports a make or a revoke so the note's
 * public badge follows.
 */
@OptIn(androidx.compose.material3.ExperimentalMaterial3Api::class)
@Composable
fun PublicLinkSheet(
    repo: NoteRepository,
    noteId: String,
    onChanged: (live: Boolean) -> Unit,
    onToast: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var link by remember(noteId) { mutableStateOf<PublicLink?>(null) }
    var loading by remember(noteId) { mutableStateOf(true) }
    var error by remember(noteId) { mutableStateOf<String?>(null) }
    var expiry by remember(noteId) { mutableStateOf("never") }
    var busy by remember(noteId) { mutableStateOf(false) }

    LaunchedEffect(noteId) {
        try {
            link = repo.publicLink(noteId)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = e.userMessage()
        } finally {
            loading = false
        }
    }

    fun copy() {
        val url = link?.url ?: return
        val cm = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        cm.setPrimaryClip(ClipData.newPlainText("public link", url))
        onToast("Copied the link.")
    }

    fun share() {
        val url = link?.url ?: return
        val send = Intent(Intent.ACTION_SEND).apply {
            type = "text/plain"
            putExtra(Intent.EXTRA_TEXT, url)
        }
        runCatching { context.startActivity(Intent.createChooser(send, null)) }
            .onFailure { onToast("No app took the share.") }
    }

    fun make() {
        if (busy) return
        busy = true
        scope.launch {
            try {
                link = repo.createPublicLink(noteId, expiry)
                error = null
                onChanged(true)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                error = e.userMessage()
            } finally {
                busy = false
            }
        }
    }

    fun revoke() {
        if (busy) return
        busy = true
        scope.launch {
            try {
                repo.revokePublicLink(noteId)
                link = null
                error = null
                onChanged(false)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                error = e.userMessage()
            } finally {
                busy = false
            }
        }
    }

    ModalBottomSheet(onDismissRequest = { if (!busy) onDismiss() }) {
        Column(
            Modifier.fillMaxWidth().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text("Public link", style = MaterialTheme.typography.titleMedium)
            val live = link
            when {
                loading -> Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                    Text("Reading the link…", style = MaterialTheme.typography.bodyMedium)
                }
                error != null -> Text(
                    error!!,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.error,
                )
                live == null -> {
                    Text(
                        "Anyone with the address reads this note, read-only, with no account.",
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                        EXPIRIES.forEachIndexed { i, (id, label) ->
                            SegmentedButton(
                                selected = expiry == id,
                                onClick = { expiry = id },
                                shape = SegmentedButtonDefaults.itemShape(i, EXPIRIES.size),
                            ) { Text(label) }
                        }
                    }
                    Button(onClick = ::make, enabled = !busy) { Text("Make a link") }
                }
                else -> {
                    Text(
                        "The note reads at this address until the link is revoked" +
                            (live.expiresAt?.takeIf { it.isNotEmpty() }?.let { " or expires ${formatTime(it)}" } ?: ""),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    SelectionContainer {
                        Text(
                            live.url,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.primary,
                            maxLines = 2,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        Button(onClick = ::copy) {
                            Icon(YanaIcons.Copy, contentDescription = null, modifier = Modifier.size(16.dp))
                            Spacer(Modifier.width(8.dp))
                            Text("Copy")
                        }
                        OutlinedButton(onClick = ::share) { Text("Share") }
                    }
                    HorizontalDivider(color = MaterialTheme.colorScheme.outlineVariant)
                    TextButton(onClick = ::revoke, enabled = !busy) { Text("Revoke the link") }
                }
            }
            Text(
                "A revoked link stops working at once; the note itself does not move.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.padding(bottom = 12.dp))
        }
    }
}
