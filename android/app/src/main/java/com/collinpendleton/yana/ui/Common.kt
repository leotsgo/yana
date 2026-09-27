package com.collinpendleton.yana.ui

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.ime
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawing
import androidx.compose.foundation.layout.union
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.collinpendleton.yana.ui.theme.Mono
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import com.collinpendleton.yana.data.parseInstant

/** The wordmark: YANA in mono, the slash in the accent. */
@Composable
fun Wordmark(modifier: Modifier = Modifier, size: TextUnit = 20.sp) {
    val accent = MaterialTheme.colorScheme.primary
    Text(
        buildAnnotatedString {
            append("YANA")
            withStyle(SpanStyle(color = accent)) { append("/") }
        },
        modifier = modifier,
        style = TextStyle(fontFamily = Mono, fontWeight = FontWeight.Bold, fontSize = size, letterSpacing = 0.sp),
        color = MaterialTheme.colorScheme.onBackground,
    )
}

/**
 * What a screen that sits above the bottom bar pads: the status bar
 * and the sides, and the keyboard when it is up — never the navigation
 * bar, whose space the bar itself owns.
 */
val ShellInsets: WindowInsets
    @Composable get() = WindowInsets.safeDrawing
        .only(WindowInsetsSides.Top + WindowInsetsSides.Horizontal)
        .union(WindowInsets.ime)

/** Loading, error-with-retry, or empty: the three states a list shows before it has rows. */
@Composable
fun Placeholder(
    loading: Boolean,
    error: String?,
    empty: String?,
    onRetry: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(modifier.fillMaxSize().padding(32.dp), contentAlignment = Alignment.Center) {
        when {
            loading -> CircularProgressIndicator()
            error != null -> Column(horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(error, style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.Center, color = MaterialTheme.colorScheme.error)
                TextButton(onClick = onRetry) { Text("Try again") }
            }
            empty != null -> Text(empty, style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.Center, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

private val dateFormat = DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, FormatStyle.SHORT)

/** A server timestamp in the device's zone and locale, or "" when unreadable. */
fun formatTime(s: String): String =
    parseInstant(s)?.let { dateFormat.format(it.atZone(ZoneId.systemDefault())) } ?: ""

/** Opens the system browser at [url]; a device with nothing to open it hears so instead of crashing. */
fun openUrl(context: Context, url: String) {
    val opened = runCatching {
        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)))
    }.isSuccess
    if (!opened) Toast.makeText(context, "No app on this device opens a web page.", Toast.LENGTH_SHORT).show()
}
