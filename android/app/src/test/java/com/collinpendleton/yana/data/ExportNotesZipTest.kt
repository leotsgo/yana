package com.collinpendleton.yana.data

import kotlinx.coroutines.runBlocking
import mockwebserver3.Dispatcher
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

/** The space export against a fake server: the path, the auth, and the bytes. */
class ExportNotesZipTest {
    private lateinit var server: MockWebServer
    private var validAccess = "access-1"

    @Before fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                return when {
                    request.url.encodedPath == "/api/spaces/homelab/export/notes.zip" -> {
                        val auth = request.headers["Authorization"]
                        if (auth != "Bearer $validAccess") {
                            json(401, """{"error":"sign in first"}""")
                        } else {
                            MockResponse.Builder().code(200)
                                .addHeader("Content-Type", "application/zip")
                                .body("PK-zip-bytes").build()
                        }
                    }
                    request.url.encodedPath == "/api/spaces/gone/export/notes.zip" ->
                        json(404, """{"error":"no such space"}""")
                    else -> json(404, """{"error":"no"}""")
                }
            }
        }
        server.start()
    }

    @After fun stop() = server.close()

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).addHeader("Content-Type", "application/json").body(body).build()

    private fun client(): YanaClient {
        val store = MemorySessionStore(
            Session(
                server = server.url("/").toString(), userId = "u", username = "sam", isOwner = false,
                sessionId = "s1", accessToken = "access-1", accessExpiresAt = System.currentTimeMillis() + 600_000,
                refreshToken = "refresh-1",
            ),
        )
        return YanaClient(store, deviceLabel = "android · test") { System.currentTimeMillis() }
    }

    @Test fun downloadsTheZipWithTheSessionAuth() = runBlocking {
        val zip = client().exportNotesZip("homelab")
        assertEquals("PK-zip-bytes", String(zip))
        val path = server.takeRequest().url.encodedPath
        assertEquals("/api/spaces/homelab/export/notes.zip", path)
    }

    @Test fun aRefusedExportCarriesTheServersReason() = runBlocking {
        try {
            client().exportNotesZip("gone")
            throw AssertionError("expected the export to fail")
        } catch (e: java.io.IOException) {
            assertTrue(e.message!!, e.message!!.lowercase().contains("no such space"))
        }
    }

    @Test fun theRootHasNoZipOfItsOwn() = runBlocking {
        try {
            client().exportNotesZip("")
            throw AssertionError("expected the export to fail")
        } catch (e: java.io.IOException) {
            assertTrue(e.message!!, e.message!!.contains("root"))
        }
    }
}
