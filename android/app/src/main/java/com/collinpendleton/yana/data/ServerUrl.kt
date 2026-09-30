package com.collinpendleton.yana.data

import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/**
 * Turns what a person types into the base URL the client talks to:
 * `notes.example.com` becomes `https://notes.example.com/`, a path is
 * kept (for a server behind a reverse-proxy prefix) and always ends in a
 * slash so relative API paths resolve under it. Returns null when the
 * text is not an http(s) address.
 */
fun normalizeServerUrl(input: String): HttpUrl? {
    val trimmed = input.trim()
    if (trimmed.isEmpty() || trimmed.any { it.isWhitespace() }) return null
    val hasScheme = trimmed.contains("://")
    if (hasScheme && !trimmed.startsWith("http://", true) && !trimmed.startsWith("https://", true)) return null
    val withScheme = if (hasScheme) trimmed else "https://$trimmed"
    val url = withScheme.toHttpUrlOrNull() ?: return null
    if (url.host.isEmpty()) return null
    val path = url.encodedPath.let { if (it.endsWith("/")) it else "$it/" }
    return url.newBuilder().encodedPath(path).query(null).fragment(null).build()
}

/** The URL as a person would write it: no trailing slash on a bare host. */
fun HttpUrl.display(): String = toString().removeSuffix("/")
