package com.collinpendleton.yana.data

import java.time.Instant
import java.time.OffsetDateTime
import kotlinx.serialization.Serializable

/** A signed-in device: who, where, and the token pair. */
@Serializable
data class Session(
    val server: String,
    val userId: String,
    val username: String,
    val isOwner: Boolean,
    val sessionId: String,
    val accessToken: String,
    /** Epoch milliseconds. */
    val accessExpiresAt: Long,
    val refreshToken: String,
) {
    fun withTokens(t: Tokens): Session = copy(
        sessionId = t.sessionId,
        accessToken = t.access,
        accessExpiresAt = parseInstant(t.accessExpiresAt)?.toEpochMilli() ?: 0,
        refreshToken = t.refresh,
    )

    companion object {
        fun of(server: String, user: User, t: Tokens): Session = Session(
            server = server,
            userId = user.id,
            username = user.username,
            isOwner = user.isOwner,
            sessionId = "",
            accessToken = "",
            accessExpiresAt = 0,
            refreshToken = "",
        ).withTokens(t)
    }
}

/** Parses the server's RFC 3339 timestamps (Go writes nanoseconds and `Z`). */
fun parseInstant(s: String): Instant? {
    if (s.isEmpty()) return null
    return runCatching { Instant.parse(s) }.getOrNull()
        ?: runCatching { OffsetDateTime.parse(s).toInstant() }.getOrNull()
}
