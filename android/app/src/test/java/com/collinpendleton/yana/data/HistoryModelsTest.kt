package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The payloads below are the server's own shapes (internal/server), trimmed. */
class HistoryModelsTest {
    @Test fun noteHistoryEntries() {
        val r = YanaJson.decodeFromString(
            HistoryResponse.serializer(),
            """{"entries":[
                {"hash":"9f2c","name":"sam","email":"sam@yana.local","date":"2026-09-20T10:00:00Z",
                 "subject":"update notes.md","kind":"person","path":"work/notes.md"},
                {"hash":"8a1b","name":"claude","email":"claude@agents","date":"2026-09-19T22:00:00Z",
                 "subject":"overnight pass","kind":"agent","path":"work/notes.md","future":true}
               ]}""",
        )
        assertEquals(2, r.entries.size)
        assertEquals("person", r.entries[0].kind)
        assertEquals("work/notes.md", r.entries[1].path)
        assertEquals("claude", r.entries[1].name)
    }

    @Test fun noteHistoryDiff() {
        val r = YanaJson.decodeFromString(HistoryDiffResponse.serializer(), """{"diff":"@@ -1 +1 @@\\n-old\\n+new\\n"}""")
        assertTrue(r.diff.startsWith("@@"))
    }

    @Test fun activityPageWithCursorAndRestoreFlag() {
        val r = YanaJson.decodeFromString(
            ActivityResponse.serializer(),
            """{"entries":[
                {"author":"claude","kind":"agent","from":"2026-09-25T02:00:00Z","to":"2026-09-25T06:00:00Z",
                 "commit":"c1a2","commits":7,
                 "changes":[{"action":"modified","path":"home/garden.md","id":"01G","title":"Garden"},
                            {"action":"renamed","path":"home/yard.md","from":"home/lawn.md"},
                            {"action":"deleted","path":"home/old.md"}]}
               ],
               "next_cursor":"beef","more":true,"restore_allowed":false}""",
        )
        val e = r.entries.single()
        assertEquals(7, e.commits)
        assertEquals("c1a2", e.commit)
        assertEquals(3, e.changes.size)
        assertEquals("01G", e.changes[0].id)
        assertEquals("home/lawn.md", e.changes[1].from)
        assertNull(e.changes[0].from)
        assertEquals("beef", r.nextCursor)
        assertTrue(r.more)
        assertFalse(r.restoreAllowed)
    }

    @Test fun pitPreviewWithMoves() {
        val r = YanaJson.decodeFromString(
            PitPreviewResponse.serializer(),
            """{"preview":{"commit":"c0ffee","subject":"overnight pass","author":"sam","date":"2026-09-24T03:00:00Z",
                "space":"home","added":2,"changed":1,"deleted":0,"moved":1,
                "changes":[
                  {"action":"added","path":"home/new.md","id":"01N","title":"New"},
                  {"action":"changed","path":"home/a.md","id":"01A","title":"A"},
                  {"action":"moved","path":"home/back.md","from":"home/moved.md","id":"01M"}
                ]}}""",
        )
        val p = r.preview!!
        assertEquals("home", p.space)
        assertEquals(2, p.added)
        assertEquals(1, p.moved)
        assertEquals("home/moved.md", p.changes[2].from)
    }

    @Test fun pitRestoreSummary() {
        val r = YanaJson.decodeFromString(
            RestoreSummary.serializer(),
            """{"ok":true,"commit":"d1","tag":"pre-restore-20260925","added":1,"changed":0,"deleted":2,"moved":0}""",
        )
        assertTrue(r.ok)
        assertEquals("pre-restore-20260925", r.tag)
        assertEquals(2, r.deleted)
    }

    @Test fun deletedNotesRow() {
        val r = YanaJson.decodeFromString(
            DeletedNotesResponse.serializer(),
            """{"entries":[
                {"id":"01D","space":"home","path":"home/gone.md","title":"Gone","kind":"md",
                 "created":"2026-08-01T00:00:00Z","deleted_at":"2026-09-25T09:00:00Z",
                 "trash_path":".trash/01D.md","has_file":true,"has_sidecar":false,"in_history":false},
                {"id":"01H","space":"home","path":"home/older.md","title":"","kind":"md",
                 "created":"2026-08-01T00:00:00Z","deleted_at":"2026-09-20T09:00:00Z",
                 "has_file":false,"has_sidecar":false,"in_history":true}
               ]}""",
        )
        assertEquals(2, r.entries.size)
        val first = r.entries[0]
        assertTrue(first.hasFile)
        assertFalse(first.inHistory)
        assertEquals(".trash/01D.md", first.trashPath)
        assertTrue(r.entries[1].inHistory)
        assertNull(r.entries[1].trashPath)
    }

    @Test fun deletedRestoreResultWithConflict() {
        val r = YanaJson.decodeFromString(
            DeletedRestoreResult.serializer(),
            """{"ok":true,"path":"home/gone.conflict-20260925T101010.md","conflict":true,
                "note":{"id":"01D","space":"home","path":"home/gone.conflict-20260925T101010.md","title":"Gone"},
                "deferred":false,"from":"trash"}""",
        )
        assertTrue(r.conflict)
        assertEquals("trash", r.from)
        assertEquals("01D", r.note?.id)
        assertFalse(r.deferred)
    }
}
