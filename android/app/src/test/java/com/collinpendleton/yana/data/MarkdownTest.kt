package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Test

class MarkdownTest {
    @Test fun stripsTheBlock() =
        assertEquals("# Title\n\nBody\n", stripFrontmatter("---\nid: 01A\ntags: [x]\n---\n# Title\n\nBody\n"))

    @Test fun stripsCrlf() = assertEquals("Body", stripFrontmatter("---\r\nid: 1\r\n---\r\nBody"))

    @Test fun leavesPlainNotes() = assertEquals("# Title\n---\n", stripFrontmatter("# Title\n---\n"))

    @Test fun leavesAnUnclosedBlock() = assertEquals("---\nid: 1\nno end", stripFrontmatter("---\nid: 1\nno end"))
}
