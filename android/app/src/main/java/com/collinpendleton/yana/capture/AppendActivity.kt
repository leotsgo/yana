package com.collinpendleton.yana.capture

import android.app.Activity
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.collinpendleton.yana.R
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.AppendOutcome
import com.collinpendleton.yana.data.CaptureKit
import com.collinpendleton.yana.ui.theme.YanaTheme
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/**
 * The reusable append behind [CaptureIntents.ACTION_APPEND]: a
 * timestamped line onto a chosen note, no editor, no navigation. The
 * share target and automation apps both end here; automation calls it
 * with the note's id and the text, and the activity says what happened
 * and leaves.
 */
class AppendActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        CapturePerf.mark("append")
        val noteId = intent.getStringExtra(CaptureIntents.NOTE_ID)?.trim().orEmpty()
        val text = intent.getStringExtra(CaptureIntents.TEXT)?.trim().orEmpty()
        val app = application as YanaApp
        setContent { YanaTheme { AppendPage(app, noteId, text) } }
    }
}

@Composable
private fun AppendPage(app: YanaApp, noteId: String, text: String) {
    val context = LocalContext.current
    LaunchedEffect(noteId, text) {
        if (noteId.isEmpty() || text.isEmpty()) {
            Toast.makeText(context, app.getString(R.string.append_needs_both), Toast.LENGTH_SHORT).show()
            (context as? Activity)?.finish()
            return@LaunchedEffect
        }
        app.appScope.launch {
            val outcome = app.capture.appendBlock(noteId, CaptureKit.timestampedLine(text))
            val said = when (outcome) {
                is AppendOutcome.Done ->
                    if (outcome.local) app.getString(R.string.append_done_local)
                    else app.getString(R.string.append_done)
                AppendOutcome.NotOnDevice -> app.getString(R.string.share_not_on_device)
                AppendOutcome.NotFound -> app.getString(R.string.share_not_found)
            }
            CapturePerf.done("append")
            withContext(Dispatchers.Main) {
                Toast.makeText(context, said, Toast.LENGTH_SHORT).show()
                (context as? Activity)?.finish()
            }
        }
    }
    Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        Surface(
            shape = MaterialTheme.shapes.medium,
            color = MaterialTheme.colorScheme.surfaceContainer,
            modifier = Modifier.padding(32.dp),
        ) {
            Column(
                Modifier.padding(horizontal = 24.dp, vertical = 20.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp, Alignment.CenterVertically),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                CircularProgressIndicator(strokeWidth = 3.dp)
                Text(stringResource(R.string.append_working), style = MaterialTheme.typography.bodyMedium)
            }
        }
    }
}
