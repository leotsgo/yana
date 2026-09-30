package com.collinpendleton.yana.data

import android.content.Context
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.collinpendleton.yana.YanaApp
import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.CancellationException

/**
 * The background half of the replica: a periodic job that pulls the
 * spaces, the note list, and the tree whenever the network is there, so
 * the replica is warm when the network goes away. The screens also sync
 * on pull-to-refresh; this keeps it fresh while the app sits idle.
 */
class SyncWorker(context: Context, params: WorkerParameters) : CoroutineWorker(context, params) {
    override suspend fun doWork(): Result {
        val app = applicationContext as? YanaApp ?: return Result.failure()
        return try {
            app.repo.sync()
            Result.success()
        } catch (e: CancellationException) {
            throw e
        } catch (e: YanaClient.NotSignedIn) {
            Result.success() // nothing to sync until the next sign-in
        } catch (e: IOException) {
            // Offline or unreachable: the next period (or the next screen)
            // tries again.
            Result.retry()
        } catch (e: Exception) {
            Result.failure()
        }
    }
}

object SyncScheduler {
    private const val WORK = "replica-sync"

    /** Schedules the periodic sync; safe to call on every app start. */
    fun schedule(context: Context) {
        val request = PeriodicWorkRequestBuilder<SyncWorker>(6, TimeUnit.HOURS)
            .setConstraints(
                Constraints.Builder()
                    .setRequiredNetworkType(NetworkType.CONNECTED)
                    .build(),
            )
            .build()
        WorkManager.getInstance(context).enqueueUniquePeriodicWork(WORK, ExistingPeriodicWorkPolicy.KEEP, request)
    }
}
