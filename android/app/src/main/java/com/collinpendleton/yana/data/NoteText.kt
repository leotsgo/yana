package com.collinpendleton.yana.data

/**
 * Deriving the text search reads from what the API hands over, the same
 * way the server's scanner derives it (internal/scanner, internal/render):
 * a markdown note's body is the file after its frontmatter block; an HTML
 * note's is the source with tags stripped. Keeping these small rules in
 * step with Go is what makes the offline index match the server's.
 */

/**
 * The body of a markdown note: the content after the frontmatter block,
 * or the whole file when the block is missing or never closes. Mirrors
 * Go's frontmatter.Parse line for line: lines are cut at \n with a
 * trailing \r tolerated, the opening line must be exactly ---, the
 * closing line --- or ..., and nothing is trimmed from the body itself.
 */
fun markdownBody(md: String): String {
    val firstNl = md.indexOf('\n')
    val first = if (firstNl < 0) md else md.substring(0, firstNl)
    if (first.trimEnd('\r') != "---") return md
    if (firstNl < 0) return md
    var offset = firstNl + 1
    while (offset <= md.length) {
        val nextNl = md.indexOf('\n', offset)
        val line = if (nextNl < 0) md.substring(offset) else md.substring(offset, nextNl)
        val t = line.trimEnd('\r')
        if (t == "---" || t == "...") {
            return if (nextNl < 0) "" else md.substring(nextNl + 1)
        }
        if (nextNl < 0) {
            // Unterminated block: the whole file is body.
            return md
        }
        offset = nextNl + 1
    }
    return md
}

private val htmlTag = Regex("(?s)<script.*?</script>|<style.*?</style>|<[^>]+>")
private val whitespace = Regex("\\s+")

/** An HTML note reduced to text for indexing, the server's StripHTML. */
fun stripHtml(src: String): String = whitespace.replace(htmlTag.replace(src, " ").trim(), " ")

/**
 * The (body, raw) pair the replica stores for a note's content: body is
 * what search reads, raw is what offline reading shows. For markdown
 * they are the same text; for HTML the raw is the source itself.
 */
fun indexText(kind: String, content: String): Pair<String, String> = when (kind) {
    "html" -> stripHtml(content) to content
    else -> markdownBody(content) to markdownBody(content)
}
