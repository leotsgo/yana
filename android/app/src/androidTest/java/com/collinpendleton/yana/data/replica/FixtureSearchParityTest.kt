package com.collinpendleton.yana.data.replica

import androidx.room.Room
import androidx.sqlite.driver.bundled.BundledSQLiteDriver
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.collinpendleton.yana.data.NoteMeta
import com.collinpendleton.yana.data.Space
import com.collinpendleton.yana.data.search.parseQuery
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The offline/online parity acceptance (MAD-539): the same corpus of 200
 * notes, the same queries, the same ordered result set. The expectations
 * in the fixture were produced by the server's own engine (run
 * `go test ./internal/index -run TestAndroidReplicaFixtures -update` in
 * the repository root to regenerate them together with the corpus), so
 * agreement here means the replica answers exactly as the server would.
 */
@RunWith(AndroidJUnit4::class)
class FixtureSearchParityTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Serializable
    private data class FxSpace(val name: String, val label: String = "", val notes: Int = 0)

    @Serializable
    private data class FxNote(
        val id: String,
        val space: String = "",
        val path: String,
        val title: String = "",
        val preview: String = "",
        val kind: String = "md",
        val created: String = "",
        @SerialName("updated_at") val updatedAt: String = "",
        val tags: List<String> = emptyList(),
        val source: String = "",
    ) {
        fun meta() = NoteMeta(
            id = id, space = space, path = path, title = title, preview = preview,
            kind = kind, created = created, updatedAt = updatedAt, tags = tags,
        )
    }

    @Serializable
    private data class FxQuery(val q: String, val space: String = "")

    @Serializable
    private data class FxExpectation(val ids: List<String> = emptyList(), val ranks: List<Double> = emptyList())

    @Serializable
    private data class FxFile(
        val spaces: List<FxSpace> = emptyList(),
        val notes: List<FxNote> = emptyList(),
        val queries: List<FxQuery> = emptyList(),
        val expected: List<FxExpectation> = emptyList(),
    )

    private fun fixture(): FxFile =
        InstrumentationRegistry.getInstrumentation().targetContext.assets.open("searchfixtures/fixture.json").use {
            json.decodeFromString(FxFile.serializer(), it.readBytes().decodeToString())
        }

    private fun store(): ReplicaStore {
        val context = InstrumentationRegistry.getInstrumentation().targetContext
        val db = Room.inMemoryDatabaseBuilder(context, ReplicaDatabase::class.java)
            .setDriver(BundledSQLiteDriver())
            .addCallback(object : androidx.room.RoomDatabase.Callback() {
                override fun onCreate(db: androidx.sqlite.db.SupportSQLiteDatabase) {
                    for (ddl in ReplicaDatabase.FTS_DDL) db.execSQL(ddl)
                }
            })
            .build()
        return ReplicaStore(db)
    }

    @Test
    fun sameQueriesSameOrderedResults() = runBlocking {
        val fx = fixture()
        assertEquals("the corpus is 200 notes", 200, fx.notes.size)
        val store = store()

        // The replica sync: metadata for every note, the body cached for
        // each one the user opened — here, all of them.
        store.applySync(
            fx.spaces.map { Space(it.name, it.label, it.notes) },
            fx.notes.map { it.meta() },
            emptyList(),
        )
        for (n in fx.notes) {
            store.storeBody(n.id, n.kind, n.source)
        }

        val knownSpaces = fx.spaces.map { it.name }.toSet() + setOf("")
        for ((i, q) in fx.queries.withIndex()) {
            val want = fx.expected[i]
            val hits = store.search(parseQuery(q.q), q.space.ifEmpty { null })
            assertEquals("query ${q.q} (space=${q.space}): hit count", want.ids.size, hits.size)
            for ((j, h) in hits.withIndex()) {
                assertEquals("query ${q.q}: order at $j", want.ids[j], h.id)
                assertTrue("rank ${h.rank} vs ${want.ranks[j]}", Math.abs(h.rank - want.ranks[j]) < 1e-9)
                // The replica only ever holds notes the server returned
                // for this account, so no foreign space can appear.
                assertTrue("hit from unknown space ${h.space}", knownSpaces.contains(h.space))
            }
        }
    }

    @Test
    fun unopenedNotesStaySearchableByTitle() = runBlocking {
        val fx = fixture()
        val store = store()
        store.applySync(fx.spaces.map { Space(it.name, it.label, it.notes) }, fx.notes.map { it.meta() }, emptyList())
        // No body was ever stored: the title half of the index still works.
        val hits = store.search(parseQuery("note"), null)
        assertEquals(50, hits.size)
        assertTrue(hits.all { it.title.isNotBlank() })
    }
}
