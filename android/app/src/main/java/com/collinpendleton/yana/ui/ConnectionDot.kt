package com.collinpendleton.yana.ui

import androidx.compose.animation.animateColorAsState
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.collinpendleton.yana.data.rt.RtStatus

/**
 * The connection's face: a small dot and a word, nothing more. Offline
 * says work is waiting on the network; syncing shows it settling; live
 * is the quiet state. No toasts, no dialogs — the connection changes
 * on its own and so does the dot.
 */
@Composable
fun ConnectionDot(status: RtStatus, modifier: Modifier = Modifier) {
    val (label, color) = when (status) {
        RtStatus.Offline -> "Offline" to MaterialTheme.colorScheme.outline
        RtStatus.Syncing -> "Syncing" to MaterialTheme.colorScheme.primary
        RtStatus.Live -> "Live" to MaterialTheme.colorScheme.tertiary
    }
    val animated by animateColorAsState(color, label = "dot")
    Row(modifier, horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(
            Modifier
                .size(7.dp)
                .background(animated, CircleShape),
        )
        Text(label, style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}
