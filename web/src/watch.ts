// Space watching over the same relay WebSocket the editor uses (see
// docs/realtime.md): a `watch` frame per space, and a lean `chg` frame
// whenever a note in a watched space changes. Listing pages — the tasks
// page — use it to know when to refetch, without joining every note's
// room. The frames carry no payloads; the page asks the API for state.

import { encode, decode } from '@msgpack/msgpack'

import { wsURL } from './auth'

interface WatchFrame {
  t: 'watch'
  s?: string
  a?: string
}

interface RelayFrame {
  t: 'watchd' | 'chg' | 'err'
  n?: string
  s?: string
  c?: string
  r?: string
}

const BACKOFF_MIN_MS = 1000
const BACKOFF_MAX_MS = 30_000
const KEEPALIVE_MS = 25_000

/** Watches spaces for changes. One socket, one frame per space; the
 * callback fires (debounced by the caller) with the note that changed. */
export class SpaceWatch {
  private ws: WebSocket | null = null
  private spaces = new Set<string>()
  private attempts = 0
  private retryTimer: number | undefined
  private keepaliveTimer: number | undefined
  private lastSentAt = 0
  private destroyed = false

  constructor(
    private readonly author: string,
    private readonly onChange: (note: string) => void,
    private readonly onRefused?: (reason: string) => void,
  ) {}

  /** Adds spaces to the watch; safe to call again as filters change. */
  watch(spaces: string[]): void {
    const next = new Set(spaces.filter((s) => s !== ''))
    const added = [...next].filter((s) => !this.spaces.has(s))
    this.spaces = next
    for (const s of added) this.send({ t: 'watch', s })
  }

  private send(frame: WatchFrame): void {
    const ws = this.ws
    if (!frame.s || !ws || ws.readyState !== WebSocket.OPEN) return
    ws.send(encode(frame))
    this.lastSentAt = Date.now()
  }

  destroy(): void {
    this.destroyed = true
    if (this.retryTimer !== undefined) window.clearTimeout(this.retryTimer)
    this.stopKeepalive()
    const ws = this.ws
    this.ws = null
    if (ws) {
      ws.onclose = null
      ws.onerror = null
      ws.close(1000, 'done')
    }
  }

  private connect(): void {
    if (this.destroyed || this.spaces.size === 0) return
    let ws: WebSocket
    try {
      ws = new WebSocket(wsURL())
    } catch {
      this.scheduleReconnect()
      return
    }
    ws.binaryType = 'arraybuffer'
    this.ws = ws
    ws.onopen = () => {
      this.attempts = 0
      for (const s of this.spaces) this.send({ t: 'watch', s })
      this.startKeepalive()
    }
    ws.onmessage = (ev) => {
      if (!(ev.data instanceof ArrayBuffer)) return
      const frame = decode(new Uint8Array(ev.data)) as RelayFrame
      if (frame.t === 'chg' && frame.n) this.onChange(frame.n)
      else if (frame.t === 'err' && frame.c === 'forbidden') this.onRefused?.(frame.r ?? '')
    }
    ws.onclose = () => {
      this.stopKeepalive()
      this.ws = null
      this.scheduleReconnect()
    }
    ws.onerror = () => ws.close()
  }

  private scheduleReconnect(): void {
    if (this.destroyed || this.retryTimer !== undefined || this.spaces.size === 0) return
    const delay = Math.min(BACKOFF_MIN_MS * 2 ** this.attempts, BACKOFF_MAX_MS) + Math.random() * 250
    this.attempts++
    this.retryTimer = window.setTimeout(() => {
      this.retryTimer = undefined
      this.connect()
    }, delay)
  }

  private startKeepalive(): void {
    this.stopKeepalive()
    this.keepaliveTimer = window.setInterval(() => {
      if (Date.now() - this.lastSentAt >= KEEPALIVE_MS) {
        this.ws?.send(encode({ t: 'ping' }))
        this.lastSentAt = Date.now()
      }
    }, KEEPALIVE_MS)
  }

  private stopKeepalive(): void {
    if (this.keepaliveTimer !== undefined) {
      window.clearInterval(this.keepaliveTimer)
      this.keepaliveTimer = undefined
    }
  }

  /** Starts the socket once spaces are known; later watch() calls ride
   * the same connection when it is open. */
  start(): void {
    if (this.ws || this.destroyed) return
    this.connect()
  }
}
