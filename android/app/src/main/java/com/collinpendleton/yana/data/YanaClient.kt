package com.collinpendleton.yana.data

import android.os.Build
import java.io.IOException
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.withContext
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import retrofit2.HttpException
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * The app's one way to the server: sign-in, sign-out, and the reads the
 * navigation shell needs. Retrofit clients are built per server address
 * and reused until the address changes.
 */
class YanaClient(
    private val store: SessionStore,
    private val plain: OkHttpClient = TokenAuth.plainClient(),
    private val deviceLabel: String = defaultDeviceLabel(),
    now: () -> Long = System::currentTimeMillis,
) {
    val auth = TokenAuth(store, plain, now)
    private val authed: OkHttpClient = plain.newBuilder()
        .addInterceptor(auth.interceptor)
        .authenticator(auth.authenticator)
        .build()

    val session: StateFlow<Session?> get() = store.session
    var lastServer: String?
        get() = store.lastServer
        set(value) {
            store.lastServer = value
        }

    /** The plain client the realtime socket dials with; its auth rides the query string, not a header. */
    fun socketClient(): OkHttpClient = plain

    private var authApiFor: Pair<HttpUrl, AuthApi>? = null
    private var apiFor: Pair<String, YanaApi>? = null

    @Synchronized
    fun authApi(base: HttpUrl): AuthApi = authApiFor?.takeIf { it.first == base }?.second
        ?: retrofit(base, plain).create(AuthApi::class.java).also { authApiFor = base to it }

    /** The authenticated API for the signed-in server. */
    @Synchronized
    fun api(): YanaApi {
        val server = store.session.value?.server ?: throw NotSignedIn()
        return apiFor?.takeIf { it.first == server }?.second
            ?: retrofit(normalizeServerUrl(server) ?: throw NotSignedIn(), authed)
                .create(YanaApi::class.java).also { apiFor = server to it }
    }

    suspend fun state(base: HttpUrl): AuthState = authApi(base).state()

    suspend fun setup(base: HttpUrl, username: String, password: String): Session =
        signedIn(base, authApi(base).setup(Credentials(username, password, deviceLabel)))

    suspend fun login(base: HttpUrl, username: String, password: String): Session =
        signedIn(base, authApi(base).login(Credentials(username, password, deviceLabel)))

    private fun signedIn(base: HttpUrl, r: SignInResponse): Session {
        val s = Session.of(base.toString(), r.user, r.tokens)
        auth.clearEndedReason()
        store.lastServer = base.display()
        store.save(s)
        return s
    }

    /** Revokes this device's session on the server, then forgets it here whatever the answer. */
    suspend fun signOut() {
        val s = store.session.value ?: return
        try {
            normalizeServerUrl(s.server)?.let { authApi(it).logout(RefreshRequest(s.refreshToken)) }
        } catch (e: CancellationException) {
            throw e
        } catch (_: Exception) {
            // Offline or already revoked: the local session goes regardless.
        }
        store.save(null)
    }

    /**
     * One space's notes as a zip (`GET /api/spaces/{space}/export/notes.zip`),
     * downloaded with this session's auth so it can be handed to the share
     * sheet. The root of the tree has no zip of its own; it exports on the web.
     */
    suspend fun exportNotesZip(space: String): ByteArray {
        if (space.isEmpty()) throw IOException("the root of the tree exports from the web")
        return download("api/spaces/$space/export/notes.zip").bytes
    }

    /**
     * One note as a single self-contained HTML file
     * (`GET /api/notes/{id}/export.html`), for the share sheet.
     */
    suspend fun exportNoteHtml(id: String): ByteArray =
        download("api/notes/$id/export.html").bytes

    /**
     * One attachment's bytes (`GET /api/files/{path}`, percent-encoded
     * per segment), with the content type the server served it under,
     * so a file another app should open carries the right MIME.
     */
    suspend fun downloadAsset(path: String): HttpDownload {
        val encoded = encodeAssetPath(path)
        return download("api/files/$encoded")
    }

    /** One authed GET, its bytes and content type together. */
    private suspend fun download(path: String): HttpDownload {
        val base = normalizeServerUrl(store.session.value?.server ?: throw NotSignedIn()) ?: throw NotSignedIn()
        val url = base.newBuilder().addEncodedPathSegments(path).build()
        val request = Request.Builder().url(url).build()
        return withContext(Dispatchers.IO) {
            authed.newCall(request).execute().use { resp ->
                if (!resp.isSuccessful) {
                    val error = runCatching {
                        YanaJson.decodeFromString(ApiErrorBody.serializer(), resp.body.string()).error
                    }.getOrNull()
                    throw IOException(error?.replaceFirstChar { it.uppercase() } ?: "The server answered HTTP ${resp.code}.")
                }
                HttpDownload(resp.body.bytes(), resp.header("Content-Type"))
            }
        }
    }

    class NotSignedIn : IOException("not signed in")
    companion object {
        private val JSON = "application/json".toMediaType()

        fun retrofit(base: HttpUrl, client: OkHttpClient): Retrofit = Retrofit.Builder()
            .baseUrl(base)
            .client(client)
            .addConverterFactory(YanaJson.asConverterFactory(JSON))
            .build()

        /** Names this device in the web's session list, e.g. "android · Pixel 8". */
        fun defaultDeviceLabel(): String {
            val model = Build.MODEL.orEmpty()
            val maker = Build.MANUFACTURER.orEmpty()
            val name = if (model.startsWith(maker, ignoreCase = true)) model else "$maker $model"
            return "android · ${name.trim().ifEmpty { "device" }}".take(80)
        }
    }
}

/** What an authed download brought back: the bytes and the content type. */
class HttpDownload(val bytes: ByteArray, val contentType: String?) {
    /** The MIME type without parameters, for intent types and file guessing. */
    val mime: String? get() = contentType?.substringBefore(';')?.trim()?.ifEmpty { null }
}

/** A message a person can act on, from whatever a call threw. */
fun Throwable.userMessage(): String = when (this) {
    is YanaClient.NotSignedIn -> "Sign in first."
    is HttpException -> errorBody() ?: when (code()) {
        401 -> "Sign in again."
        403 -> "You do not have access to this."
        404 -> "Not found."
        429 -> "Too many attempts. Wait a minute and try again."
        else -> "The server answered HTTP ${code()}."
    }
    is kotlinx.serialization.SerializationException -> "That address answered, but not like a YANA/ server."
    is java.net.UnknownHostException -> "Could not find that server. Check the address."
    is java.net.ConnectException -> "Could not connect to the server."
    is java.net.SocketTimeoutException -> "The server took too long to answer."
    is javax.net.ssl.SSLException -> "The secure connection failed: ${message ?: "TLS error"}."
    is IOException -> message ?: "The network request failed."
    else -> message ?: toString()
}

private fun HttpException.errorBody(): String? = runCatching {
    response()?.errorBody()?.string()?.let { YanaJson.decodeFromString(ApiErrorBody.serializer(), it).error }
}.getOrNull()?.replaceFirstChar { it.uppercase() }?.let { if (it.endsWith(".")) it else "$it." }
