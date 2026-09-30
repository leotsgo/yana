package com.collinpendleton.yana.ui.activity

import com.collinpendleton.yana.data.ActivityChange
import com.collinpendleton.yana.data.ActivityEntry
import com.collinpendleton.yana.data.ActivityResponse
import com.collinpendleton.yana.data.parseInstant
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** One feed entry stamped with the space it came from. */
data class ActivityRow(val space: String, val entry: ActivityEntry)

/** How far back the feed reaches. */
enum class FeedWindow(val label: String) {
    Seen("Since I last looked"),
    Day("Last 24 hours"),
    Week("Last 7 days"),
    Month("Last 30 days"),
    All("Everything"),
}

/**
 * The window's `since` bound as an ISO timestamp, or null for
 * Everything. "Since I last looked" falls back to a week the first
 * time, the way the web's feed does.
 */
fun sinceIso(window: FeedWindow, seenAt: Long?, nowMs: Long = System.currentTimeMillis()): String? = when (window) {
    FeedWindow.Seen -> Instant.ofEpochMilli(seenAt ?: nowMs - 7 * DAY).toString()
    FeedWindow.Day -> Instant.ofEpochMilli(nowMs - DAY).toString()
    FeedWindow.Week -> Instant.ofEpochMilli(nowMs - 7 * DAY).toString()
    FeedWindow.Month -> Instant.ofEpochMilli(nowMs - 30 * DAY).toString()
    FeedWindow.All -> null
}

private const val DAY = 86_400_000L

/** A timestamp as an age: just now, minutes, hours, days, then the date itself. */
fun formatAgo(iso: String, nowMs: Long = System.currentTimeMillis()): String {
    val t = parseInstant(iso)?.toEpochMilli() ?: return iso
    val s = Math.max(0, (nowMs - t) / 1000)
    return when {
        s < 60 -> "just now"
        s < 3600 -> "${s / 60}m ago"
        s < DAY / 1000 -> "${s / 3600}h ago"
        s < 7 * DAY / 1000 -> "${s / (DAY / 1000)}d ago"
        else -> formatDate(t)
    }
}

/** How long an agent's run lasted: minutes, hours, or days, rounded the way the web rounds it. */
fun formatSpan(fromIso: String, toIso: String): String {
    val from = parseInstant(fromIso)?.toEpochMilli() ?: return ""
    val to = parseInstant(toIso)?.toEpochMilli() ?: return ""
    val s = Math.max(0, (to - from) / 1000)
    return when {
        s < 5400 -> "${Math.max(1, Math.round(s / 60.0))} minutes"
        s < 129_600 -> "${Math.round(s / 3600.0)} hours"
        else -> "${Math.round(s / 86_400.0)} days"
    }
}

private val dayFmt = DateTimeFormatter.ofPattern("MMMM d")

/** A day as the feed heads it: Today, Yesterday, or the date; the year joins when it is not this one. */
fun dayLabel(epochMs: Long, zone: ZoneId = ZoneId.systemDefault(), locale: Locale = Locale.getDefault()): String {
    val date = Instant.ofEpochMilli(epochMs).atZone(zone).toLocalDate()
    val today = java.time.LocalDate.now(zone)
    return when (today.toEpochDay() - date.toEpochDay()) {
        0L -> "Today"
        1L -> "Yesterday"
        else -> {
            val base = date.format(dayFmt.withLocale(locale))
            if (date.year == today.year) base else "$base, ${date.year}"
        }
    }
}

private val dateFmt = DateTimeFormatter.ofPattern("MMM d, yyyy")

/** A timestamp as the date the feed falls back to past a week old. */
fun formatDate(epochMs: Long, zone: ZoneId = ZoneId.systemDefault(), locale: Locale = Locale.getDefault()): String =
    Instant.ofEpochMilli(epochMs).atZone(zone).toLocalDate().format(dateFmt.withLocale(locale))

/** A day of the feed: its label, its rows, and where those rows start in the flat list. */
data class DayGroup(val label: String, val rows: List<ActivityRow>, val start: Int)

/**
 * Groups already-ordered rows under day labels. Rows arrive newest
 * first, so each entry lands in the group of its `to` timestamp and
 * the groups come out in feed order.
 */
fun groupByDay(rows: List<ActivityRow>): List<DayGroup> {
    data class Building(var label: String, val rows: MutableList<ActivityRow>)
    val groups = ArrayList<Building>()
    for (r in rows) {
        val t = parseInstant(r.entry.to)?.toEpochMilli() ?: continue
        val label = dayLabel(t)
        val last = groups.lastOrNull()
        if (last != null && last.label == label) {
            last.rows.add(r)
        } else {
            groups.add(Building(label, ArrayList(listOf(r))))
        }
    }
    var start = 0
    return groups.map { g ->
        val out = DayGroup(g.label, g.rows, start)
        start += g.rows.size
        out
    }
}

/** What a page fetched across the target spaces holds: the merged rows, each space's cursor, and whether older history remains. */
data class MergedFeed(
    val rows: List<ActivityRow>,
    val cursors: Map<String, String>,
    val more: Boolean,
)

/** Merges one page per space into one list, newest first, the way the web's every-space feed does. */
fun mergePages(pages: List<Pair<String, ActivityResponse>>): MergedFeed {
    val rows = pages.flatMap { (space, page) -> page.entries.map { ActivityRow(space, it) } }
        .sortedByDescending { parseInstant(it.entry.to)?.toEpochMilli() ?: 0L }
    return MergedFeed(
        rows = rows,
        cursors = pages.associate { (space, page) -> space to page.nextCursor },
        more = pages.any { it.second.more },
    )
}

/** The action words the feed uses; the server's "modified" reads as "edited". */
fun actionLabel(action: String): String = when (action) {
    "added" -> "added"
    "modified" -> "edited"
    "renamed" -> "renamed"
    "deleted" -> "deleted"
    else -> "changed"
}

/** What a change row shows as its name: the note's title when there is one, its path otherwise. */
fun ActivityChange.displayName(): String = title?.ifEmpty { null } ?: path

/**
 * The home screen's "What changed" line: how many entries landed in
 * the window, and the newest one as a teaser. It never marks anything
 * seen; only opening the feed does.
 */
fun whatsChangedText(rows: List<ActivityRow>, seenAt: Long?): String {
    if (rows.isEmpty()) {
        return if (seenAt == null) "See what changed this week" else "Nothing has changed since you last looked"
    }
    val lead = if (seenAt == null) "this week" else "since you last looked"
    val n = if (rows.size == 1) "1 change" else "${rows.size} changes"
    val teaser = latestTeaser(rows)
    return "$n $lead" + (if (teaser.isNotEmpty()) " — $teaser" else "")
}

private fun latestTeaser(rows: List<ActivityRow>): String {
    val newest = rows.maxByOrNull { parseInstant(it.entry.to)?.toEpochMilli() ?: 0L } ?: return ""
    val who = newest.entry.author
    val ago = formatAgo(newest.entry.to)
    val c = newest.entry.changes.firstOrNull()
    return when {
        newest.entry.changes.size > 1 -> "latest: $who touched ${newest.entry.changes.size} notes, $ago"
        c != null -> "latest: $who ${actionLabel(c.action)} ${c.displayName()}, $ago"
        else -> "latest: $who, $ago"
    }
}
