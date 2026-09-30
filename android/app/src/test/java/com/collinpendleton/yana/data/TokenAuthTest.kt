package com.collinpendleton.yana.data

import java.util.concurrent.TimeUnit
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import mockwebserver3.Dispatcher
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Before
import org.junit.Test
import retrofit2.HttpException

/**
 * The token lifecycle against a fake server that behaves like the real
 * one: access tokens are checked on every call, the refresh token trades
 * for a new access token, and a revoked session refuses both.
 */
class TokenAuthTest {
    private lateinit var server: MockWebServer
    private var now = 1_000_000L
    private var validAccess = "access-1"
    private var revoked = false
    private var refreshes = 0
    private var refreshReturnsGarbage = false

    @Before fun start() {
        server = MockWebServer()
        server.dispatcher = object : Dispatcher() {
            override fun dispatch(request: RecordedRequest): MockResponse {
                val path = request.url.encodedPath
                return when {
                    path == "/api/auth/refresh" -> {
                        refreshes++
                        if (refreshReturnsGarbage) {
                            MockResponse.Builder().code(200).addHeader("Content-Type", "text/html")
                                .body("<html>sign in to the wifi</html>").build()
                        } else if (revoked || !request.body!!.utf8().contains("\"refresh-1\"")) {
                            json(401, """{"error":"sign in again"}""")
                        } else {
                            validAccess = "access-${refreshes + 1}"
                            json(200, tokens(validAccess, now + 15 * 60_000))
                        }
                    }
                    path == "/api/auth/logout" -> {
                        revoked = true
                        json(200, """{"ok":true}""")
                    }
                    path == "/api/spaces" -> {
                        val auth = request.headers["Authorization"]
                        if (revoked || auth != "Bearer $validAccess") json(401, """{"error":"sign in first"}""")
                        else json(200, """{"spaces":[{"name":"work","label":"Work","notes":1}]}""")
                    }
                    else -> json(404, """{"error":"no"}""")
                }
            }
        }
        server.start()
    }

    @After fun stop() = server.close()

    private fun json(code: Int, body: String) =
        MockResponse.Builder().code(code).addHeader("Content-Type", "application/json").body(body).build()

    private fun tokens(access: String, expMillis: Long) =
        """{"tokens":{"access_token":"$access","access_expires_at":"${java.time.Instant.ofEpochMilli(expMillis)}",
           "refresh_token":"refresh-1","session_id":"s1","refresh_expires_at":"2030-01-01T00:00:00Z"}}"""

    private fun client(accessExp: Long): Pair<YanaClient, MemorySessionStore> {
        val store = MemorySessionStore(
            Session(
                server = server.url("/").toString(), userId = "u", username = "sam", isOwner = true,
                sessionId = "s1", accessToken = "access-1", accessExpiresAt = accessExp, refreshToken = "refresh-1",
            ),
        )
        val c = YanaClient(store, deviceLabel = "android · test") { now }
        return c to store
    }

    @Test fun validTokenGoesStraightThrough() = runBlocking {
        val (c, _) = client(now + 10 * 60_000)
        assertEquals("Work", c.api().spaces().spaces.single().label)
        assertEquals(0, refreshes)
    }

    @Test fun expiringTokenIsRefreshedBeforeTheCall() = runBlocking {
        val (c, store) = client(now + 10_000) // inside the refresh margin
        validAccess = "never-matches" // the old token would be refused
        c.api().spaces()
        assertEquals(1, refreshes)
        assertEquals("access-2", store.session.value!!.accessToken)
        assertEquals(2, server.requestCount) // one refresh, one spaces call
    }

    @Test fun rejectedTokenRefreshesOnceAndRetries() = runBlocking {
        val (c, store) = client(now + 10 * 60_000)
        validAccess = "server-rotated-secret" // server restarted with a new secret: our token is now refused
        c.api().spaces()
        assertEquals(1, refreshes)
        assertEquals("access-2", store.session.value!!.accessToken)
    }

    @Test fun restartKeepsTheSessionWithoutThePassword() = runBlocking {
        // A new client over the same stored session: what an app restart does.
        val (_, store) = client(now - 1) // access token already expired on disk
        val second = YanaClient(store, deviceLabel = "android · test") { now }
        assertEquals(1, second.api().spaces().spaces.size)
        assertEquals(1, refreshes)
        assertNotNull(store.session.value)
    }

    @Test fun revokedSessionSignsTheDeviceOut() = runBlocking {
        val (c, store) = client(now + 10 * 60_000)
        revoked = true // revoked on the web
        try {
            c.api().spaces()
            fail("expected a 401")
        } catch (e: HttpException) {
            assertEquals(401, e.code())
        }
        assertNull(store.session.value)
        assertTrue(c.auth.endedReason.value!!.contains("signed out"))
    }

    @Test fun signOutRevokesOnTheServer() = runBlocking {
        val (c, store) = client(now + 10 * 60_000)
        c.signOut()
        assertTrue(revoked)
        assertNull(store.session.value)
        val logout = generateSequence { server.takeRequest(0, TimeUnit.SECONDS) }.first { it.url.encodedPath == "/api/auth/logout" }
        assertTrue(logout.body!!.utf8().contains("refresh-1"))
    }

    @Test fun aRefreshThatIsNotJsonFailsAsIoNotAsACrash() = runBlocking {
        // A captive portal answering 200 with HTML: the decode must surface as an
        // IOException the caller can handle, never as a SerializationException out
        // of the interceptor chain.
        val (c, store) = client(now + 10_000) // inside the refresh margin
        refreshReturnsGarbage = true
        validAccess = "never-matches"
        try {
            c.api().spaces()
            fail("expected the call to fail")
        } catch (e: Exception) {
            // Graceful: the refresh failed as IO, the stale token went out and was
            // refused. Not a SerializationException out of the interceptor chain.
            assertTrue(e.toString(), e is HttpException || e is java.io.IOException)
            if (e is HttpException) assertEquals(401, e.code())
        }
        assertNotNull(store.session.value) // an unreadable answer is not a revocation
    }

    @Test fun signOutOfflineStillForgetsLocally() = runBlocking {
        val (c, store) = client(now + 10 * 60_000)
        server.close()
        c.signOut()
        assertNull(store.session.value)
    }
}
