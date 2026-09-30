package com.collinpendleton.yana.data

import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import okhttp3.Authenticator
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.Route

/**
 * Keeps the access token fresh. The interceptor attaches it and swaps an
 * expiring one for a new one before the request goes out; the
 * authenticator handles the 401 a revoked or early-expired token gets,
 * trading the refresh token once. A refresh the server refuses means the
 * session is over (signed out elsewhere, revoked on the web, or idle past
 * its lifetime): the session is cleared and the app returns to sign-in.
 */
class TokenAuth(
    private val store: SessionStore,
    private val http: OkHttpClient,
    private val now: () -> Long = System::currentTimeMillis,
) {
    private val lock = Any()
    private val ended = MutableStateFlow<String?>(null)

    /** Why the last session ended without the person signing out, for the sign-in screen. */
    val endedReason: StateFlow<String?> = ended.asStateFlow()

    fun clearEndedReason() {
        ended.value = null
    }

    val interceptor = Interceptor { chain ->
        val token = validAccessToken()
        val req = chain.request()
        chain.proceed(if (token == null) req else req.withBearer(token))
    }

    val authenticator = object : Authenticator {
        override fun authenticate(route: Route?, response: Response): Request? {
            if (response.priorResponse != null) return null // one retry per call
            val used = response.request.header("Authorization")?.removePrefix("Bearer ") ?: return null
            val fresh = refreshAfterRejection(used) ?: return null
            return response.request.withBearer(fresh)
        }
    }

    /** The access token to send, refreshed first when it is about to lapse. */
    fun validAccessToken(): String? = synchronized(lock) {
        val s = store.session.value ?: return null
        if (s.accessExpiresAt - now() > EXPIRY_MARGIN_MS) return s.accessToken
        return try {
            refreshLocked(s)?.accessToken
        } catch (e: IOException) {
            s.accessToken // offline: send what we have; the call fails on its own terms
        }
    }

    /** A request carrying [used] came back 401; returns a token worth retrying with. */
    fun refreshAfterRejection(used: String): String? = synchronized(lock) {
        val s = store.session.value ?: return null
        if (s.accessToken != used) return s.accessToken // another call refreshed meanwhile
        return try {
            refreshLocked(s)?.accessToken
        } catch (e: IOException) {
            null
        }
    }

    /** Trades the refresh token; null when the server refused it and the session ended. */
    private fun refreshLocked(s: Session): Session? {
        val body = YanaJson.encodeToString(RefreshRequest.serializer(), RefreshRequest(s.refreshToken))
        val req = Request.Builder()
            .url(s.server.trimEnd('/') + "/api/auth/refresh")
            .post(body.toRequestBody(JSON))
            .build()
        http.newCall(req).execute().use { resp ->
            if (resp.code == 401 || resp.code == 403) {
                end("This device was signed out. Sign in again to keep going.")
                return null
            }
            if (!resp.isSuccessful) throw IOException("refresh failed: HTTP ${resp.code}")
            // A 200 that is not a token payload (captive portal, a proxy erroring in
            // HTML) must not escape as a SerializationException: an interceptor may
            // only throw IOException, and anything else kills the dispatcher thread.
            val tokens = try {
                YanaJson.decodeFromString(RefreshResponse.serializer(), resp.body.string()).tokens
            } catch (e: IOException) {
                throw e
            } catch (e: Exception) {
                throw IOException("refresh response was not a token payload", e)
            }
            val next = s.withTokens(tokens)
            store.save(next)
            return next
        }
    }

    private fun end(reason: String) {
        ended.value = reason
        store.save(null)
    }

    companion object {
        /** Refresh this long before the access token lapses, to absorb clock skew and slow networks. */
        const val EXPIRY_MARGIN_MS = 60_000L
        private val JSON = "application/json".toMediaType()

        fun plainClient(): OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(15, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .build()
    }
}

private fun Request.withBearer(token: String): Request =
    newBuilder().header("Authorization", "Bearer $token").build()
