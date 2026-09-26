package com.collinpendleton.yana.capture

import android.os.SystemClock
import android.util.Log
import com.collinpendleton.yana.BuildConfig
import java.util.concurrent.ConcurrentHashMap

/**
 * Capture speed, measured: each fast entry point marks when its intent
 * arrives and reports the time to the first frame the person can type
 * into. Debug builds only — the numbers are for development, not the
 * person's logcat.
 */
object CapturePerf {
    private const val TAG = "CapturePerf"
    private val marks = ConcurrentHashMap<String, Long>()

    /** The moment an entry point's intent landed. */
    fun mark(label: String) {
        marks[label] = SystemClock.elapsedRealtime()
    }

    /** The moment the entry point delivered: a frame that can take typing, or a line that landed. */
    fun done(label: String) {
        val at = marks.remove(label) ?: return
        if (!BuildConfig.DEBUG) return
        Log.d(TAG, "$label took ${SystemClock.elapsedRealtime() - at}ms")
    }
}
