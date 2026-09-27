package com.collinpendleton.yana.data

import android.content.ContentResolver
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Matrix
import android.net.Uri
import android.provider.OpenableColumns
import com.collinpendleton.yana.data.replica.UploadOp
import com.collinpendleton.yana.data.rt.SyncEngine
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.InputStream
import java.net.URLDecoder
import java.time.Instant
import java.time.ZoneOffset
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

/**
 * Images in notes, the phone's half of the web editor's upload flow:
 * pick or take a photo, send it to the note's sibling `_assets/`
 * directory through the same `PUT /api/files/<path>` the web editor
 * uses, and write `![alt](_assets/name)` into the document. The naming
 * is the web client's (web/src/upload.ts) ported exactly — the same
 * sanitization, the same stamped fallback — so both clients produce the
 * same tree.
 */
object AssetNames {
    /** Photos longer than this on their longest edge are downscaled to it before upload. */
    const val MAX_EDGE = 2048

    /** The quality of a downscaled photo's re-encode. */
    const val JPEG_QUALITY = 90

    /** A name the server accepts: the web client's safeName, ported. */
    fun safeName(name: String, type: String, nowMs: Long = System.currentTimeMillis()): String {
        var n = name.trim()
            .replace(Regex("""[\\/:*?"<>| ]"""), "-")
            .replace(Regex("""\s+"""), "-")
            .replace(Regex("-+"), "-")
        n = n.replaceFirst(Regex("^[.-]+"), "")
        if (n.isEmpty() || n == "." || n == "..") {
            n = "photo-${stamp(nowMs)}${extFor(type)}"
        }
        return n
    }

    /** The extension a MIME type implies; empty when it implies none. */
    fun extFor(type: String): String = when (type) {
        "image/png" -> ".png"
        "image/jpeg" -> ".jpg"
        "image/gif" -> ".gif"
        "image/webp" -> ".webp"
        "image/svg+xml" -> ".svg"
        "image/heic", "image/heif" -> ".heic"
        else -> ""
    }

    /** The caption of a file name, the web client's altFor. */
    fun altFor(name: String): String = name.replace(Regex("""\.[^.]+$"""), "").replace(Regex("[-_]+"), " ")

    /** The web client's paste stamp (UTC, `YYYYMMDD-HHMMSS`), the fallback name's spine. */
    fun stamp(nowMs: Long = System.currentTimeMillis()): String {
        val t = Instant.ofEpochMilli(nowMs).atZone(ZoneOffset.UTC)
        return "%04d%02d%02d-%02d%02d%02d".format(t.year, t.monthValue, t.dayOfMonth, t.hour, t.minute, t.second)
    }

    /** True of the placeholder name pasted screenshots arrive with, which get a stamp instead. */
    fun isGenericName(name: String): Boolean =
        Regex("""^image\.(png|jpe?g|gif|webp)$""", RegexOption.IGNORE_CASE).containsMatchIn(name)

    /** The link a picture becomes in the note. */
    fun imageLink(name: String): String = "![${altFor(name)}](_assets/$name)"

    /** The in-flight marker, replaced by the link once the server answers. */
    fun marker(name: String): String = "![uploading $name](...)"

    /** Swaps [marker] for [replacement], wherever it sits now; the cursor rides through. */
    fun replaceMarker(text: String, marker: String, replacement: String, cursor: Int): Pair<String, Int> {
        val idx = text.indexOf(marker)
        return if (idx < 0) {
            // Gone (edited away while the upload ran): the web's rule — a
            // replacement lands at the cursor, nothing else moves.
            if (replacement.isEmpty()) return text to cursor
            val at = cursor.coerceIn(0, text.length)
            (text.substring(0, at) + replacement + text.substring(at)) to at + replacement.length
        } else {
            val next = text.substring(0, idx) + replacement + text.substring(idx + marker.length)
            val delta = replacement.length - marker.length
            val c = if (cursor <= idx) cursor else (cursor + delta).coerceAtLeast(idx + replacement.length)
            next to c.coerceIn(0, next.length)
        }
    }
}

/** Where image bytes come from: the picker or the camera. */
sealed interface ImageSource {
    class Picked(val uri: Uri) : ImageSource

    /** A file this app wrote for the camera, already named `photo-<stamp>.jpg`. */
    class Capture(val file: File) : ImageSource
}

/**
 * The staged bytes of queued uploads. The pending_ops payloads are
 * JSON, so an offline upload's bytes live beside the database in a file
 * named here, and leave when the op replays (or is refused).
 */
class PendingUploadStore(private val dir: File) {
    /** Writes the bytes and answers the staged file's name. */
    fun stage(bytes: ByteArray, ext: String): String {
        dir.mkdirs()
        val name = "${com.collinpendleton.yana.data.CaptureKit.newUlid()}$ext"
        File(dir, name).writeBytes(bytes)
        return name
    }

    /** The staged bytes, or null when the file is gone. */
    fun read(name: String): ByteArray? = file(name)?.takeIf { it.isFile }?.readBytes()

    /** Removes one staged file; its op has settled. */
    fun delete(name: String) {
        file(name)?.delete()
    }

    /** Drops staged files no queued op names anymore (a wiped queue, a crash between writes). */
    fun sweep(referenced: Set<String>) {
        val files = dir.listFiles() ?: return
        for (f in files) {
            if (f.name !in referenced) f.delete()
        }
    }

    /** Resolves inside the staging directory or not at all. */
    private fun file(name: String): File? {
        if (name.isEmpty() || name.contains('/') || name.contains('\\') || name.contains("..")) return null
        val f = File(dir, name)
        return if (f.canonicalFile?.parentFile == dir.canonicalFile) f else null
    }
}

/** One image ready to go up: its (possibly downscaled) bytes and the name it asks for. */
data class PreparedAsset(val bytes: ByteArray, val name: String, val mime: String)

/** What became of one shared photo on its way into the note. */
sealed interface Delivery {
    /** It is on the server, under [name] — the name the link carries. */
    data class Uploaded(val name: String) : Delivery

    /** The network is gone; the link uses the asked-for name and the upload queues. */
    data class Offline(val item: PreparedAsset) : Delivery

    /** The server refused it (over the asset limit); it is not in the note. */
    data class Refused(val name: String, val reason: String) : Delivery
}

/**
 * Reads images (downscaling what is over the maximum edge), uploads
 * them to `_assets/`, and queues what the network would not take. The
 * byte-level work is Android's; the naming and link logic above it is
 * plain and pinned by tests.
 */
class ImageAssets(
    private val resolver: ContentResolver,
    val staged: PendingUploadStore,
) {
    /**
     * Reads one image for [source]. What fits inside the maximum edge
     * goes up untouched; what does not is downscaled to it and
     * re-encoded as JPEG (its name's extension follows), rotated the
     * way its EXIF says, so a portrait photo stays portrait. Something
     * that will not decode (a format this device cannot read) is sent
     * as it is, for the server and other clients to make of it what
     * they can.
     */
    suspend fun prepare(source: ImageSource): PreparedAsset? = withContext(Dispatchers.IO) {
        when (source) {
            is ImageSource.Picked -> {
                val display = displayName(source.uri)
                val mime = resolver.getType(source.uri).orEmpty()
                prepareCore(display, mime, open = {
                    runCatching { resolver.openInputStream(source.uri) }.getOrNull()
                }, rotation = {
                    runCatching {
                        resolver.openFileDescriptor(source.uri, "r")?.use { fd ->
                            rotationOf(android.media.ExifInterface(fd.fileDescriptor))
                        }
                    }.getOrNull() ?: 0
                })
            }
            is ImageSource.Capture -> prepareCore(
                source.file.name,
                "",
                open = { runCatching { source.file.inputStream() }.getOrNull() },
                rotation = {
                    runCatching { rotationOf(android.media.ExifInterface(source.file.absolutePath)) }.getOrNull() ?: 0
                },
            )
        }
    }

    /** Prepares each source, dropping what cannot be read. */
    suspend fun prepareAll(sources: List<ImageSource>): List<PreparedAsset> =
        sources.mapNotNull { runCatching { prepare(it) }.getOrNull() }

    /**
     * Sends each prepared asset up now: uploaded, refused, or offline
     * (the caller writes the asked-for name's link and queues the
     * bytes). Once one lands offline the rest are offline too — the
     * network is gone, and there is nothing to learn by trying again.
     */
    suspend fun deliver(
        items: List<PreparedAsset>,
        base: String,
        repo: NoteRepository,
    ): List<Delivery> {
        val out = mutableListOf<Delivery>()
        var offline = false
        for (item in items) {
            if (offline) {
                out += Delivery.Offline(item)
                continue
            }
            out += try {
                val res = repo.uploadAsset("$base/_assets/${item.name}", item.bytes, item.mime)
                Delivery.Uploaded(res.name.ifEmpty { item.name })
            } catch (e: CancellationException) {
                throw e
            } catch (e: java.io.IOException) {
                offline = true
                Delivery.Offline(item)
            } catch (e: retrofit2.HttpException) {
                Delivery.Refused(item.name, e.userMessage())
            }
        }
        return out
    }

    /** Stages and queues one offline upload against the note it belongs to. */
    suspend fun queueOffline(item: PreparedAsset, base: String, noteId: String, repo: NoteRepository) {
        val ext = item.name.substringAfterLast('.', "").let { if (it.isEmpty()) "" else ".$it" }
        val file = staged.stage(item.bytes, ext)
        repo.enqueueUpload(UploadOp(path = "$base/_assets/${item.name}", name = item.name, noteId = noteId, file = file))
    }

    private fun prepareCore(
        display: String?,
        mime: String,
        open: () -> InputStream?,
        rotation: () -> Int,
    ): PreparedAsset? {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        open()?.use { BitmapFactory.decodeStream(it, null, bounds) } ?: return null
        val type = bounds.outMimeType ?: mime
        val asked = AssetNames.safeName(
            if (display != null && !AssetNames.isGenericName(display)) display else "",
            type,
        )
        val w = bounds.outWidth
        val h = bounds.outHeight
        val maxEdge = maxOf(w, h)
        if (w <= 0 || h <= 0 || maxEdge <= AssetNames.MAX_EDGE) {
            // Undecodable or already inside the limit: the bytes as they are.
            val bytes = open()?.use { it.readBytes() } ?: return null
            if (bytes.isEmpty()) return null
            return PreparedAsset(bytes, asked, type.ifEmpty { "application/octet-stream" })
        }
        var sample = 1
        while (maxOf(w, h) / (sample * 2) >= AssetNames.MAX_EDGE) sample *= 2
        val opts = BitmapFactory.Options().apply { inSampleSize = sample }
        val decoded = open()?.use { BitmapFactory.decodeStream(it, null, opts) } ?: return null
        val degrees = rotation()
        val upright = if (degrees != 0) {
            val m = Matrix().apply { postRotate(degrees.toFloat()) }
            Bitmap.createBitmap(decoded, 0, 0, decoded.width, decoded.height, m, true).also {
                if (it != decoded) decoded.recycle()
            }
        } else {
            decoded
        }
        val scale = AssetNames.MAX_EDGE.toFloat() / maxOf(upright.width, upright.height)
        val scaled = if (scale < 1f) {
            Bitmap.createScaledBitmap(
                upright,
                (upright.width * scale).toInt().coerceAtLeast(1),
                (upright.height * scale).toInt().coerceAtLeast(1),
                true,
            ).also { if (it != upright) upright.recycle() }
        } else {
            upright
        }
        val out = ByteArrayOutputStream()
        scaled.compress(Bitmap.CompressFormat.JPEG, AssetNames.JPEG_QUALITY, out)
        scaled.recycle()
        val name = asked.replaceFirst(Regex("""\.[^.]+$"""), "").ifEmpty { "photo-${AssetNames.stamp()}" } + ".jpg"
        return PreparedAsset(out.toByteArray(), name, "image/jpeg")
    }

    private fun displayName(uri: Uri): String? = runCatching {
        resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) c.getString(0) else null
        }
    }.getOrNull()

    /** The EXIF orientation as a clockwise rotation, the way browsers honor it for the original. */
    private fun rotationOf(e: android.media.ExifInterface): Int = when (
        e.getAttributeInt(
            android.media.ExifInterface.TAG_ORIENTATION,
            android.media.ExifInterface.ORIENTATION_NORMAL,
        )
    ) {
        android.media.ExifInterface.ORIENTATION_ROTATE_90,
        android.media.ExifInterface.ORIENTATION_TRANSPOSE,
        -> 90
        android.media.ExifInterface.ORIENTATION_ROTATE_180 -> 180
        android.media.ExifInterface.ORIENTATION_ROTATE_270,
        android.media.ExifInterface.ORIENTATION_TRANSVERSE,
        -> 270
        else -> 0
    }
}

/**
 * Rewrites a note's link when a queued upload lands under the name the
 * server picked instead of the one asked for — the same open, edit,
 * close the append path uses, so the fix merges with open editors.
 */
suspend fun fixUploadLink(engine: SyncEngine, noteId: String, from: String, to: String) {
    if (from == to || from.isEmpty() || to.isEmpty()) return
    val handle = engine.open(noteId)
    try {
        val ready = withTimeoutOrNull(8_000) { handle.ready.first { it } }
        if (ready != true) return
        val text = handle.text.value
        val want = text.replace("(_assets/$from)", "(_assets/$to)")
        if (want != text) engine.edit(noteId, want)
    } finally {
        engine.close(noteId)
    }
}

/** One tree path percent-encoded per segment, the form `/api/files/` takes. */
fun encodeAssetPath(path: String): String = path.split('/').joinToString("/") { seg -> percent(seg) }

/** The inverse of [encodeAssetPath], for comparing what the reader asks for. */
fun decodeAssetPath(encoded: String): String =
    encoded.split('/').joinToString("/") { URLDecoder.decode(it, "UTF-8") }

/** encodeURIComponent's unreserved set, UTF-8 bytes, %XX for the rest. */
private fun percent(s: String): String = buildString {
    for (b in s.toByteArray(Charsets.UTF_8)) {
        val c = b.toInt().toChar()
        if (c in 'A'..'Z' || c in 'a'..'z' || c in '0'..'9' || c == '-' || c == '.' || c == '_' || c == '~') {
            append(c)
        } else {
            append('%')
            append(HEX[(b.toInt() shr 4) and 0xF])
            append(HEX[b.toInt() and 0xF])
        }
    }
}

private val HEX = "0123456789ABCDEF".toCharArray()

/** The `_assets` base of a note's tree path: its directory. */
fun assetBaseOf(notePath: String): String = notePath.substringBeforeLast('/', "")
