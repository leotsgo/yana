package com.collinpendleton.yana

import android.content.Intent
import android.os.Bundle
import android.graphics.Color
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.lifecycleScope
import androidx.navigation.NavHostController
import androidx.navigation.compose.rememberNavController
import com.collinpendleton.yana.capture.CaptureIntents
import com.collinpendleton.yana.capture.CapturePerf
import com.collinpendleton.yana.capture.refreshNewNoteWidgets
import com.collinpendleton.yana.capture.showOverLock
import com.collinpendleton.yana.ui.NoteRoute
import com.collinpendleton.yana.ui.YanaNavHost
import com.collinpendleton.yana.ui.theme.ThemeMode
import com.collinpendleton.yana.ui.theme.YanaTheme
import kotlinx.coroutines.launch

/**
 * What an entry point asked the shell to end at. Compose actions make
 * their note asynchronously (the replica first, the network later) and
 * then become [Open].
 */
private sealed interface Pending {
    data object Compose : Pending

    data object Today : Pending

    data class Open(val id: String, val title: String, val edit: Boolean) : Pending
}

class MainActivity : ComponentActivity() {
    private var pending by mutableStateOf<Pending?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        // A recreation hands the same intent back; the entry point ran
        // once already and must not run again.
        if (savedInstanceState == null) take(intent)
        val app = yana
        setContent {
            val mode by app.prefs.themeMode.collectAsStateWithLifecycle()
            val dark = when (mode) {
                ThemeMode.System -> isSystemInDarkTheme()
                ThemeMode.Light -> false
                ThemeMode.Dark -> true
            }
            // Status and navigation bar icons follow the app's theme, not only the system's.
            DisposableEffect(dark) {
                val bars = SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT) { dark }
                enableEdgeToEdge(statusBarStyle = bars, navigationBarStyle = bars)
                onDispose {}
            }
            val nav = rememberNavController()
            YanaTheme(mode) {
                YanaNavHost(app, nav)
            }
            PendingEffect(app, nav)
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        take(intent)
    }

    override fun onResume() {
        super.onResume()
        // The widget's note list is whatever the replica holds now.
        lifecycleScope.launch { runCatching { refreshNewNoteWidgets(this@MainActivity) } }
    }

    /** Reads one entry point's intent into the pending action. */
    private fun take(intent: Intent?) {
        if (intent == null) return
        if (intent.hasExtra(CaptureIntents.CAPTURE)) {
            // The capture prompt stands on its own; the shell only
            // carries it there.
            startActivity(Intent(this, com.collinpendleton.yana.capture.CaptureActivity::class.java))
            return
        }
        if (intent.getBooleanExtra(CaptureIntents.OVER_LOCK, false)) {
            // Typing over the lock screen: the note is local until the
            // device unlocks and the network parts can run.
            showOverLock()
        }
        val label = intent.getStringExtra(CaptureIntents.PERF_LABEL)
        pending = when {
            intent.hasExtra(CaptureIntents.NEW_NOTE) -> {
                label?.let(CapturePerf::mark)
                Pending.Compose
            }
            intent.hasExtra(CaptureIntents.TODAY) -> {
                label?.let(CapturePerf::mark)
                Pending.Today
            }
            intent.hasExtra(CaptureIntents.NOTE) -> Pending.Open(
                intent.getStringExtra(CaptureIntents.NOTE).orEmpty(),
                intent.getStringExtra(CaptureIntents.NOTE_TITLE).orEmpty(),
                edit = false,
            )
            else -> pending
        }
    }

    /** Turns the pending action into navigation, once its note exists. */
    @androidx.compose.runtime.Composable
    private fun PendingEffect(app: YanaApp, nav: NavHostController) {
        val context = LocalContext.current
        LaunchedEffect(pending) {
            val action = pending ?: return@LaunchedEffect
            when (action) {
                Pending.Compose -> {
                    // The shell may still be at the sign-in screen; a
                    // compose from a tile is dropped there like
                    // everything else until a session exists.
                    if (app.client.session.value == null) {
                        pending = null
                        return@LaunchedEffect
                    }
                    val note = app.capture.newInboxNote()
                    pending = if (note != null) {
                        Pending.Open(note.id, note.title, edit = true)
                    } else {
                        null
                    }
                }
                Pending.Today -> {
                    if (app.client.session.value == null) {
                        pending = null
                        return@LaunchedEffect
                    }
                    val today = app.capture.todayNote()
                    pending = if (today != null) {
                        Pending.Open(today.id, today.path.substringAfterLast('/').removeSuffix(".md"), edit = today.local)
                    } else {
                        null
                    }
                }
                is Pending.Open -> {
                    nav.navigate(NoteRoute(action.id, action.title, edit = action.edit))
                    pending = null
                }
            }
        }
    }
}
