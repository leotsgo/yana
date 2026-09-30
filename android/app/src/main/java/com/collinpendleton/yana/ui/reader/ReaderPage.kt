package com.collinpendleton.yana.ui.reader

import com.collinpendleton.yana.data.ResolvedLink
import com.collinpendleton.yana.data.YanaJson
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * The document the reader WebView shows: the asset template
 * (web/src/android-reader.html, built into assets/reader/) with the
 * theme, the reader's config — link resolution, the note's base and
 * space, read-only — and the first render inlined. Later renders arrive
 * through the page's own yanaReader.setBody.
 */
object ReaderPage {
    /** The reader's config, as the page's JSON script tag carries it. */
    @Serializable
    private data class Config(
        val dark: Boolean,
        val base: String,
        val space: String,
        val readOnly: Boolean,
        val links: List<Link>,
    )

    @Serializable
    private data class Link(
        val raw: String,
        val to: String,
        val ok: Boolean,
    )

    fun build(
        template: String,
        dark: Boolean,
        base: String,
        space: String,
        readOnly: Boolean,
        links: List<ResolvedLink>,
        bodyHtml: String,
    ): String {
        val config = YanaJson.encodeToString(
            Config.serializer(),
            Config(
                dark = dark,
                base = base,
                space = space,
                readOnly = readOnly,
                links = links.map { Link(raw = it.raw, to = it.toId ?: "", ok = it.resolved) },
            ),
        )
        return template
            .replace("__THEME__", if (dark) "dark" else "light")
            .replace("<!--YANA-CONFIG-->", config)
            .replace("<!--YANA-BODY-->", bodyHtml)
    }
}
