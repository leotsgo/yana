package com.collinpendleton.yana.capture

import android.content.Context
import android.content.Intent
import androidx.compose.runtime.Composable
import androidx.compose.ui.unit.dp
import androidx.glance.GlanceId
import androidx.glance.GlanceModifier
import androidx.glance.GlanceTheme
import androidx.glance.LocalContext
import androidx.glance.action.ActionParameters
import androidx.glance.action.actionParametersOf
import androidx.glance.action.clickable
import androidx.glance.appwidget.GlanceAppWidget
import androidx.glance.appwidget.GlanceAppWidgetManager
import androidx.glance.appwidget.GlanceAppWidgetReceiver
import androidx.glance.appwidget.action.actionStartActivity
import androidx.glance.appwidget.provideContent
import androidx.glance.background
import androidx.glance.layout.Alignment
import androidx.glance.layout.Column
import androidx.glance.layout.Row
import androidx.glance.layout.Spacer
import androidx.glance.layout.fillMaxSize
import androidx.glance.layout.fillMaxWidth
import androidx.glance.layout.height
import androidx.glance.layout.padding
import androidx.glance.text.FontWeight
import androidx.glance.text.Text
import androidx.glance.text.TextStyle
import com.collinpendleton.yana.MainActivity
import com.collinpendleton.yana.data.replica.RecentNoteRow

/**
 * The home-screen widget: one tap target that makes a note in the
 * inbox and opens the editor on it, and the last three notes under it,
 * each one a tap from open. The list is whatever the replica held when
 * the widget last refreshed.
 */
class NewNoteWidget : GlanceAppWidget() {
    override suspend fun provideGlance(context: Context, id: GlanceId) {
        val app = context.applicationContext as? com.collinpendleton.yana.YanaApp
        val notes = runCatching { app?.capture?.recentNotes(3) }.getOrNull().orEmpty()
        provideContent { WidgetContent(notes) }
    }
}

/** The widget's receiver; the manifest points APPWIDGET_UPDATE at it. */
class NewNoteWidgetReceiver : GlanceAppWidgetReceiver() {
    override val glanceAppWidget: GlanceAppWidget = NewNoteWidget()
}

/** Refreshes every placed copy of the widget from the replica. */
suspend fun refreshNewNoteWidgets(context: Context) {
    val manager = GlanceAppWidgetManager(context)
    for (id in manager.getGlanceIds(NewNoteWidget::class.java)) {
        runCatching { NewNoteWidget().update(context, id) }
    }
}

// The parameters land as intent extras under the same names every
// other entry point uses.
private val newNoteKey = ActionParameters.Key<Boolean>(CaptureIntents.NEW_NOTE)
private val perfKey = ActionParameters.Key<String>(CaptureIntents.PERF_LABEL)
private val noteKey = ActionParameters.Key<String>(CaptureIntents.NOTE)
private val titleKey = ActionParameters.Key<String>(CaptureIntents.NOTE_TITLE)

@Composable
private fun WidgetContent(notes: List<RecentNoteRow>) {
    val context = LocalContext.current
    val ink = GlanceTheme.colors.onBackground
    Column(
        GlanceModifier.fillMaxSize().background(GlanceTheme.colors.background).padding(10.dp),
    ) {
        Row(
            GlanceModifier.fillMaxWidth().clickable(
                actionStartActivity(
                    Intent(context, MainActivity::class.java),
                    actionParametersOf(newNoteKey to true, perfKey to "widget"),
                ),
            ).padding(vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                "+",
                style = TextStyle(color = GlanceTheme.colors.primary, fontWeight = FontWeight.Bold),
            )
            Spacer(GlanceModifier.padding(start = 8.dp))
            Text(
                "New note",
                style = TextStyle(color = ink, fontWeight = FontWeight.Bold),
            )
        }
        if (notes.isNotEmpty()) {
            Spacer(GlanceModifier.height(2.dp))
            for (n in notes) {
                Column(
                    GlanceModifier.fillMaxWidth().clickable(
                        actionStartActivity(
                            Intent(context, MainActivity::class.java),
                            actionParametersOf(noteKey to n.id, titleKey to n.title),
                        ),
                    ).padding(vertical = 5.dp),
                ) {
                    Text(
                        n.title.ifEmpty { n.path.substringAfterLast('/') },
                        style = TextStyle(color = ink),
                        maxLines = 1,
                    )
                    Text(
                        n.path,
                        style = TextStyle(color = ink),
                        maxLines = 1,
                    )
                }
            }
        }
    }
}
