package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The payloads below are the server's own shapes (internal/server/conflicts.go), trimmed. */
class ConflictModelsTest {
    @Test fun noteCarriesItsConflictCount() {
        val n = YanaJson.decodeFromString(
            Note.serializer(),
            """{"id":"01A","space":"main","path":"main/a.md","title":"A","kind":"md","conflict_count":2}""",
        )
        assertEquals(2, n.conflictCount)
    }

    @Test fun conflictsListWithAndWithoutASurvivor() {
        val r = YanaJson.decodeFromString(
            ConflictsResponse.serializer(),
            """{"conflicts":[
                {"note":{"id":"01C","space":"main","path":"main/a.conflict-20260923T121212.md","title":"A","kind":"md"},
                 "of":{"id":"01A","space":"main","path":"main/a.md","title":"A","kind":"md"}},
                {"note":{"id":"01D","space":"main","path":"main/gone.conflict-20260923-121212-2.md","title":"Gone","kind":"md"}}
            ]}""",
        )
        assertEquals(2, r.conflicts.size)
        assertEquals("01A", r.conflicts[0].of?.id)
        assertNull(r.conflicts[1].of)
    }

    @Test fun noteConflictsAreFullNoteRows() {
        val r = YanaJson.decodeFromString(
            NoteConflictsResponse.serializer(),
            """{"conflicts":[{"id":"01C","space":"main","path":"main/a.conflict-20260923T121212.md",
                "title":"A","preview":"the older text","kind":"md","size":18,
                "mtime":"2026-09-23T12:12:12Z","created":"2026-09-23T12:12:12Z","updated_at":"2026-09-23T12:12:12Z",
                "conflict_of":"01A"}]}""",
        )
        val copy = r.conflicts.single()
        assertEquals("01A", copy.conflictOf)
        assertEquals("main/a.conflict-20260923T121212.md", copy.path)
    }

    @Test fun conflictDiffNamesBothSides() {
        val r = YanaJson.decodeFromString(
            ConflictDiffResponse.serializer(),
            """{"diff":"--- main/a.md\n+++ main/a.conflict-20260923T121212.md\n-the current text\n+the older text\n",
                "mine":{"id":"01A","path":"main/a.md","title":"A"},
                "theirs":{"id":"01C","path":"main/a.conflict-20260923T121212.md","title":"A"}}""",
        )
        assertEquals("01A", r.mine.id)
        assertEquals("main/a.conflict-20260923T121212.md", r.theirs.path)
        assertTrue(r.diff.startsWith("--- main/a.md"))
    }

    @Test fun resolveResponsesWithAndWithoutARenamedPath() {
        val both = YanaJson.decodeFromString(
            ConflictResolveResponse.serializer(),
            """{"ok":true,"action":"both","path":"main/a (older).md"}""",
        )
        assertEquals("main/a (older).md", both.path)
        val mine = YanaJson.decodeFromString(
            ConflictResolveResponse.serializer(),
            """{"ok":true,"action":"mine"}""",
        )
        assertTrue(mine.ok)
        assertNull(mine.path)
    }

    @Test fun actionsUseTheServersWords() {
        assertEquals("mine", ConflictAction.Mine.value)
        assertEquals("theirs", ConflictAction.Theirs.value)
        assertEquals("both", ConflictAction.Both.value)
    }
}
