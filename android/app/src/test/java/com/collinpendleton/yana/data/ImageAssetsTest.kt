package com.collinpendleton.yana.data

import com.collinpendleton.yana.data.replica.CreateOp
import com.collinpendleton.yana.data.replica.OpPayload
import com.collinpendleton.yana.data.replica.UploadOp
import java.nio.file.Files
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The pure half of image uploads, held to the web client's semantics. */
class ImageAssetsTest {
    // --- names -------------------------------------------------------------------

    @Test
    fun `a name is sanitized the way the web client sanitizes it`() {
        assertEquals("My-photo.png", AssetNames.safeName("My photo.png", "image/png"))
        assertEquals("a-b-c-d-e-f-g-h-i-j.png", AssetNames.safeName("a b\\c:d*e?f\"g<h>i|j.png", "image/png"))
        assertEquals("shot.png", AssetNames.safeName("....shot.png", "image/png"))
        assertEquals("shot.png", AssetNames.safeName("--shot.png", "image/png"))
        assertEquals("photo-1.jpg", AssetNames.safeName("photo 1.jpg", "image/jpeg"))
    }

    @Test
    fun `a name without a body gets a stamp and the type's extension`() {
        val name = AssetNames.safeName("", "image/jpeg", nowMs = 1_758_861_020_000L)
        assertTrue(name.matches(Regex("""photo-\d{8}-\d{6}\.jpg""")))
        assertEquals("photo-20250926-043020.jpg", name)
    }

    @Test
    fun `a name that sanitizes to a dot gets the same fallback`() {
        val name = AssetNames.safeName("..", "image/png", nowMs = 1_758_861_020_000L)
        assertEquals("photo-20250926-043020.png", name)
    }

    @Test
    fun `the stamp is the web client's, UTC`() {
        // 2026-09-26T10:15:30Z -> 20260926-101530
        assertEquals("20260926-101530", AssetNames.stamp(1_790_417_730_000L))
    }

    @Test
    fun `extensions follow the type`() {
        assertEquals(".png", AssetNames.extFor("image/png"))
        assertEquals(".jpg", AssetNames.extFor("image/jpeg"))
        assertEquals(".gif", AssetNames.extFor("image/gif"))
        assertEquals(".webp", AssetNames.extFor("image/webp"))
        assertEquals(".svg", AssetNames.extFor("image/svg+xml"))
        assertEquals("", AssetNames.extFor("application/octet-stream"))
    }

    @Test
    fun `the caption strips the extension and softens the separators`() {
        assertEquals("My shot of the kettle", AssetNames.altFor("My_shot-of-the_kettle.jpg"))
    }

    @Test
    fun `the placeholder name pasted screenshots arrive with is detected`() {
        assertTrue(AssetNames.isGenericName("image.png"))
        assertTrue(AssetNames.isGenericName("image.JPG"))
        assertFalse(AssetNames.isGenericName("IMG_1234.JPG"))
        assertFalse(AssetNames.isGenericName(""))
    }

    @Test
    fun `the link and marker are the web client's shapes`() {
        assertEquals("![my shot](_assets/my-shot.png)", AssetNames.imageLink("my-shot.png"))
        assertEquals("![uploading my-shot.png](...)", AssetNames.marker("my-shot.png"))
    }

    @Test
    fun `a file that is not a picture links as the plain link the web writes`() {
        assertEquals("[budget.xlsx](_assets/budget.xlsx)", AssetNames.fileLink("budget.xlsx"))
        assertEquals("[uploading budget.xlsx](...)", AssetNames.fileMarker("budget.xlsx"))
    }

    @Test
    fun `linkFor picks the picture link only for image types`() {
        assertEquals("![shot](_assets/shot.png)", AssetNames.linkFor("shot.png", image = true))
        assertEquals("[report.pdf](_assets/report.pdf)", AssetNames.linkFor("report.pdf", image = false))
    }

    // --- the marker's replacement ------------------------------------------------

    @Test
    fun `a marker is replaced wherever it sits`() {
        val (text, cursor) = AssetNames.replaceMarker(
            "before ![uploading x.png](...) after",
            "![uploading x.png](...)",
            "![x](_assets/x.png)",
            0,
        )
        assertEquals("before ![x](_assets/x.png) after", text)
        assertEquals(0, cursor)
    }

    @Test
    fun `a cursor past the marker shifts with the replacement`() {
        val (text, cursor) = AssetNames.replaceMarker(
            "a ![uploading x.png](...) tail",
            "![uploading x.png](...)",
            "![x](_assets/x.png)",
            30,
        )
        assertEquals("a ![x](_assets/x.png) tail", text)
        assertEquals(26, cursor)
    }

    @Test
    fun `a marker edited away leaves the replacement at the cursor`() {
        val (text, cursor) = AssetNames.replaceMarker(
            "typing continues",
            "![uploading x.png](...)",
            "![x](_assets/x.png)",
            7,
        )
        assertEquals("typing ![x](_assets/x.png)continues", text)
        assertEquals(7 + "![x](_assets/x.png)".length, cursor)
    }

    @Test
    fun `a marker edited away and a failed upload leaves nothing`() {
        val (text, cursor) = AssetNames.replaceMarker(
            "typing continues",
            "![uploading x.png](...)",
            "",
            7,
        )
        assertEquals("typing continues", text)
        assertEquals(7, cursor)
    }

    // --- the queue's payload --------------------------------------------------------

    @Test
    fun `an upload op rides the polymorphic payload both ways`() {
        val op = UploadOp(path = "home/sub/_assets/x.png", name = "x.png", noteId = "01ABC", file = "01ULID.png")
        val encoded = YanaJson.encodeToString(OpPayload.serializer(), OpPayload.Upload(op))
        val decoded = YanaJson.decodeFromString(OpPayload.serializer(), encoded)
        assertEquals(OpPayload.Upload(op), decoded)
    }

    @Test
    fun `payloads written before uploads still decode`() {
        val legacy =
            """{"type":"com.collinpendleton.yana.data.replica.OpPayload.Create","op":{"space":"home","path":"home/n.md","content":"","id":"01X"}}"""
        val decoded = YanaJson.decodeFromString(OpPayload.serializer(), legacy)
        assertTrue(decoded is OpPayload.Create)
        assertEquals(CreateOp("home", "home/n.md", "", "01X"), (decoded as OpPayload.Create).op)
    }

    // --- staged bytes ---------------------------------------------------------------

    @Test
    fun `staged bytes round-trip and leave when deleted`() {
        val dir = Files.createTempDirectory("pending-uploads").toFile()
        val store = PendingUploadStore(dir)
        val name = store.stage(byteArrayOf(1, 2, 3), ".jpg")
        assertTrue(name.endsWith(".jpg") && !name.contains('/'))
        assertArrayEquals(byteArrayOf(1, 2, 3), store.read(name))
        store.delete(name)
        assertNull(store.read(name))
    }

    @Test
    fun `a sweep drops only what no op names`() {
        val dir = Files.createTempDirectory("pending-uploads").toFile()
        val store = PendingUploadStore(dir)
        val keep = store.stage(byteArrayOf(1), ".png")
        val drop = store.stage(byteArrayOf(2), ".png")
        store.sweep(setOf(keep))
        assertArrayEquals(byteArrayOf(1), store.read(keep))
        assertNull(store.read(drop))
    }

    @Test
    fun `a staged name cannot escape the directory`() {
        val dir = Files.createTempDirectory("pending-uploads").toFile()
        val store = PendingUploadStore(dir)
        assertNull(store.read("../evil.png"))
        assertNull(store.read("sub/evil.png"))
        assertNull(store.read(""))
        store.delete("../evil.png") // no throw
    }

    @Test
    fun `two staged files differ`() {
        val dir = Files.createTempDirectory("pending-uploads").toFile()
        val store = PendingUploadStore(dir)
        assertNotEquals(store.stage(byteArrayOf(1), ".png"), store.stage(byteArrayOf(1), ".png"))
    }

    // --- paths ----------------------------------------------------------------------

    @Test
    fun `asset paths encode per segment and decode back`() {
        val path = "home/my notes/sub/_assets/café shot.png"
        assertEquals(path, decodeAssetPath(encodeAssetPath(path)))
        assertTrue(encodeAssetPath(path).startsWith("home/my%20notes/sub/_assets/"))
        assertFalse(encodeAssetPath(path).contains(" "))
    }

    @Test
    fun `a note's asset base is its directory`() {
        assertEquals("home/sub", assetBaseOf("home/sub/note.md"))
        assertEquals("", assetBaseOf("note.md"))
    }

    private fun assertArrayEquals(want: ByteArray, got: ByteArray?) {
        assertTrue(got != null && want.contentEquals(got))
    }
}
