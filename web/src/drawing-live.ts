// A drawing's room on the relay (docs/realtime.md): a relay-only `sub`, so
// the server loads no document for it; `dr` frames carry scene deltas,
// `aw` frames this page's pointer and selection, and `chg` says the file
// changed on disk. Reconnects with backoff like the note sync; the page
// leaves the room while hidden, and says goodbye so its cursor goes at
// once rather than when it times out.

import { encode, decode } from '@msgpack/msgpack'

import { tryRefresh, wsURL } from './auth'
import type { Delta } from './drawing-delta'
import { presence } from './sync'

interface ServerFrame {
  t: string
  n?: string
  p?: Uint8Array
  h?: string
  c?: string
  r?: string
}

/** What a page tells the room about itself; gone says it left. */
export interface Pointer {
  /** This page: one per open drawing, not per account. */
  c: string
  name: string
  color: string
  x: number
  y: number
  button: 'up' | 'down'
  selected: Record<string, true>
  gone?: true
}

export interface RoomEvents {
  /** Joined (again): the room may hold changes this page never saw. */
  onJoined(): void
  onDelta(d: Delta): void
  /** The others' pointers, by page, after any change. */
  onPointers(peers: Map<string, Pointer>): void
  /** The file changed on disk; hash is its content hash when known. */
  onChanged(hash: string | undefined): void
  onError?(code: string, reason: string): void
}

/** The relay's default frame cap is 1 MiB; deltas stay under it with room for the envelope. */
export const MAX_FRAME = 900_000

const POINTER_MS = 120 // like the text editor's cursor batching
const IDLE_MS = 30_000 // a pointer not heard from this long is dropped
const BACKOFF_MIN_MS = 500
const BACKOFF_MAX_MS = 8000
const KEEPALIVE_MS = 25_000

export class DrawingRoom {
  readonly me = presence()
  private readonly c = Math.random().toString(36).slice(2, 10)
  private ws: WebSocket | null = null
  private joined = false
  private paused = false
  private destroyed = false
  private attempts = 0
  private retryTimer: number | undefined
  private keepalive: number | undefined
  private pointerTimer: number | undefined
  private lastSentAt = 0
  private pointer: Pointer | null = null
  private peers = new Map<string, Pointer & { at: number }>()
  private sweep: number

  constructor(
    private readonly note: string,
    private readonly events: RoomEvents,
  ) {
    this.sweep = window.setInterval(() => {
      const old = Date.now() - IDLE_MS
      let gone = false
      for (const [k, p] of this.peers) if (p.at < old) gone = this.peers.delete(k)
      if (gone) this.emitPointers()
    }, 5000)
    this.connect()
  }

  /** Sends delta payloads (from pack); dropped while not joined, as the next join re-sends the scene. */
  send(payloads: string[]): void {
    for (const p of payloads) this.frame({ t: 'dr', n: this.note, p: new TextEncoder().encode(p) })
  }

  /** This page's pointer and selection, batched. */
  point(x: number, y: number, button: 'up' | 'down', selected: Record<string, true>): void {
    this.pointer = { c: this.c, name: this.me.name, color: this.me.color, x, y, button, selected }
    if (this.pointerTimer !== undefined) return
    this.pointerTimer = window.setTimeout(() => {
      this.pointerTimer = undefined
      this.sendPointer()
    }, POINTER_MS)
  }

  /** Leaves the room while the page is hidden; resume joins again. */
  pause(): void {
    if (this.paused) return
    this.paused = true
    this.close()
  }

  resume(): void {
    if (!this.paused) return
    this.paused = false
    this.connect()
  }

  destroy(): void {
    this.destroyed = true
    window.clearInterval(this.sweep)
    this.close()
  }

  private sendPointer(): void {
    if (this.pointer) this.frame({ t: 'aw', n: this.note, p: new TextEncoder().encode(JSON.stringify(this.pointer)) })
  }

  private frame(f: { t: string; n?: string; p?: Uint8Array; rl?: boolean }): void {
    const ws = this.ws
    if (!ws || ws.readyState !== WebSocket.OPEN || (!this.joined && f.t !== 'sub' && f.t !== 'ping')) return
    ws.send(encode(f))
    this.lastSentAt = Date.now()
  }

  private emitPointers(): void {
    this.events.onPointers(new Map(this.peers))
  }

  private connect(): void {
    if (this.destroyed || this.paused || this.ws) return
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
      this.frame({ t: 'sub', n: this.note, rl: true })
      window.clearInterval(this.keepalive)
      this.keepalive = window.setInterval(() => {
        if (Date.now() - this.lastSentAt >= KEEPALIVE_MS) this.frame({ t: 'ping' })
      }, KEEPALIVE_MS)
    }
    ws.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) this.handle(decode(new Uint8Array(ev.data)) as ServerFrame)
    }
    ws.onclose = () => {
      const early = !this.joined
      this.dropSocket()
      // A handshake refused is often an expired token: renew it so the
      // next attempt carries a fresh one.
      if (early && !this.destroyed) tryRefresh().catch(() => {})
      this.scheduleReconnect()
    }
    ws.onerror = () => ws.close()
  }

  private handle(f: ServerFrame): void {
    switch (f.t) {
      case 'subd':
        this.joined = true
        this.attempts = 0
        this.events.onJoined()
        this.sendPointer()
        break
      case 'dr':
        if (f.p) {
          try {
            this.events.onDelta(JSON.parse(new TextDecoder().decode(f.p)) as Delta)
          } catch {
            // Not a delta this page can read; the save brings it.
          }
        }
        break
      case 'aw':
        if (f.p) {
          try {
            const p = JSON.parse(new TextDecoder().decode(f.p)) as Pointer
            if (typeof p.c !== 'string' || p.c === this.c) return
            if (p.gone) this.peers.delete(p.c)
            else this.peers.set(p.c, { ...p, at: Date.now() })
            this.emitPointers()
          } catch {
            // A text note's awareness, or garbage: not a pointer.
          }
        }
        break
      case 'chg':
        this.events.onChanged(f.h || undefined)
        break
      case 'upd':
        // The drawing's text document is loaded elsewhere (an agent's
        // write): the file is changing, hash unknown.
        this.events.onChanged(undefined)
        break
      case 'err':
        this.events.onError?.(f.c ?? 'error', f.r ?? '')
        break
    }
  }

  /** Says goodbye and closes the socket, without reconnecting. */
  private close(): void {
    window.clearTimeout(this.retryTimer)
    this.retryTimer = undefined
    window.clearTimeout(this.pointerTimer)
    this.pointerTimer = undefined
    const ws = this.ws
    if (ws && this.joined) {
      ws.send(encode({ t: 'aw', n: this.note, p: new TextEncoder().encode(JSON.stringify({ c: this.c, gone: true })) }))
    }
    this.dropSocket()
    if (ws) {
      ws.onclose = null
      ws.onerror = null
      ws.close(1000, 'leaving')
    }
  }

  private dropSocket(): void {
    window.clearInterval(this.keepalive)
    this.keepalive = undefined
    this.ws = null
    this.joined = false
    if (this.peers.size > 0) {
      // Out of the room, the others' pointers would only go stale.
      this.peers.clear()
      this.emitPointers()
    }
  }

  private scheduleReconnect(): void {
    if (this.destroyed || this.paused || this.retryTimer !== undefined) return
    const delay = Math.min(BACKOFF_MIN_MS * 2 ** this.attempts, BACKOFF_MAX_MS) + Math.random() * 250
    this.attempts++
    this.retryTimer = window.setTimeout(() => {
      this.retryTimer = undefined
      this.connect()
    }, delay)
  }
}
