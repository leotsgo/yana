package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The payloads below are the server's own shapes (internal/server), trimmed. */
class ModelsTest {
    @Test fun signInResponse() {
        val r = YanaJson.decodeFromString(
            SignInResponse.serializer(),
            """{"user":{"id":"01J","username":"sam","is_owner":true},
               "tokens":{"access_token":"a.b","access_expires_at":"2026-09-24T01:02:03.123456789Z",
               "refresh_token":"r","session_id":"s1","refresh_expires_at":"2026-10-24T01:02:03Z"}}""",
        )
        val s = Session.of("https://x/", r.user, r.tokens)
        assertEquals("sam", s.username)
        assertTrue(s.isOwner)
        assertEquals("s1", s.sessionId)
        assertEquals(java.time.Instant.parse("2026-09-24T01:02:03.123Z").toEpochMilli(), s.accessExpiresAt)
    }

    @Test fun authStateWithoutAccounts() {
        val st = YanaJson.decodeFromString(AuthState.serializer(), """{"setup_required":true}""")
        assertTrue(st.setupRequired)
        assertNull(st.user)
    }

    @Test fun spacesIncludingTheRoot() {
        val r = YanaJson.decodeFromString(
            SpacesResponse.serializer(),
            """{"spaces":[{"name":"","label":"","notes":2},{"name":"work","label":"Work","notes":5},{"name":"home","label":"","notes":0}]}""",
        )
        assertEquals(listOf("/", "Work", "home"), r.spaces.map { it.displayName })
    }

    @Test fun treeWithFoldersNotesAndUnknownFields() {
        val r = YanaJson.decodeFromString(
            TreeResponse.serializer(),
            """{"spaces":[{"name":"work","notes":2,"children":[
                {"type":"dir","name":"projects","path":"work/projects","children":[
                  {"type":"note","name":"a.md","path":"work/projects/a.md","id":"01A","title":"Alpha","kind":"md","order":1,"future":"x"}]},
                {"type":"note","name":"b.md","path":"work/b.md","id":"01B","title":"","kind":"md","tags":["t"],"public":true}]}]}""",
        )
        val kids = r.spaces.single().children
        assertTrue(kids[0].isDir)
        assertEquals("Alpha", kids[0].children.single().label)
        assertEquals("b.md", kids[1].label) // an empty title falls back to the file name
        assertTrue(kids[1].public)
    }

    @Test fun noteMetadata() {
        val n = YanaJson.decodeFromString(
            Note.serializer(),
            """{"id":"01A","space":"work","path":"work/a.md","title":"Alpha","preview":"p","kind":"md",
               "content_hash":"h","size":12,"mtime":"2026-09-01T00:00:00Z","created":"2026-09-01T00:00:00Z",
               "updated_at":"2026-09-02T00:00:00Z","trusted":false,"tags":["x"],"base":"work","links":[],
               "html":"<p>hi</p>","markdown":"hi","role":"editor","public":false}""",
        )
        assertEquals("hi", n.markdown)
        assertEquals("editor", n.role)
        assertFalse(n.public)
        assertEquals("h", n.contentHash)
        assertFalse(n.trusted)
    }

    @Test fun htmlNoteFields() {
        val n = YanaJson.decodeFromString(
            Note.serializer(),
            """{"id":"01B","space":"work","path":"work/dash.html","title":"Dash","preview":"p","kind":"html",
               "content_hash":"abc","updated_at":"2026-09-02T00:00:00Z","trusted":true,"source":"<p>hi</p>","role":"editor"}""",
        )
        assertTrue(n.trusted)
        assertEquals("abc", n.contentHash)
        assertEquals("<p>hi</p>", n.source)
    }

    @Test fun noteViewUrl() {
        val v = YanaJson.decodeFromString(
            NoteView.serializer(),
            """{"url":"http://192.168.1.4:8081/n/01B?token=tok","expires_at":"2026-09-24T12:00:00Z"}""",
        )
        assertEquals("http://192.168.1.4:8081/n/01B?token=tok", v.url)
    }

    @Test fun saveSourceResponseWithConflictCopy() {
        val r = YanaJson.decodeFromString(
            SaveSourceResponse.serializer(),
            """{"ok":true,"path":"work/dash.html","hash":"new","conflict_copy":"work/dash.conflict-20260924-101010.html"}""",
        )
        assertTrue(r.ok)
        assertEquals("new", r.hash)
        assertEquals("work/dash.conflict-20260924-101010.html", r.conflictCopy)
    }

    @Test fun saveSourceResponseWithoutConflictCopy() {
        val r = YanaJson.decodeFromString(SaveSourceResponse.serializer(), """{"ok":true,"path":"work/dash.html","hash":"new"}""")
        assertNull(r.conflictCopy)
    }

    @Test fun errorBody() {
        val e = YanaJson.decodeFromString(ApiErrorBody.serializer(), """{"error":"no account exists yet","setup_required":true}""")
        assertTrue(e.setupRequired)
    }

    @Test fun publicLinkLive() {
        val r = YanaJson.decodeFromString(
            PublicLinkResponse.serializer(),
            """{"link":{"id":"01L","note_id":"01A","url":"https://c.example/p/tok",
               "created_at":"2026-09-25T01:02:03Z","expires_at":"2026-10-02T01:02:03Z"}}""",
        )
        val link = r.link!!
        assertEquals("01A", link.noteId)
        assertEquals("https://c.example/p/tok", link.url)
        assertEquals("2026-10-02T01:02:03Z", link.expiresAt)
    }

    @Test fun publicLinkNone() {
        val r = YanaJson.decodeFromString(PublicLinkResponse.serializer(), """{"link":null}""")
        assertNull(r.link)
    }

    @Test fun publicLinkNeverExpires() {
        val r = YanaJson.decodeFromString(
            PublicLinkResponse.serializer(),
            """{"link":{"id":"01L","note_id":"01A","url":"https://c.example/p/tok",
               "created_at":"2026-09-25T01:02:03Z","expires_at":null},"created":true}""",
        )
        assertNull(r.link!!.expiresAt)
    }
}
