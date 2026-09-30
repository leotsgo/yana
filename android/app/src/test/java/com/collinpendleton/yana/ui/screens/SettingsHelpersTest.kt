package com.collinpendleton.yana.ui.screens

import com.collinpendleton.yana.data.SessionInfo
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.SpaceDetail
import com.collinpendleton.yana.data.TreeNode
import java.time.Instant
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class SettingsHelpersTest {
    private fun dir(name: String, vararg children: TreeNode) =
        TreeNode(type = "dir", name = name, path = name, children = children.toList())

    private fun note(name: String, id: String? = null) =
        TreeNode(type = "note", name = name, path = name, id = id)

    @Test fun findsStartHereAnywhereInTheTrees() {
        val trees = listOf(
            listOf(dir("homelab", note("rack.md"))),
            listOf(dir("home", note("garden.md"), dir("docs", note("Start here.md", id = "01G")))),
        )
        assertEquals("01G", findStartHere(trees)?.id)
    }

    @Test fun noStartHereMeansNone() {
        val trees = listOf(listOf(dir("homelab", note("rack.md"), dir("docs", note("readme.md")))))
        assertNull(findStartHere(trees))
    }

    @Test fun directoriesNamedStartHereDoNotCount() {
        val trees = listOf(listOf(dir("Start here.md", note("inside.md"))))
        assertNull(findStartHere(trees))
    }

    @Test fun liveSessionsDropRevokedAndLapsed() {
        val now = Instant.parse("2026-09-26T12:00:00Z")
        val sessions = listOf(
            SessionInfo(id = "1", label = "live", expiresAt = "2026-10-01T00:00:00Z"),
            SessionInfo(id = "2", label = "revoked", expiresAt = "2026-10-01T00:00:00Z", revokedAt = "2026-09-25T00:00:00Z"),
            SessionInfo(id = "3", label = "lapsed", expiresAt = "2026-09-26T11:00:00Z"),
            SessionInfo(id = "4", label = "no expiry said"),
        )
        assertEquals(listOf("1", "4"), liveSessions(sessions, now).map { it.id })
    }

    @Test fun zipSlugFlattensWhatAFilesystemRefuses() {
        assertEquals("homelab", zipSlug("homelab"))
        assertEquals("my-notes-2", zipSlug("my notes/2"))
        assertEquals("notes", zipSlug(""))
        assertEquals(60, zipSlug("a".repeat(80)).length)
    }

    @Test fun spacesWithRolesSurviveASpaceThatWillNotSay() = runBlocking {
        val rows = spacesWithRoles(
            listOf(Space(name = "", label = "", notes = 3), Space(name = "homelab", label = "", notes = 5)),
        ) { name ->
            if (name == "homelab") SpaceDetail(name = name, role = "editor") else throw IllegalStateException("no such space")
        }
        // The root never asks — only the owner's list carries it, and the root is the owner's.
        assertEquals(listOf("owner" to "", "editor" to "homelab"), rows.map { it.role to it.space.name })
    }
}
