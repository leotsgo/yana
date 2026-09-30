package com.collinpendleton.yana.data

import com.collinpendleton.yana.crdt.crdt.Crdt

/**
 * The render half of the CRDT bind package: the same goldmark engine the
 * server renders with, running on the device, so the phone's reader and
 * the web's reading mode agree by construction (mobile/crdt/render.go).
 */
object GoRender {
    /** One note body as HTML. Throws only on a broken document, which goldmark does not produce. */
    fun markdown(body: String): String = Crdt.renderMarkdown(body)

    /** The distinct raw [[targets]] in a body, the extraction the render itself performed. */
    fun wikiLinks(body: String): List<String> =
        Crdt.wikiLinks(body).split('\n').filter { it.isNotEmpty() }
}
