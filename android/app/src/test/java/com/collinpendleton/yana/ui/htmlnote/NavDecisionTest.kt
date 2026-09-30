package com.collinpendleton.yana.ui.htmlnote

import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertEquals
import org.junit.Test

/** Navigation decisions for the note WebView against a content origin like a home server's :8081. */
class NavDecisionTest {
    private val origin = "http://192.168.1.4:8081".toHttpUrl()

    @Test fun sameOriginLoads() {
        assertEquals(NavDecision.Load, navDecision("http://192.168.1.4:8081/n/01A?token=t", origin))
        assertEquals(NavDecision.Load, navDecision("http://192.168.1.4:8081/t/tok/f/work/img.png", origin))
    }

    @Test fun defaultPortsMatchTheirScheme() {
        val api = "https://notes.example.com".toHttpUrl()
        assertEquals(NavDecision.Load, navDecision("https://notes.example.com:443/n/01A", api))
        assertEquals(NavDecision.External, navDecision("http://notes.example.com/n/01A", api))
    }

    @Test fun otherHostsGoToTheBrowser() {
        assertEquals(NavDecision.External, navDecision("https://example.com/", origin))
        assertEquals(NavDecision.External, navDecision("http://192.168.1.4:8080/api/notes", origin))
        assertEquals(NavDecision.External, navDecision("http://192.168.1.5:8081/n/01A", origin))
    }

    @Test fun nonHttpSchemesAreBlocked() {
        assertEquals(NavDecision.Blocked, navDecision("file:///data/local/tmp/secret.html", origin))
        assertEquals(NavDecision.Blocked, navDecision("content://media/external/file/1", origin))
        assertEquals(NavDecision.Blocked, navDecision("intent://example.com/#Intent;end", origin))
        assertEquals(NavDecision.Blocked, navDecision("javascript:fetch('/api/notes')", origin))
        assertEquals(NavDecision.Blocked, navDecision("data:text/html,<script>1</script>", origin))
    }

    @Test fun unparseableOrMissingUrlsAreBlocked() {
        assertEquals(NavDecision.Blocked, navDecision(null, origin))
        assertEquals(NavDecision.Blocked, navDecision("", origin))
        assertEquals(NavDecision.Blocked, navDecision("not a url", origin))
        assertEquals(NavDecision.Blocked, navDecision("http://192.168.1.4:8081/n/01A", null))
    }
}
