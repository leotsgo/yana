package com.collinpendleton.yana.ui.history

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.collinpendleton.yana.ui.theme.Mono

/** One rendered line of a unified diff. */
sealed interface DiffLine {
    /** A `@@ -a,b +c,d @@` header; [text] is the whole line. */
    data class Hunk(val text: String) : DiffLine

    /** A body line. [kind] is ' ', '+', '-' or '\\' (the no-newline marker). */
    data class Body(val kind: Char, val text: String) : DiffLine
}

/**
 * Parses the unified diff the server's history endpoint returns into
 * the lines worth reading on a phone: file headers (`diff --git`,
 * `index`, `---`, `+++`) drop away; hunks keep their header and body.
 */
object DiffParser {
    fun parse(diff: String): List<DiffLine> {
        val out = ArrayList<DiffLine>()
        var inHunk = false
        for (raw in diff.lineSequence()) {
            when {
                raw.startsWith("@@") -> {
                    inHunk = true
                    out += DiffLine.Hunk(raw)
                }
                !inHunk -> Unit
                raw.startsWith("+") -> out += DiffLine.Body('+', raw.substring(1))
                raw.startsWith("-") -> out += DiffLine.Body('-', raw.substring(1))
                raw.startsWith("\\") -> out += DiffLine.Body('\\', raw.substring(1))
                else -> out += DiffLine.Body(' ', raw.substring(1))
            }
        }
        return out
    }

    /**
     * Parses the headerless unified diff the conflict endpoint returns:
     * a `---`/`+++` pair naming the two sides, then every body line
     * with no hunks between them.
     */
    fun parseHeaderless(diff: String): List<DiffLine> {
        val out = ArrayList<DiffLine>()
        for (raw in diff.lineSequence()) {
            when {
                raw.startsWith("---") || raw.startsWith("+++") -> Unit
                raw.startsWith("+") -> out += DiffLine.Body('+', raw.substring(1))
                raw.startsWith("-") -> out += DiffLine.Body('-', raw.substring(1))
                raw.startsWith("\\") -> out += DiffLine.Body('\\', raw.substring(1))
                else -> out += DiffLine.Body(' ', raw.substring(1))
            }
        }
        return out
    }
}

/** The diff's own colours, keyed off the theme the way the Identity palette is: muted green for what arrives, the error pair for what goes. */
data class DiffColors(val addBg: Color, val addFg: Color, val delBg: Color, val delFg: Color, val hunkFg: Color)

@Composable
fun diffColors(): DiffColors = if (MaterialTheme.colorScheme.background.luminance() < 0.5f) {
    DiffColors(
        addBg = Color(0xFF1F2A20), addFg = Color(0xFF9CD2AC),
        delBg = Color(0xFF3B2016), delFg = Color(0xFFE0805E),
        hunkFg = Color(0xFFB4AC9E),
    )
} else {
    DiffColors(
        addBg = Color(0xFFE8F1E4), addFg = Color(0xFF226B40),
        delBg = Color(0xFFF7E5DC), delFg = Color(0xFF9A3412),
        hunkFg = Color(0xFF5B564E),
    )
}

/**
 * A diff as a readable phone-width list: one line per row, mono,
 * additions and removals tinted, long lines wrapping instead of
 * scrolling sideways.
 */
@Composable
fun DiffView(diff: String, modifier: Modifier = Modifier) {
    DiffLines(remember(diff) { DiffParser.parse(diff) }, modifier)
}

/** The same list over lines parsed another way — a conflict diff has no hunks. */
@Composable
fun DiffLines(lines: List<DiffLine>, modifier: Modifier = Modifier) {
    val colors = diffColors()
    LazyColumn(modifier) {
        items(lines) { line ->
            when (line) {
                is DiffLine.Hunk -> Text(
                    line.text,
                    style = MaterialTheme.typography.labelSmall.copy(fontFamily = Mono),
                    color = colors.hunkFg,
                    modifier = Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainer).padding(horizontal = 12.dp, vertical = 4.dp),
                )
                is DiffLine.Body -> {
                    val (bg, fg) = when (line.kind) {
                        '+' -> colors.addBg to colors.addFg
                        '-' -> colors.delBg to colors.delFg
                        '\\' -> Color.Transparent to colors.hunkFg
                        else -> Color.Transparent to MaterialTheme.colorScheme.onSurface
                    }
                    Text(
                        line.text.ifEmpty { " " },
                        style = MaterialTheme.typography.bodySmall.copy(fontFamily = Mono, fontSize = 12.sp, lineHeight = 17.sp),
                        color = fg,
                        modifier = Modifier.fillMaxWidth().background(bg).padding(horizontal = 12.dp, vertical = 1.dp),
                    )
                }
            }
        }
    }
}

/** The one-line summary under a diff's title: how many lines each way. */
fun diffCounts(diff: String): Pair<Int, Int> = lineCounts(DiffParser.parse(diff))

/** The same counts over parsed lines, whichever parser made them. */
fun lineCounts(lines: List<DiffLine>): Pair<Int, Int> {
    var add = 0
    var del = 0
    for (l in lines) {
        when ((l as? DiffLine.Body)?.kind) {
            '+' -> add++
            '-' -> del++
        }
    }
    return add to del
}

/** A row of the counts as words: "+12 −3" for a diff with twelve arrivals and three removals. */
@Composable
fun DiffCounts(diff: String, modifier: Modifier = Modifier) {
    val (add, del) = remember(diff) { diffCounts(diff) }
    DiffCountRow(add, del, modifier)
}

@Composable
private fun DiffCountRow(add: Int, del: Int, modifier: Modifier = Modifier) {
    val colors = diffColors()
    Text(
        buildAnnotatedString {
            withStyle(SpanStyle(color = colors.addFg)) { append("+$add") }
            append("  ")
            withStyle(SpanStyle(color = colors.delFg)) { append("−$del") }
        },
        style = MaterialTheme.typography.labelMedium.copy(fontFamily = Mono, fontWeight = FontWeight.SemiBold),
        modifier = modifier,
    )
}

/** The counts row for lines parsed another way, a conflict diff's among them. */
@Composable
fun DiffCounts(lines: List<DiffLine>, modifier: Modifier = Modifier) {
    val (add, del) = remember(lines) { lineCounts(lines) }
    DiffCountRow(add, del, modifier)
}
