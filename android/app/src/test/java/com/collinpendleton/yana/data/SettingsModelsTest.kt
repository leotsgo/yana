package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** The payloads below are the server's own shapes (internal/server), trimmed. */
class SettingsModelsTest {
    @Test fun sessionListCarriesExpiryAndRevocation() {
        val r = YanaJson.decodeFromString(
            SessionsResponse.serializer(),
            """{"sessions":[
                {"id":"01S1","label":"android · Pixel 8","created_at":"2026-09-20T10:00:00Z",
                 "last_used_at":"2026-09-26T09:00:00Z","expires_at":"2026-10-20T10:00:00Z",
                 "revoked_at":null,"current":true},
                {"id":"01S2","label":"mac · Macintosh; Intel Mac OS X 10_15_7","created_at":"2026-09-18T10:00:00Z",
                 "last_used_at":"2026-09-25T22:00:00Z","expires_at":"2026-09-25T22:30:00Z",
                 "revoked_at":"2026-09-25T22:31:00Z","current":false}
               ]}""",
        )
        assertEquals(2, r.sessions.size)
        assertNull(r.sessions[0].revokedAt)
        assertEquals("2026-10-20T10:00:00Z", r.sessions[0].expiresAt)
        assertEquals("2026-09-25T22:31:00Z", r.sessions[1].revokedAt)
        assertEquals(true, r.sessions[0].current)
    }

    @Test fun usersListCarriesTheOwnerFlag() {
        val r = YanaJson.decodeFromString(
            UsersResponse.serializer(),
            """{"users":[
                {"id":"01U1","username":"sam","is_owner":true,"created_at":"2026-09-01T10:00:00Z"},
                {"id":"01U2","username":"ada","is_owner":false,"created_at":"2026-09-10T10:00:00Z"}
               ]}""",
        )
        assertEquals("ada", r.users[1].username)
        assertEquals(false, r.users[1].isOwner)
        assertEquals(true, r.users[0].isOwner)
    }

    @Test fun spaceDetailCarriesRoleAndMembers() {
        val r = YanaJson.decodeFromString(
            SpaceDetail.serializer(),
            """{"name":"homelab","label":"The homelab","role":"owner","members":[
                {"user":"01U1","role":"owner","id":"01U1","username":"sam"},
                {"user":"ada","role":"editor"},
                {"user":"01U9","role":"viewer"}
               ]}""",
        )
        assertEquals("owner", r.role)
        assertEquals(3, r.members.size)
        assertEquals("sam", r.members[0].displayName)
        assertEquals("ada", r.members[1].displayName)
        assertNull(r.members[2].id)
        assertEquals("01U9", r.members[2].displayName)
    }

    @Test fun guideAnswerFoundAndNotYetIndexed() {
        val found = YanaJson.decodeFromString(
            GuideResponse.serializer(),
            """{"id":"01N1","path":"Start here.md","created":false}""",
        )
        assertEquals("01N1", found.id)
        assertEquals(false, found.created)
        val deferred = YanaJson.decodeFromString(
            GuideResponse.serializer(),
            """{"path":"homelab/Start here.md","created":true}""",
        )
        assertNull(deferred.id)
        assertEquals(true, deferred.created)
    }

    @Test fun updateSpaceRequestWritesTheWholeList() {
        val body = YanaJson.encodeToString(
            UpdateSpaceRequest.serializer(),
            UpdateSpaceRequest(
                name = "The homelab",
                members = listOf(MemberSpec("sam", "owner"), MemberSpec("ada", "editor")),
            ),
        )
        assertEquals("""{"name":"The homelab","members":[{"user":"sam","role":"owner"},{"user":"ada","role":"editor"}]}""", body)
    }
}
