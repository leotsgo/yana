package com.collinpendleton.yana.ui.tasks

/**
 * A task line's inline HTML — bold, italics, code, strikethrough, links,
 * tags and wikilinks, the shape the server's scanner renders — read as
 * plain text plus style spans, so the tasks list shows the row the way
 * the note renders it. Unknown tags keep their text; entities decode;
 * nothing here reaches for a WebView.
 */
object TaskTextParser {

    /** One styled stretch over [TaskText.text]: [start, end). */
    data class Span(val start: Int, val end: Int, val style: Style)

    enum class Style { Bold, Italic, Code, Strike }

    /** A task line as text and the spans that style it. */
    data class Parsed(val text: String, val spans: List<Span>)

    private val entities = mapOf("amp" to "&", "lt" to "<", "gt" to ">", "quot" to "\"", "apos" to "'", "nbsp" to " ")

    private fun styleOf(tag: String): Style? = when (tag) {
        "strong", "b" -> Style.Bold
        "em", "i" -> Style.Italic
        "code" -> Style.Code
        "del", "s", "strike" -> Style.Strike
        else -> null
    }

    /** Reads one line of inline HTML; unbalanced tags close at the end. */
    fun parse(html: String): Parsed {
        val out = StringBuilder()
        val spans = ArrayList<Span>()
        val open = ArrayList<Pair<Style, Int>>()
        var i = 0
        while (i < html.length) {
            val c = html[i]
            when {
                c == '<' && html.startsWith("<!--", i) -> {
                    val end = html.indexOf("-->", i + 4)
                    i = if (end >= 0) end + 3 else html.length
                }
                c == '<' -> {
                    val close = html.indexOf('>', i)
                    if (close < 0) {
                        out.append(html, i, html.length)
                        i = html.length
                        break
                    }
                    val tag = html.substring(i + 1, close).trim()
                    i = close + 1
                    if (tag.startsWith('/')) {
                        val style = styleOf(tag.removePrefix("/").takeWhile { it.isLetterOrDigit() })
                        val hit = open.indexOfLast { it.first == style }
                        if (style != null && hit >= 0) {
                            val (s, start) = open.removeAt(hit)
                            spans.add(Span(start, out.length, s))
                        }
                    } else if (!tag.endsWith('/') && tag.isNotEmpty()) {
                        val name = tag.takeWhile { it.isLetterOrDigit() || it == '-' }
                        styleOf(name)?.let { open.add(it to out.length) }
                    }
                }
                c == '&' -> {
                    val semi = html.indexOf(';', i)
                    val name = if (semi in i + 1..i + 8) html.substring(i + 1, semi) else null
                    val decoded = name?.let { entities[it] ?: entityNumber(it) }
                    if (decoded != null) {
                        out.append(decoded)
                        i = (semi + 1)
                    } else {
                        out.append(c)
                        i++
                    }
                }
                else -> {
                    out.append(c)
                    i++
                }
            }
        }
        while (open.isNotEmpty()) {
            val (s, start) = open.removeAt(open.lastIndex)
            spans.add(Span(start, out.length, s))
        }
        return Parsed(out.toString(), spans)
    }

    /** A numeric entity: `#38`, `#x26`; the basic plane only. */
    private fun entityNumber(name: String): String? {
        val code = when {
            name.startsWith("#x") || name.startsWith("#X") -> name.drop(2).toIntOrNull(16)
            name.startsWith("#") -> name.drop(1).toIntOrNull()
            else -> null
        } ?: return null
        return if (code in 1..0xFFFF) code.toChar().toString() else null
    }
}
