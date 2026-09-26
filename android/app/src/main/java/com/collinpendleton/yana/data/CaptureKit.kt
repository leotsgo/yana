package com.collinpendleton.yana.data

import java.security.SecureRandom

/**
 * The pure half of capture: turning a share into a markdown block,
 * naming an inbox note, expanding the daily-note pattern, and minting
 * the ULIDs an offline compose needs before the server has seen
 * anything. Everything here is free of Android types so the JVM suite
 * can hold it to the web's semantics (web/src/sharelib.ts).
 */
object CaptureKit {
    /** The server's default daily-note pattern (config.DefaultDaily). */
    const val DEFAULT_DAILY_PATTERN = "journal/{YYYY}/{MM}/{YYYY}-{MM}-{DD}.md"

    /** The folder fast capture drops a new note into, under a space. */
    const val DEFAULT_INBOX = "inbox"

    private const val CROCKFORD = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
    private val rng = SecureRandom()

    /**
     * A fresh ULID: 48 bits of epoch milliseconds and 80 of randomness,
     * Crockford base32, 26 uppercase characters — the format the
     * server's scanner.NewID mints and every note id already uses. The
     * id a note is born with offline is the id it keeps after sync
     * creates it on the server, because the create request carries it
     * in the frontmatter and the server's EnsureID keeps an id that is
     * already there.
     */
    fun newUlid(nowMs: Long = System.currentTimeMillis(), random: java.util.Random = rng): String {
        var time = nowMs
        val timeChars = CharArray(10)
        for (i in 9 downTo 0) {
            timeChars[i] = CROCKFORD[(time and 0x1F).toInt()]
            time = time ushr 5
        }
        val randomChars = CharArray(16)
        val bytes = ByteArray(10)
        random.nextBytes(bytes)
        // 80 bits as five 16-bit groups, each split 5+5+5+1 to keep the
        // encoding a straight base-32 read of the bits, like the ULID
        // spec's reference layout.
        var bits = 0L
        var held = 0
        var out = 0
        for (b in bytes) {
            bits = (bits shl 8) or (b.toLong() and 0xFF)
            held += 8
            while (held >= 5 && out < 16) {
                randomChars[out++] = CROCKFORD[((bits shr (held - 5)) and 0x1F).toInt()]
                held -= 5
            }
        }
        while (out < 16) {
            randomChars[out++] = CROCKFORD[((bits shl (5 - held)) and 0x1F).toInt()]
            held = 0
        }
        return String(timeChars) + String(randomChars)
    }

    /** The local date as YYYY-MM-DD, the way the daily note endpoint wants it. */
    fun today(nowMs: Long = System.currentTimeMillis()): String {
        val cal = java.util.Calendar.getInstance()
        cal.timeInMillis = nowMs
        return "%04d-%02d-%02d".format(
            cal.get(java.util.Calendar.YEAR),
            cal.get(java.util.Calendar.MONTH) + 1,
            cal.get(java.util.Calendar.DAY_OF_MONTH),
        )
    }

    /**
     * Fills a path's {YYYY}, {MM}, {DD} and {date} tokens with the
     * date's values — the server's expandDaily, on the device, so an
     * offline Today knows where the daily note lives.
     */
    fun expandDaily(pattern: String, date: String): String {
        val y = date.substring(0, 4)
        val m = date.substring(5, 7)
        val d = date.substring(8, 10)
        return pattern.replace("{YYYY}", y).replace("{MM}", m).replace("{DD}", d).replace("{date}", date)
    }

    /** The daily note's full path in a space, from the server's cached pattern. */
    fun dailyPath(space: String, pattern: String, date: String): String =
        (if (space.isEmpty()) "" else "$space/") + expandDaily(pattern, date)

    /**
     * The next free note name in a folder: Untitled, Untitled 2, 3 …
     * over the lowercase names (as file names, `.md` appended) already
     * taken there. The web's newNote, with the same collision walk.
     */
    fun nextInboxName(taken: Iterable<String>, base: String = "Untitled"): String {
        val used = taken.map { it.lowercase() }.toSet()
        if (!used.contains("$base.md".lowercase())) return base
        var i = 2
        while (used.contains("$base $i.md".lowercase())) i++
        return "$base $i"
    }

    private fun escapeLabel(s: String): String =
        s.replace("\\", "\\\\").replace("[", "\\[").replace("]", "\\]")

    /**
     * The markdown for one shared item, the web's composeShareBlock: a
     * link when a URL came along, the text quoted under it when the
     * text said more than its own name; plain dashed lines otherwise.
     */
    fun shareBlock(rawTitle: String, rawText: String, rawUrl: String): String {
        val title = rawTitle.trim().replace(Regex("\\s+"), " ")
        val url = rawUrl.trim().replace(Regex("[)\\s]")) { c -> if (c.value == ")") "%29" else "" }
        val text = rawText.trim()
        val lines = mutableListOf<String>()
        if (url.isNotEmpty()) {
            val textLines = if (text.isNotEmpty()) text.split('\n') else emptyList()
            val label = escapeLabel(title.ifEmpty { textLines.firstOrNull() ?: url })
            lines.add("- [$label]($url)")
            val rest = if (title.isNotEmpty()) {
                if (text.isNotEmpty() && text != url && text != title) textLines else emptyList()
            } else {
                textLines.drop(1)
            }
            for (l in rest) lines.add("  $l")
        } else {
            val head = if (title.isNotEmpty() && text.isNotEmpty() && text != title) "$title — $text" else text.ifEmpty { title }
            for (l in head.split('\n')) lines.add(if (Regex("^[*-+] ").containsMatchIn(l)) "  $l" else "- $l")
        }
        return lines.joinToString("\n")
    }

    /**
     * The block a capture line becomes: the text as one dashed line per
     * line of input, the web's capture (composeShareBlock with no title
     * or URL).
     */
    fun captureBlock(text: String): String = shareBlock("", text, "")

    /**
     * One line stamped with the time it was captured, for the APPEND
     * intent: `HH:mm` in the local zone, then the text.
     */
    fun timestampedLine(text: String, nowMs: Long = System.currentTimeMillis()): String {
        val cal = java.util.Calendar.getInstance()
        cal.timeInMillis = nowMs
        val stamp = "%02d:%02d".format(cal.get(java.util.Calendar.HOUR_OF_DAY), cal.get(java.util.Calendar.MINUTE))
        val trimmed = text.trim()
        return if (Regex("^[*-+] ").containsMatchIn(trimmed)) "  $trimmed" else "- $stamp $trimmed"
    }

    /**
     * A block joined to a note's current text: a newline first when the
     * text does not end with one, nothing doubled when it does, and the
     * block alone in an empty note.
     */
    fun appendBlock(current: String, block: String): String =
        when {
            current.isEmpty() -> block
            current.endsWith("\n") -> current + block
            else -> "$current\n$block"
        }
}
