package com.collinpendleton.yana.data

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class ServerUrlTest {
    private fun norm(s: String) = normalizeServerUrl(s)?.toString()

    @Test fun bareHostGetsHttps() = assertEquals("https://notes.example.com/", norm("notes.example.com"))
    @Test fun keepsHttpAndPort() = assertEquals("http://192.168.1.20:8080/", norm(" http://192.168.1.20:8080 "))
    @Test fun keepsPathPrefixWithSlash() = assertEquals("https://example.com/yana/", norm("https://example.com/yana"))
    @Test fun dropsQueryAndFragment() = assertEquals("https://example.com/", norm("https://example.com/?a=1#x"))
    @Test fun emulatorHostAlias() = assertEquals("http://10.0.2.2:8080/", norm("http://10.0.2.2:8080/"))
    @Test fun rejectsOtherSchemes() = assertNull(norm("ftp://example.com"))
    @Test fun rejectsBlank() = assertNull(norm("   "))
    @Test fun rejectsSpaces() = assertNull(norm("not a host"))
    @Test fun displayDropsTrailingSlash() = assertEquals("https://notes.example.com", normalizeServerUrl("notes.example.com")!!.display())
}
