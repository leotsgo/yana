package com.collinpendleton.yana.data.rt

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject

/**
 * The awareness protocol the web client speaks (y-protocols/awareness
 * over the relay's `aw` frames): a small varint envelope carrying, per
 * client, a clock and a JSON state. The relay passes the bytes through
 * untouched; this codec is their one implementation on Android, pinned
 * by byte-level tests against payloads the real libraries produced.
 *
 * Envelope: `varuint numStates`, then per state `varuint clientID`,
 * `varuint clock`, `varstring JSON`. A removed state is the JSON
 * literal `null` with a clock one higher than the last live one.
 */
object Awareness {
    /**
     * One decoded client state. [stateJson] null means removal.
     */
    data class Entry(val clientID: Long, val clock: Long, val stateJson: String?)

    /** Encodes this device's own state — the only shape we send. */
    fun encode(clientID: Long, clock: Long, stateJson: String?): ByteArray {
        val state = stateJson?.toByteArray(Charsets.UTF_8) ?: "null".toByteArray(Charsets.UTF_8)
        val out = ByteWriter()
        out.varUint(1)
        out.varUint(clientID)
        out.varUint(clock)
        out.varUint(state.size.toLong())
        out.raw(state)
        return out.bytes()
    }

    /** Decodes every entry; null when the payload is malformed. */
    fun decode(payload: ByteArray): List<Entry>? {
        val r = ByteReader(payload)
        val count = r.varUint() ?: return null
        if (count < 0 || count > 1024) return null
        val out = ArrayList<Entry>(count.toInt())
        repeat(count.toInt()) {
            val client = r.varUint() ?: return null
            val clock = r.varUint() ?: return null
            val state = r.varString() ?: return null
            out.add(Entry(client, clock, if (state == "null") null else state))
        }
        return out
    }
}

/** lib0's unsigned varint writing: 7 bits per byte, high bit continues. */
private class ByteWriter {
    private val out = ArrayList<Byte>(16)

    fun varUint(v: Long) {
        var n = v
        while (n > 127) {
            raw(((n and 0x7f) or 0x80).toInt())
            n = n ushr 7
        }
        raw(n.toInt())
    }

    fun raw(bytes: ByteArray) = bytes.forEach { out.add(it) }

    private fun raw(b: Int) {
        out.add(b.toByte())
    }

    fun bytes(): ByteArray = out.toByteArray()
}

/** lib0's unsigned varint reading over a fixed buffer. */
private class ByteReader(private val bytes: ByteArray) {
    private var pos = 0

    fun varUint(): Long? {
        var shift = 0
        var acc = 0L
        while (true) {
            if (pos >= bytes.size) return null
            val b = bytes[pos++].toInt() and 0xff
            acc = acc or ((b and 0x7f).toLong() shl shift)
            if (b and 0x80 == 0) return acc
            shift += 7
            if (shift > 63) return null
        }
    }

    fun varString(): String? {
        val len = varUint() ?: return null
        if (len < 0 || len > bytes.size - pos) return null
        val s = String(bytes, pos, len.toInt(), Charsets.UTF_8)
        pos += len.toInt()
        return s
    }
}

/** Presence identity: the same fields, palette, and name hash the web client uses. */
data class PresenceUser(val name: String, val color: String, val colorLight: String)

/** The web's palette (web/src/sync.ts); keep the order for matching colors. */
private val palette =
    listOf("#c65314", "#7a3fa8", "#2c7fb8", "#33812e", "#b03434", "#a07719", "#0e7c86", "#8a4b6d")

/**
 * The color a name gets, hashed exactly as the web does it: over the
 * first UTF-16 unit of each code point, with 32-bit wraparound, so the
 * same person shows the same color on every device.
 */
fun presenceFor(name: String): PresenceUser {
    var hash = 0
    var i = 0
    while (i < name.length) {
        hash = hash * 31 + name[i].code
        i += Character.charCount(name.codePointAt(i))
    }
    val idx = (Math.abs(hash.toLong()) % palette.size).toInt()
    val color = palette[idx]
    return PresenceUser(name = name, color = color, colorLight = color + "33")
}

/** One peer's presence as the editor renders it. A null [anchor] means no cursor to draw. */
data class PeerCursor(
    val clientID: Long,
    val name: String?,
    val color: String,
    val colorLight: String,
    val anchor: Int?,
    val head: Int?,
) {
    val selection: Boolean get() = anchor != null && head != null && anchor != head
}

/** The awareness state's JSON shape: {"user":{...},"cursor":{"anchor":{...},"head":{...}}|null}. */
object PresenceState {
    private val json = Json

    /** Builds the state JSON; anchor and head are relative-position JSON objects, or null for no cursor. */
    fun encode(user: PresenceUser, anchor: ByteArray?, head: ByteArray?): String {
        fun pos(bytes: ByteArray?): JsonElement = bytes?.let { json.parseToJsonElement(it.decodeToString()) }
            ?: JsonPrimitive(null)
        val obj = buildMap {
            put("user", buildMap {
                put("name", JsonPrimitive(user.name))
                put("color", JsonPrimitive(user.color))
                put("colorLight", JsonPrimitive(user.colorLight))
            }.let { JsonObject(it) })
            put(
                "cursor",
                if (anchor == null || head == null) {
                    JsonPrimitive(null)
                } else {
                    JsonObject(
                        buildMap {
                            put("anchor", pos(anchor))
                            put("head", pos(head))
                        },
                    )
                },
            )
        }
        return JsonObject(obj).toString()
    }

    /**
     * Parses a peer's state: identity, and the cursor's anchor/head as
     * the JSON bytes the document resolves. Null when unparseable.
     */
    fun decode(stateJson: String): Pair<PresenceUser, Pair<ByteArray, ByteArray>?>? {
        return try {
            val obj = json.parseToJsonElement(stateJson).jsonObject
            val userEl = obj["user"] as? JsonObject ?: return null
            val name = (userEl["name"] as? JsonPrimitive)?.content ?: return null
            val color = (userEl["color"] as? JsonPrimitive)?.content ?: return null
            val colorLight = (userEl["colorLight"] as? JsonPrimitive)?.content ?: color + "33"
            val user = PresenceUser(name, color, colorLight)
            val cursor = obj["cursor"] as? JsonObject
            val cursorPair = cursor?.let { c ->
                val a = c["anchor"] as? JsonObject ?: return null
                val h = c["head"] as? JsonObject ?: return null
                a.toString().toByteArray(Charsets.UTF_8) to h.toString().toByteArray(Charsets.UTF_8)
            }
            user to cursorPair
        } catch (_: Exception) {
            null
        }
    }
}
