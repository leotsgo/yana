package com.collinpendleton.yana.ui.activity

import com.collinpendleton.yana.data.ActivityChange
import com.collinpendleton.yana.data.ActivityEntry
import com.collinpendleton.yana.data.ActivityResponse
import java.time.ZoneId
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** The feed's reading layer: windows, ages, day groups, merges, and the home line. */
class FeedFormatTest {
    private fun row(space: String, to: String, author: String = "sam", kind: String = "person", commits: Int = 1) =
        ActivityRow(space, ActivityEntry(author = author, kind = kind, to = to, from = to, commit = to.hashCode().toString(), commits = commits))

    @Test fun sinceWindowBounds() {
        val now = 1_800_000_000_000
        val seen = now - 3_600_000
        // Seen falls back to a week when the device never looked.
        assertEquals(
            java.time.Instant.ofEpochMilli(now - 7 * 86_400_000L),
            java.time.Instant.parse(sinceIso(FeedWindow.Seen, null, now)),
        )
        assertEquals(java.time.Instant.ofEpochMilli(seen), java.time.Instant.parse(sinceIso(FeedWindow.Seen, seen, now)))
        assertEquals(java.time.Instant.ofEpochMilli(now - 86_400_000L), java.time.Instant.parse(sinceIso(FeedWindow.Day, null, now)))
        assertEquals(java.time.Instant.ofEpochMilli(now - 7 * 86_400_000L), java.time.Instant.parse(sinceIso(FeedWindow.Week, null, now)))
        assertEquals(java.time.Instant.ofEpochMilli(now - 30L * 86_400_000L), java.time.Instant.parse(sinceIso(FeedWindow.Month, null, now)))
        assertNull(sinceIso(FeedWindow.All, seen, now))
    }

    @Test fun agesReadRelativeToNow() {
        val now = 1_800_000_000_000
        assertEquals("just now", formatAgo(java.time.Instant.ofEpochMilli(now - 30_000).toString(), now))
        assertEquals("5m ago", formatAgo(java.time.Instant.ofEpochMilli(now - 5 * 60_000).toString(), now))
        assertEquals("3h ago", formatAgo(java.time.Instant.ofEpochMilli(now - 3 * 3_600_000).toString(), now))
        assertEquals("2d ago", formatAgo(java.time.Instant.ofEpochMilli(now - 2L * 86_400_000).toString(), now))
    }

    @Test fun spansRoundTheWayTheWebRounds() {
        assertEquals("45 minutes", formatSpan("2026-09-25T02:00:00Z", "2026-09-25T02:45:00Z"))
        // A minute and a half rounds to 2 both here and on the web (Math.round, half away from zero).
        assertEquals("2 minutes", formatSpan("2026-09-25T02:00:00Z", "2026-09-25T02:01:30Z"))
        assertEquals("1 minutes", formatSpan("2026-09-25T02:00:00Z", "2026-09-25T02:01:10Z"))
        assertEquals("5 hours", formatSpan("2026-09-25T02:00:00Z", "2026-09-25T07:00:00Z"))
        assertEquals("2 days", formatSpan("2026-09-25T02:00:00Z", "2026-09-27T02:00:00Z"))
    }

    @Test fun dayLabelsHeadTheirGroups() {
        val zone = ZoneId.of("UTC")
        val today = java.time.LocalDate.now(zone).atStartOfDay(zone).plusHours(12)
        val yesterday = today.minusDays(1)
        val older = today.minusDays(20)
        assertEquals("Today", dayLabel(today.toInstant().toEpochMilli(), zone))
        assertEquals("Yesterday", dayLabel(yesterday.toInstant().toEpochMilli(), zone))
        // An older day heads with the month and day, not a relative word.
        assertEquals(true, dayLabel(older.toInstant().toEpochMilli(), zone).matches(Regex("[A-Z][a-z]+ \\d+.*")))
    }

    @Test fun rowsGroupByDayWithFlatStarts() {
        // groupByDay labels in the system zone; the rows must be built
        // against the same one, or the test flips when the two zones
        // sit on different days.
        val zone = ZoneId.systemDefault()
        val morning = java.time.LocalDate.now(zone).atStartOfDay(zone).plusHours(9)
        val afternoon = morning.plusHours(4)
        val yesterdaySame = morning.minusDays(1)
        val rows = listOf(
            row("work", afternoon.toInstant().toString()),
            row("work", morning.toInstant().toString()),
            row("home", yesterdaySame.toInstant().toString()),
        )
        val groups = groupByDay(rows)
        assertEquals(2, groups.size)
        assertEquals("Today", groups[0].label)
        assertEquals(2, groups[0].rows.size)
        assertEquals("Yesterday", groups[1].label)
        assertEquals(1, groups[1].rows.size)
        assertEquals(2, groups[1].start) // the second group starts after today's two
    }

    @Test fun pagesMergeNewestFirstWithCursors() {
        val pages = listOf(
            "home" to ActivityResponse(
                entries = listOf(ActivityEntry(author = "sam", to = "2026-09-25T10:00:00Z", commit = "h1")),
                nextCursor = "h2",
                more = true,
            ),
            "work" to ActivityResponse(
                entries = listOf(
                    ActivityEntry(author = "claude", to = "2026-09-25T12:00:00Z", commit = "w1"),
                    ActivityEntry(author = "kim", to = "2026-09-24T09:00:00Z", commit = "w0"),
                ),
                nextCursor = "",
                more = false,
            ),
        )
        val merged = mergePages(pages)
        assertEquals(listOf("w1", "h1", "w0"), merged.rows.map { it.entry.commit })
        assertEquals("work", merged.rows[0].space)
        assertEquals(mapOf("home" to "h2", "work" to ""), merged.cursors)
        assertEquals(true, merged.more)
    }

    @Test fun actionWordsMatchTheWeb() {
        assertEquals("edited", actionLabel("modified"))
        assertEquals("added", actionLabel("added"))
        assertEquals("renamed", actionLabel("renamed"))
        assertEquals("deleted", actionLabel("deleted"))
        assertEquals("changed", actionLabel("whatever"))
    }

    @Test fun changeNamesFallBackToThePath() {
        assertEquals("Garden", ActivityChange(action = "modified", path = "home/garden.md", title = "Garden").displayName())
        assertEquals("home/garden.md", ActivityChange(action = "modified", path = "home/garden.md", title = "").displayName())
        assertEquals("home/gone.md", ActivityChange(action = "deleted", path = "home/gone.md").displayName())
    }

    @Test fun homeLineCountsAndTeases() {
        val seen = 1_700_000_000_000L
        val none = whatsChangedText(emptyList(), null)
        assertEquals("See what changed this week", none)
        assertEquals("Nothing has changed since you last looked", whatsChangedText(emptyList(), seen))
        val rows = listOf(
            row("work", "2026-09-25T10:00:00Z", author = "sam"),
            ActivityRow(
                "home",
                ActivityEntry(
                    author = "claude", kind = "agent", to = "2026-09-25T12:00:00Z", from = "2026-09-25T12:00:00Z",
                    commit = "w1",
                    changes = listOf(ActivityChange(action = "modified", path = "home/g.md", title = "Garden")),
                ),
            ),
        )
        val text = whatsChangedText(rows, seen)
        assertEquals(true, text.startsWith("2 changes since you last looked — latest: claude edited Garden"))
    }
}
