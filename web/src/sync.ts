// Realtime sync for one note over the relay protocol (docs/realtime.md):
// a WebSocket carrying binary msgpack frames, a Yjs document per note,
// keystroke batching, awareness for presence, and reconnect with
// exponential backoff. Offline edits queue locally and merge both ways on
// reconnect.

import * as Y from 'yjs'
import { encode, decode } from '@msgpack/msgpack'
import { Awareness, applyAwarenessUpdate, encodeAwarenessUpdate } from 'y-protocols/awareness'

/** Client-to-server frame. Field names are the wire keys in docs/realtime.md. */
interface ClientFrame {
  t: 'sub' | 'unsub' | 'upd' | 'aw' | 'ping'
  n?: string
  sv?: Uint8Array
  u?: Uint8Array
  a?: string
  p?: Uint8Array
}

/** Server-to-client frame. */
interface ServerFrame {
  t: 'subd' | 'upd' | 'aw' | 'moved' | 'deleted' | 'pong' | 'err'
  n?: string
  u?: Uint8Array
  a?: string
  p?: Uint8Array
  path?: string
  c?: string
  r?: string
}

export type SyncStatus = 'connecting' | 'synced' | 'offline'

export interface PresenceUser {
  name: string
  color: string
}

/** What each editor puts in the awareness state. */
export interface PresenceState {
  user: PresenceUser
  cursor: { line: number; col: number; index: number; length: number } | null
}

export interface SyncEvents {
  /** Connection state changed; offline means local edits are kept. */
  onStatus?(status: SyncStatus, detail?: string): void
  /** A relay error arrived (rate limit, unknown note, ...). */
  onError?(code: string, reason: string): void
  /** The note's file moved or disappeared; the view should refetch. */
  onGone?(kind: 'moved' | 'deleted', path?: string): void
  /** Awareness states changed; presence UI should rerender. */
  onPresence?(): void
}

const FLUSH_MS = 50 // keystroke batching window
const BACKOFF_MIN_MS = 500
const BACKOFF_MAX_MS = 8000
const KEEPALIVE_MS = 25_000

/** A stable per-browser identity until accounts exist (Phase 4). */
function localAuthor(): PresenceUser {
  const palette = ['#c65314', '#7a3fa8', '#2c7fb8', '#33812e', '#b03434', '#a07719', '#0e7c86', '#8a4b6d']
  let name = ''
  try {
    name = localStorage.getItem('yana.author') ?? ''
    if (!name) {
      const animals = ['otter', 'heron', 'fox', 'moth', 'lynx', 'wren', 'ibex', 'orca', 'elk', 'ray']
      name = `${animals[Math.floor(Math.random() * animals.length)]}-${Math.floor(Math.random() * 90 + 10)}`
      localStorage.setItem('yana.author', name)
    }
  } catch {
    name = 'guest'
  }
  let hash = 0
  for (const ch of name) hash = (hash * 31 + ch.charCodeAt(0)) | 0
  return { name, color: palette[Math.abs(hash) % palette.length] ?? '#666' }
}

export class SyncClient {
  readonly doc = new Y.Doc()
  readonly text: Y.Text
  readonly awareness: Awareness
  readonly author: string

  private readonly noteID: string
  private readonly events: SyncEvents
  private ws: WebSocket | null = null
  private attempts = 0
  private retryTimer: number | undefined
  private flushTimer: number | undefined
  private keepaliveTimer: number | undefined
  private pending: Uint8Array[] = []
  private pendingAwareness: Uint8Array[] = []
  private lastSentAt = 0
  private synced = false
  private destroyed = false
  private composing = false

  constructor(noteID: string, events: SyncEvents = {}) {
    this.noteID = noteID
    this.events = events
    this.text = this.doc.getText('body')
    this.author = `user:${localAuthor().name}`
    this.awareness = new Awareness(this.doc)

    this.doc.on('update', (update: Uint8Array, origin: unknown) => {
      if (origin === 'remote') return
      this.pending.push(update)
      this.scheduleFlush()
    })
    this.awareness.on('update', ({ added, updated, removed }: { added: number[]; updated: number[]; removed: number[] }) => {
      const clients = [...added, ...updated, ...removed]
      if (clients.length === 0) return
      this.pendingAwareness.push(encodeAwarenessUpdate(this.awareness, clients))
      this.scheduleFlush()
    })
    this.connect()
  }

  get status(): SyncStatus {
    if (this.synced) return 'synced'
    return this.ws && this.ws.readyState === WebSocket.OPEN ? 'connecting' : 'offline'
  }

  /** Local editing composition state; remote splices wait for it to end. */
  setComposing(v: boolean): void {
    this.composing = v
  }

  private connect(): void {
    if (this.destroyed) return
    this.events.onStatus?.(this.attempts === 0 ? 'connecting' : 'offline')
    const url = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws`
    let ws: WebSocket
    try {
      ws = new WebSocket(url)
    } catch {
      this.scheduleReconnect()
      return
    }
    ws.binaryType = 'arraybuffer'
    this.ws = ws

    ws.onopen = () => {
      // Ask for only what this document is missing; on first open that is
      // everything, on reconnect only the delta.
      this.send({ t: 'sub', n: this.noteID, sv: Y.encodeStateVector(this.doc), a: this.author })
      this.lastSentAt = Date.now()
      this.startKeepalive()
    }
    ws.onmessage = (ev) => {
      if (!(ev.data instanceof ArrayBuffer)) return
      this.handle(decode(new Uint8Array(ev.data)) as ServerFrame)
    }
    ws.onclose = () => {
      this.stopKeepalive()
      this.synced = false
      this.ws = null
      this.scheduleReconnect()
    }
    ws.onerror = () => {
      ws.close()
    }
  }

  private handle(frame: ServerFrame): void {
    switch (frame.t) {
      case 'subd':
        if (frame.u && frame.u.length > 0) Y.applyUpdate(this.doc, frame.u, 'remote')
        this.synced = true
        this.attempts = 0
        this.events.onStatus?.('synced')
        // Push anything queued while offline; the server merges both ways.
        this.flush()
        break
      case 'upd':
        if (frame.u && frame.u.length > 0) Y.applyUpdate(this.doc, frame.u, 'remote')
        break
      case 'aw':
        if (frame.p && frame.p.length > 0) {
          applyAwarenessUpdate(this.awareness, frame.p, 'remote')
          this.events.onPresence?.()
        }
        break
      case 'pong':
        break
      case 'err':
        this.events.onError?.(frame.c ?? 'error', frame.r ?? '')
        break
      case 'moved':
        this.events.onGone?.('moved', frame.path)
        break
      case 'deleted':
        this.events.onGone?.('deleted')
        break
    }
  }

  private send(frame: ClientFrame): void {
    const ws = this.ws
    if (!ws || ws.readyState !== WebSocket.OPEN) return
    ws.send(encode(frame))
    this.lastSentAt = Date.now()
  }

  private scheduleFlush(): void {
    if (this.flushTimer !== undefined || !this.synced) return
    this.flushTimer = window.setTimeout(() => {
      this.flushTimer = undefined
      this.flush()
    }, FLUSH_MS)
  }

  /** Send queued updates and awareness payloads, merged per batch. */
  private flush(): void {
    this.flushTimer = undefined
    if (!this.synced) return
    if (this.pending.length > 0) {
      this.send({ t: 'upd', n: this.noteID, u: Y.mergeUpdates(this.pending), a: this.author })
      this.pending = []
    }
    if (this.pendingAwareness.length > 0) {
      for (const p of this.pendingAwareness) {
        this.send({ t: 'aw', n: this.noteID, p })
      }
      this.pendingAwareness = []
    }
  }

  private scheduleReconnect(): void {
    if (this.destroyed || this.retryTimer !== undefined) return
    const delay = Math.min(BACKOFF_MIN_MS * 2 ** this.attempts, BACKOFF_MAX_MS) + Math.random() * 250
    this.attempts++
    this.events.onStatus?.('offline')
    this.retryTimer = window.setTimeout(() => {
      this.retryTimer = undefined
      this.connect()
    }, delay)
  }

  private startKeepalive(): void {
    this.stopKeepalive()
    this.keepaliveTimer = window.setInterval(() => {
      if (Date.now() - this.lastSentAt >= KEEPALIVE_MS) this.send({ t: 'ping' })
    }, KEEPALIVE_MS)
  }

  private stopKeepalive(): void {
    if (this.keepaliveTimer !== undefined) {
      window.clearInterval(this.keepaliveTimer)
      this.keepaliveTimer = undefined
    }
  }

  /** True while an IME composition is in progress (remote splices wait). */
  get isComposing(): boolean {
    return this.composing
  }

  destroy(): void {
    this.destroyed = true
    if (this.retryTimer !== undefined) window.clearTimeout(this.retryTimer)
    if (this.flushTimer !== undefined) window.clearTimeout(this.flushTimer)
    this.stopKeepalive()
    const ws = this.ws
    this.ws = null
    if (ws) {
      ws.onclose = null
      ws.onerror = null
      ws.close(1000, 'leaving')
    }
    this.awareness.destroy()
    this.doc.destroy()
  }
}
