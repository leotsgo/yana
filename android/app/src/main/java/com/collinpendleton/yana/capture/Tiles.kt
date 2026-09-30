package com.collinpendleton.yana.capture

import android.app.PendingIntent
import android.content.Intent
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import com.collinpendleton.yana.MainActivity

/**
 * The quick-settings tiles: one for a new note, one for a line into
 * today's. Both open over the lock screen — the note is made in the
 * replica and typing starts without an unlock; the network parts wait
 * for one, which is their rule, not the tiles'.
 */
class NewNoteTileService : TileService() {
    override fun onStartListening() {
        super.onStartListening()
        qsTile?.let {
            it.state = Tile.STATE_ACTIVE
            it.updateTile()
        }
    }

    override fun onClick() {
        super.onClick()
        launch(this) {
            intent<MainActivity>(this)
                .putExtra(CaptureIntents.NEW_NOTE, true)
                .putExtra(CaptureIntents.PERF_LABEL, "tile")
        }
    }
}

/** The second tile: straight into the one-line capture. */
class CaptureTileService : TileService() {
    override fun onStartListening() {
        super.onStartListening()
        qsTile?.let {
            it.state = Tile.STATE_ACTIVE
            it.updateTile()
        }
    }

    override fun onClick() {
        super.onClick()
        launch(this) { intent<CaptureActivity>(this) }
    }
}

private inline fun <reified T> intent(service: TileService): Intent =
    Intent(service, T::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

private fun launch(service: TileService, make: () -> Intent) {
    val i = make().putExtra(CaptureIntents.OVER_LOCK, true)
    if (Build.VERSION.SDK_INT >= 34) {
        val pi = PendingIntent.getActivity(
            service,
            i.hashCode(),
            i,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        service.startActivityAndCollapse(pi)
    } else {
        @Suppress("StartActivityAndCollapseDeprecated")
        service.startActivityAndCollapse(i)
    }
}
