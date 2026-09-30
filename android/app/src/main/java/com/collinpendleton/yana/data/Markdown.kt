package com.collinpendleton.yana.data

private const val BOM = "\uFEFF"

/**
 * The note text without its YAML frontmatter block (id, created, tags):
 * that block is file bookkeeping, not what a person reads.
 */
fun stripFrontmatter(md: String): String {
    val text = md.removePrefix(BOM)
    if (!text.startsWith("---\n") && !text.startsWith("---\r\n")) return md
    val lines = text.lines()
    val end = (1 until lines.size).firstOrNull { lines[it].trimEnd() == "---" } ?: return md
    return lines.drop(end + 1).joinToString("\n").trimStart('\n', '\r')
}
