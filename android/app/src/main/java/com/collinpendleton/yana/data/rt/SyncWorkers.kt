package com.collinpendleton.yana.data.rt

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.content.pm.ServiceInfo
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.ExistingWorkPolicy
import androidx.work.ForegroundInfo
import androidx.work.NetworkType
import androidx.work.OneTimeWorkRequestBuilder
import androidx.work.OutOfQuotaPolicy
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.collinpendleton.yana.R
import com.collinpendleton.yana.YanaApp
import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CancellationException

/**
 * The background half of realtime sync: opens the socket, flushes the
 * outbox for every note with unconfirmed updates, pulls deltas for the
 * notes the person opened lately (the replica and its search index
 * come along), and closes again. The periodic job is the backstop;
 * the expedited run fires when the app goes to the background with
 * work still queued.
 */
class CrdtSyncWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val app = applicationContext as? YanaApp ?: return Result.failure()
        return try {
            if (app.syncEngine.runBackgroundSync()) Result.success() else Result.retry()
        } catch (e: CancellationException) {
            throw e
        } catch (e: com.collinpendleton.yana.data.YanaClient.NotSignedIn) {
            Result.success() // nothing to sync until the next sign-in
        } catch (e: IOException) {
            Result.retry()
        } catch (e: Exception) {
            Result.failure()
        }
    }

    /** Expedited work runs as a foreground service below API 31. */
    override suspend fun getForegroundInfo(): ForegroundInfo = syncForegroundInfo(applicationContext)
}

/** The quiet notification an expedited run shows while it flushes. */
private fun syncForegroundInfo(context: Context): ForegroundInfo {
    val nm = context.getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
    nm.createNotificationChannel(
        NotificationChannel(CHANNEL, context.getString(R.string.sync_channel_name), NotificationManager.IMPORTANCE_MIN),
    )
    val notification = NotificationCompat.Builder(context, CHANNEL)
        .setSmallIcon(android.R.drawable.stat_notify_sync)
        .setContentTitle(context.getString(R.string.sync_notification_title))
        .setOngoing(true)
        .build()
    return if (Build.VERSION.SDK_INT >= 29) {
        ForegroundInfo(notification.hashCode(), notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
    } else {
        ForegroundInfo(notification.hashCode(), notification)
    }
}

private const val CHANNEL = "crdt-sync"

object CrdtSyncScheduler {
    private const val PERIODIC = "crdt-sync"
    private const val FLUSH = "crdt-flush"

    /** Schedules the periodic backstop; safe to call on every app start. */
    fun schedule(context: Context) {
        val request = PeriodicWorkRequestBuilder<CrdtSyncWorker>(30, TimeUnit.MINUTES)
            .setConstraints(
                Constraints.Builder()
                    .setRequiredNetworkType(NetworkType.CONNECTED)
                    .build(),
            )
            .build()
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(PERIODIC, ExistingPeriodicWorkPolicy.KEEP, request)
    }

    /**
     * Enqueues the expedited flush when the outbox holds anything —
     * the app going to the background with unconfirmed edits, or a
     * start that finds rows a force stop left behind. Without expedited
     * quota the job runs as ordinary work rather than being dropped.
     */
    fun flushIfPending(context: Context, pending: Boolean) {
        if (!pending) return
        val request = OneTimeWorkRequestBuilder<CrdtSyncWorker>()
            .setConstraints(
                Constraints.Builder()
                    .setRequiredNetworkType(NetworkType.CONNECTED)
                    .build(),
            )
            .setExpedited(OutOfQuotaPolicy.RUN_AS_NON_EXPEDITED_WORK_REQUEST)
            .build()
        WorkManager.getInstance(context)
            .enqueueUniqueWork(FLUSH, ExistingWorkPolicy.REPLACE, request)
    }
}
