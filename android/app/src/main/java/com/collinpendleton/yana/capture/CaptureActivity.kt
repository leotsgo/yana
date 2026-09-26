package com.collinpendleton.yana.capture

import android.app.Activity
import android.os.Bundle
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Add
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
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
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.collinpendleton.yana.R
import com.collinpendleton.yana.YanaApp
import com.collinpendleton.yana.data.AppendOutcome
import com.collinpendleton.yana.ui.theme.ThemeMode
import com.collinpendleton.yana.ui.theme.YanaTheme
import kotlinx.coroutines.launch

/**
 * Capture as its own entry: one line onto the end of today's note,
 * without opening it. The second tile, the launcher shortcut, and the
 * home screen's Capture row land here; from the lock screen when the
 * tile called, so a thought does not wait for an unlock. Enter adds
 * the line, back leaves, and the note is never on screen.
 */
class CaptureActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        CapturePerf.mark("capture-line")
        if (intent.getBooleanExtra(CaptureIntents.OVER_LOCK, false)) {
            showOverLock()
        }
        val app = application as YanaApp
        setContent {
            val mode by app.prefs.themeMode.collectAsStateWithLifecycle()
            val dark = when (mode) {
                ThemeMode.System -> isSystemInDarkTheme()
                ThemeMode.Light -> false
                ThemeMode.Dark -> true
            }
            YanaTheme(mode) { CapturePage(app) }
        }
    }
}

/** Typing over the lock screen without an unlock; the window flags on 26. */
internal fun android.app.Activity.showOverLock() {
    if (android.os.Build.VERSION.SDK_INT >= 27) {
        setShowWhenLocked(true)
        setTurnScreenOn(true)
    } else {
        @Suppress("DEPRECATION")
        window.addFlags(
            android.view.WindowManager.LayoutParams.FLAG_SHOW_WHEN_LOCKED or
                android.view.WindowManager.LayoutParams.FLAG_TURN_SCREEN_ON,
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CapturePage(app: YanaApp) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var line by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var note by remember { mutableStateOf<String?>(null) }
    val focus = remember { FocusRequester() }

    LaunchedEffect(Unit) { focus.requestFocus() }

    fun add() {
        val text = line.trim()
        if (text.isEmpty() || busy) return
        busy = true
        scope.launch {
            when (val outcome = app.capture.captureLine(text)) {
                is AppendOutcome.Done -> {
                    CapturePerf.done("capture-line")
                    Toast.makeText(
                        context,
                        if (outcome.local) app.getString(R.string.capture_added_local)
                        else app.getString(R.string.capture_added),
                        Toast.LENGTH_SHORT,
                    ).show()
                    (context as? Activity)?.finish()
                }
                AppendOutcome.NotOnDevice -> {
                    busy = false
                    note = app.getString(R.string.capture_not_on_device)
                }
                AppendOutcome.NotFound -> {
                    busy = false
                    note = app.getString(R.string.capture_no_space)
                }
            }
        }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(stringResource(R.string.capture_title)) },
                navigationIcon = {
                    IconButton(onClick = { (context as? Activity)?.finish() }) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = null)
                    }
                },
            )
        },
    ) { pad ->
        Column(
            Modifier.fillMaxSize().padding(pad).padding(horizontal = 20.dp),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            OutlinedTextField(
                value = line,
                onValueChange = { line = it; note = null },
                placeholder = { Text(stringResource(R.string.capture_hint)) },
                supportingText = { Text(stringResource(R.string.capture_support)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { add() }),
                isError = note != null,
                modifier = Modifier.fillMaxWidth().focusRequester(focus),
            )
            note?.let {
                Text(it, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
            }
            Button(onClick = ::add, enabled = line.isNotBlank() && !busy, modifier = Modifier.heightIn(min = 48.dp)) {
                if (busy) {
                    CircularProgressIndicator(Modifier.width(18.dp).heightIn(max = 18.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(10.dp))
                } else {
                    Icon(Icons.Default.Add, contentDescription = null)
                    Spacer(Modifier.width(8.dp))
                }
                Text(stringResource(R.string.capture_add))
            }
        }
    }
}
