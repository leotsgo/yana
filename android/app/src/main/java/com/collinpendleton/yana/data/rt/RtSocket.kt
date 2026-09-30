package com.collinpendleton.yana.data.rt

import okhttp3.HttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString

/**
 * One WebSocket connection to the relay, behind an interface so the
 * JVM test suite can fake a server end to end. Implementations deliver
 * frames in order on whatever thread they own; the engine funnels them
 * into its own scope.
 */
interface RtTransport {
    fun connect(url: String, listener: Listener): Connection

    interface Connection {
        /** Queues one frame; false when the connection is already gone. */
        fun send(bytes: ByteArray): Boolean

        /** Begins the close handshake; in-flight frames still land. */
        fun close(code: Int, reason: String)

        /** Drops the connection without a handshake, for engine shutdown. */
        fun cancel()
    }

    interface Listener {
        fun onOpen()

        fun onFrame(bytes: ByteArray)

        /** The server closed, or the close handshake finished. */
        fun onClosed(code: Int, reason: String)

        /** The dial or the stream failed; [httpCode] is set when the handshake was refused. */
        fun onFailure(t: Throwable?, httpCode: Int?)
    }
}

/** The transport over the app's one OkHttp stack. */
class OkHttpRtTransport(private val http: OkHttpClient) : RtTransport {
    override fun connect(url: String, listener: RtTransport.Listener): RtTransport.Connection {
        val socket = HttpSocket(listener)
        val ws = http.newWebSocket(
            Request.Builder().url(url).build(),
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) = listener.onOpen()

                override fun onMessage(webSocket: WebSocket, bytes: ByteString) = listener.onFrame(bytes.toByteArray())

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(code, reason)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = listener.onClosed(code, reason)

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) =
                    listener.onFailure(t, response?.code)
            },
        )
        socket.attach(ws)
        return socket
    }

    private class HttpSocket(private val listener: RtTransport.Listener) : RtTransport.Connection {
        @Volatile
        private var ws: WebSocket? = null

        fun attach(ws: WebSocket) {
            this.ws = ws
        }

        override fun send(bytes: ByteArray): Boolean = ws?.send(bytes.toByteString()) ?: false

        override fun close(code: Int, reason: String) {
            ws?.close(code, reason)
        }

        override fun cancel() {
            ws?.cancel()
        }
    }
}

/**
 * The dial URL: the session's server with the WebSocket scheme, the
 * `/ws` endpoint under any path prefix, and the access token the
 * handshake authenticates with (docs/realtime.md). Built as a string
 * because OkHttp's HttpUrl refuses a ws scheme; its Request builder
 * accepts one and swaps it for http(s) itself.
 */
fun wsUrl(base: HttpUrl, token: String?): String {
    val sb = StringBuilder()
    sb.append(if (base.scheme == "http") "ws://" else "wss://")
    sb.append(base.host)
    val defaultPort = if (base.isHttps) 443 else 80
    if (base.port != defaultPort) sb.append(':').append(base.port)
    sb.append(base.encodedPath.trimEnd('/')).append("/ws")
    if (!token.isNullOrEmpty()) sb.append("?token=").append(token.encodeURLQuery())
    return sb.toString()
}

private fun String.encodeURLQuery(): String = java.net.URLEncoder.encode(this, "UTF-8")
