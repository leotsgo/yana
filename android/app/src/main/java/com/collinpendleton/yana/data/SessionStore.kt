package com.collinpendleton.yana.data

import android.content.Context
import android.content.SharedPreferences
import androidx.core.content.edit
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/** Where the token pair and the last server address live between launches. */
interface SessionStore {
    val session: StateFlow<Session?>
    /** The last server address entered; kept after sign-out. */
    var lastServer: String?
    fun save(session: Session?)
}

/** Holds everything in memory; the unit tests use it. */
class MemorySessionStore(initial: Session? = null) : SessionStore {
    private val state = MutableStateFlow(initial)
    override val session: StateFlow<Session?> = state.asStateFlow()
    override var lastServer: String? = initial?.server
    override fun save(session: Session?) {
        state.value = session
    }
}

/**
 * The device store: EncryptedSharedPreferences keyed by an AES-256 master
 * key in the Android Keystore, so the refresh token never sits on disk in
 * the clear.
 */
@Suppress("DEPRECATION") // security-crypto is deprecated upstream but still the supported Keystore-backed prefs.
class EncryptedSessionStore(context: Context) : SessionStore {
    private val prefs: SharedPreferences = open(context)
    private val state = MutableStateFlow(read())
    override val session: StateFlow<Session?> = state.asStateFlow()

    override var lastServer: String?
        get() = prefs.getString(KEY_SERVER, null)
        set(value) = prefs.edit { putString(KEY_SERVER, value) }

    @Synchronized
    override fun save(session: Session?) {
        prefs.edit(commit = true) {
            if (session == null) remove(KEY_SESSION)
            else putString(KEY_SESSION, YanaJson.encodeToString(Session.serializer(), session))
        }
        state.value = session
    }

    private fun read(): Session? = prefs.getString(KEY_SESSION, null)?.let {
        runCatching { YanaJson.decodeFromString(Session.serializer(), it) }.getOrNull()
    }

    private companion object {
        const val FILE = "yana_session"
        const val KEY_SESSION = "session"
        const val KEY_SERVER = "server"

        fun open(context: Context): SharedPreferences {
            val key = MasterKey.Builder(context).setKeyScheme(MasterKey.KeyScheme.AES256_GCM).build()
            return try {
                create(context, key)
            } catch (e: Exception) {
                // A restored backup or a reset Keystore leaves a file the
                // key cannot open; start clean rather than crash on launch.
                context.deleteSharedPreferences(FILE)
                create(context, key)
            }
        }

        fun create(context: Context, key: MasterKey) = EncryptedSharedPreferences.create(
            context,
            FILE,
            key,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }
}
