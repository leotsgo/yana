package com.collinpendleton.yana.data.rt

/**
 * The relay protocol's frames and their msgpack encoding
 * (docs/realtime.md). One WebSocket frame is one binary msgpack map;
 * field names are the single-letter wire keys the server and the web
 * client use. This file is the protocol's one declaration on Android:
 * every sender and receiver goes through it, and the byte-level tests
 * pin the encoding against frames the server's own encoder produced,
 * so the three clients cannot drift apart.
 *
 * The codec covers the subset the protocol needs — maps with string
 * keys, string and byte-array values — and skips anything else it
 * meets, so a newer server may add fields without breaking the app.
 */

/** Client-to-server frame. Null fields are omitted, like the server's omitempty. */
data class ClientFrame(
    val t: String,
    val n: String? = null,
    val sv: ByteArray? = null,
    val u: ByteArray? = null,
    val a: String? = null,
    val p: ByteArray? = null,
    val s: String? = null,
)

/** Server-to-client frame. */
data class ServerFrame(
    val t: String,
    val n: String? = null,
    val u: ByteArray? = null,
    val a: String? = null,
    val p: ByteArray? = null,
    val path: String? = null,
    val s: String? = null,
    val c: String? = null,
    val r: String? = null,
)

/** Message kinds, the `t` values both sides speak. */
object Rt {
    const val SUB = "sub"
    const val UNSUB = "unsub"
    const val UPD = "upd"
    const val AW = "aw"
    const val PING = "ping"
    const val WATCH = "watch"
    const val SUBD = "subd"
    const val PONG = "pong"
    const val MOVED = "moved"
    const val DELETED = "deleted"
    const val ERR = "err"
    const val WATCHED = "watchd"
    const val CHANGED = "chg"
}

/** Encodes one frame; field order matches the server's structs. */
fun encodeClientFrame(f: ClientFrame): ByteArray =
    encodeFrame(listOfNotNull("t" to f.t, f.n?.let { "n" to it }, f.sv?.let { "sv" to it }, f.u?.let { "u" to it }, f.a?.let { "a" to it }, f.p?.let { "p" to it }, f.s?.let { "s" to it }))

/** Encodes one server frame; the test suite's fake relay speaks this. */
fun encodeServerFrame(f: ServerFrame): ByteArray =
    encodeFrame(
        listOfNotNull(
            "t" to f.t,
            f.n?.let { "n" to it },
            f.u?.let { "u" to it },
            f.a?.let { "a" to it },
            f.p?.let { "p" to it },
            f.path?.let { "path" to it },
            f.s?.let { "s" to it },
            f.c?.let { "c" to it },
            f.r?.let { "r" to it },
        ),
    )

private fun encodeFrame(fields: List<Pair<String, Any>>): ByteArray {
    val out = Msgpack.Writer()
    out.mapHeader(fields.size)
    for ((k, v) in fields) {
        out.str(k)
        when (v) {
            is String -> out.str(v)
            is ByteArray -> out.bin(v)
        }
    }
    return out.bytes()
}

/** Decodes one server frame; null when the bytes are not one. */
fun decodeServerFrame(b: ByteArray): ServerFrame? = decodeFrame(b)?.let {
    ServerFrame(
        t = it.getValue("t") as String,
        n = it["n"] as? String,
        u = it["u"] as? ByteArray,
        a = it["a"] as? String,
        p = it["p"] as? ByteArray,
        path = it["path"] as? String,
        s = it["s"] as? String,
        c = it["c"] as? String,
        r = it["r"] as? String,
    )
}

/** Decodes one client frame; the test suite's fake relay reads with this. */
fun decodeClientFrame(b: ByteArray): ClientFrame? = decodeFrame(b)?.let {
    ClientFrame(
        t = it.getValue("t") as String,
        n = it["n"] as? String,
        sv = it["sv"] as? ByteArray,
        u = it["u"] as? ByteArray,
        a = it["a"] as? String,
        p = it["p"] as? ByteArray,
        s = it["s"] as? String,
    )
}

/** Reads one frame map into a name-to-value bag; string and byte values are kept, anything else skipped. */
private fun decodeFrame(b: ByteArray): Map<String, Any>? = try {
    val r = Msgpack.Reader(b)
    val count = r.mapCount() ?: return null
    val out = LinkedHashMap<String, Any>(count)
    repeat(count) {
        val key = r.string()
        val type = r.peekFormat()
        // Keys and values interleave on the wire; each value is
        // dispatched the moment its key names it.
        when (type) {
            0xc4, 0xc5, 0xc6 -> out[key] = r.bytes()
            in 0xa0..0xbf, 0xd9, 0xda, 0xdb -> out[key] = if (key in BYTE_FIELDS) r.bytes() else r.string()
            else -> r.skip()
        }
    }
    if (!r.done || !out.containsKey("t")) null else out
} catch (_: Msgpack.Malformed) {
    null
} catch (_: IndexOutOfBoundsException) {
    null
} catch (_: IllegalArgumentException) {
    null
}

/** Fields whose values are byte arrays on the wire. */
private val BYTE_FIELDS = setOf("sv", "u", "p")

/**
 * The msgpack subset the wire needs. Writer and Reader are throwaway
 * single-message objects, small enough to stay allocation-light.
 */
object Msgpack {

    /** Thrown by the reader on bytes that are not the subset it speaks. */
    class Malformed(why: String) : Exception(why)

    class Writer internal constructor() {
        private var buf = ByteArray(64)
        private var len = 0

        fun bytes(): ByteArray = buf.copyOf(len)

        fun mapHeader(count: Int) {
            when {
                count < 16 -> byte((0x80 or count).toByte())
                count < 0x10000 -> {
                    byte(0xde.toByte())
                    u16(count)
                }
                else -> {
                    byte(0xdf.toByte())
                    u32(count)
                }
            }
        }

        fun str(s: String) {
            val b = s.toByteArray(Charsets.UTF_8)
            when {
                b.size < 32 -> byte((0xa0 or b.size).toByte())
                b.size < 0x100 -> {
                    byte(0xd9.toByte())
                    u8(b.size)
                }
                b.size < 0x10000 -> {
                    byte(0xda.toByte())
                    u16(b.size)
                }
                else -> {
                    byte(0xdb.toByte())
                    u32(b.size)
                }
            }
            raw(b)
        }

        fun bin(b: ByteArray) {
            when {
                b.size < 0x100 -> {
                    byte(0xc4.toByte())
                    u8(b.size)
                }
                b.size < 0x10000 -> {
                    byte(0xc5.toByte())
                    u16(b.size)
                }
                else -> {
                    byte(0xc6.toByte())
                    u32(b.size)
                }
            }
            raw(b)
        }

        private fun raw(b: ByteArray) {
            ensure(len + b.size)
            System.arraycopy(b, 0, buf, len, b.size)
            len += b.size
        }

        private fun byte(v: Byte) {
            ensure(len + 1)
            buf[len++] = v
        }

        private fun u8(v: Int) = byte(v.toByte())

        private fun u16(v: Int) {
            byte((v ushr 8).toByte())
            byte(v.toByte())
        }

        private fun u32(v: Int) {
            byte((v ushr 24).toByte())
            byte((v ushr 16).toByte())
            byte((v ushr 8).toByte())
            byte(v.toByte())
        }

        private fun ensure(need: Int) {
            if (need <= buf.size) return
            var cap = buf.size
            while (cap < need) cap *= 2
            buf = buf.copyOf(cap)
        }
    }

    class Reader internal constructor(private val buf: ByteArray) {
        private var pos = 0

        val done: Boolean get() = pos == buf.size

        /** Reads a map's header and returns how many key/value pairs follow; null when the frame is not a map. */
        fun mapCount(): Int? = when (val type = peek()) {
            in 0x80..0x8f -> (type and 0x0f).also { advance() }
            0xde -> {
                advance()
                u16()
            }
            0xdf -> {
                advance()
                u32()
            }
            else -> null
        }?.takeIf { it in 0..64 }

        fun string(): String = String(stringBytes(), Charsets.UTF_8)

        /** The format byte at the current position, without consuming it. */
        fun peekFormat(): Int = peek()

        fun bytes(): ByteArray = when (val type = peek()) {
            0xc4 -> { advance(); take(u8()) }
            0xc5 -> { advance(); take(u16()) }
            0xc6 -> { advance(); take(u32()) }
            // A byte field sent through the raw/str encodings reads the
            // same on the wire; accept it rather than drop the frame.
            in 0xa0..0xbf, 0xd9, 0xda, 0xdb -> stringBytes()
            else -> throw Malformed("expected bytes, got format 0x${type.toString(16)}")
        }

        fun skip() = skipAt(0)

        private fun skipAt(depth: Int) {
            // A hostile frame could nest deeply enough to exhaust the
            // stack; the protocol never nests at all.
            if (depth > 32) throw Malformed("value nested too deeply")
            when (val type = peek()) {
                0xc0, 0xc2, 0xc3, in 0x70..0x7f -> advance() // nil, bool, fixext*
                in 0x00..0x7f -> advance() // positive fixint
                in 0xe0..0xff -> advance() // negative fixint
                0xcc, 0xd0 -> { advance(); take(1) }
                0xcd, 0xd1 -> { advance(); take(2) }
                0xce, 0xd2, 0xca -> { advance(); take(4) }
                0xcf, 0xd3, 0xcb -> { advance(); take(8) }
                0xd4, 0xd5 -> { advance(); take(2) }
                0xd6, 0xd7 -> { advance(); take(4) }
                0xd8 -> { advance(); take(8) }
                in 0xa0..0xbf -> stringBytes()
                0xd9, 0xda, 0xdb -> stringBytes()
                0xc4, 0xc5, 0xc6 -> bytes()
                in 0x90..0x9f -> {
                    val n = type and 0x0f
                    advance()
                    repeat(n) { skipAt(depth + 1) }
                }
                0xdc -> { advance(); repeat(u16()) { skipAt(depth + 1) } }
                0xdd -> { advance(); repeat(u32()) { skipAt(depth + 1) } }
                in 0x80..0x8f -> {
                    val n = type and 0x0f
                    advance()
                    repeat(n * 2) { skipAt(depth + 1) }
                }
                0xde -> { advance(); repeat(u16() * 2) { skipAt(depth + 1) } }
                0xdf -> { advance(); repeat(u32() * 2) { skipAt(depth + 1) } }
                else -> throw Malformed("cannot skip format 0x${type.toString(16)}")
            }
        }

        private fun stringBytes(): ByteArray = when (val type = peek()) {
            in 0xa0..0xbf -> { advance(); take(type and 0x1f) } // fixstr: length is the low five bits
            0xd9 -> { advance(); take(u8()) }
            0xda -> { advance(); take(u16()) }
            0xdb -> { advance(); take(u32()) }
            else -> throw Malformed("expected string, got format 0x${type.toString(16)}")
        }

        private fun peek(): Int = if (pos < buf.size) buf[pos].toInt() and 0xff else throw Malformed("unexpected end of frame")

        private fun advance() {
            pos++
        }

        private fun u8(): Int = peek().also { advance() }

        private fun u16(): Int = (u8() shl 8) or u8()

        private fun u32(): Int = (u8() shl 24) or (u8() shl 16) or (u8() shl 8) or u8()

        private fun take(n: Int): ByteArray {
            if (n < 0 || pos + n > buf.size) throw Malformed("value runs past the frame")
            val out = buf.copyOfRange(pos, pos + n)
            pos += n
            return out
        }
    }
}
