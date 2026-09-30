package com.collinpendleton.yana.capture

/**
 * The intent contract capture exposes: what the tiles, the widget, the
 * share target, and outside automations call. Every extra travels on an
 * intent to [com.collinpendleton.yana.MainActivity] unless named
 * otherwise.
 */
object CaptureIntents {
    /**
     * The reusable append: adds a timestamped line to a chosen note.
     * Sent to [com.collinpendleton.yana.capture.AppendActivity] with
     * [NOTE_ID] and [TEXT]; automation apps use it as-is.
     */
    const val ACTION_APPEND = "com.collinpendleton.yana.APPEND"

    /** The note a [ACTION_APPEND] append lands in, by id. */
    const val NOTE_ID = "com.collinpendleton.yana.extra.NOTE_ID"

    /** The text an append adds. */
    const val TEXT = "com.collinpendleton.yana.extra.TEXT"

    /** Make a note in the inbox and open the editor on it. Boolean. */
    const val NEW_NOTE = "com.collinpendleton.yana.extra.NEW_NOTE"

    /** Open (or make) today's daily note. Boolean. */
    const val TODAY = "com.collinpendleton.yana.extra.TODAY"

    /** Open the one-line capture prompt; the shell forwards to it. Boolean. */
    const val CAPTURE = "com.collinpendleton.yana.extra.CAPTURE"

    /** Open this note by id; [NOTE_TITLE] carries a display title. */
    const val NOTE = "com.collinpendleton.yana.extra.NOTE"

    const val NOTE_TITLE = "com.collinpendleton.yana.extra.NOTE_TITLE"

    /** Show over the lock screen: the tiles set it so typing starts without an unlock. */
    const val OVER_LOCK = "com.collinpendleton.yana.extra.OVER_LOCK"

    /** Which entry point the timing logs name. */
    const val PERF_LABEL = "com.collinpendleton.yana.extra.PERF_LABEL"
}
