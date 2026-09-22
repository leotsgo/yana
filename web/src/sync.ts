// Realtime sync for one note over the relay protocol (docs/realtime.md):
// a WebSocket carrying binary msgpack frames, a Yjs document per note,
// keystroke batching, awareness for presence, and reconnect with
// exponential backoff. Offline edits queue locally and merge both ways on
// reconnect. The document is also persisted to IndexedDB (y-indexeddb),
// so a note edited offline survives the tab closing and merges the next
// time the note opens.

import * as Y from 'yjs'
import { encode, decode } from '@msgpack/msgpack'
import { Awareness, applyAwarenessUpdate, encodeAwarenessUpdate } from 'y-protocols/awareness'
import { IndexeddbPersistence } from 'y-indexeddb'

import { expire, tryRefresh, user, wsURL } from './auth'
import { displayName } from './prefs'

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
  colorLight: string
}

/** What each editor puts in the awareness state. The cursor is what the
 * editor binding writes: relative positions in the document. */
export interface PresenceState {
  user: PresenceUser
  cursor: { anchor: unknown; head: unknown } | null
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
  /** The local copy of the document is loaded (or storage was
   * unavailable); the note can be read and edited offline from here. */
  onLocal?(): void
  /** This client changed the document: typing, a ticked box, a title. */
  onEdit?(): void
}

const FLUSH_MS = 50 // keystroke batching window
const AWARENESS_MS = 120 // cursor moves are batched more coarsely than edits
const BACKOFF_MIN_MS = 500
const BACKOFF_MAX_MS = 8000
const KEEPALIVE_MS = 25_000

/** A stable per-browser identity when the server runs without accounts:
 * the display name from settings, else a generated one kept in storage. */
function localAuthor(): PresenceUser {
  let name = displayName()
  if (name) return withColor(name)
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
  return withColor(name)
}

const palette = ['#c65314', '#7a3fa8', '#2c7fb8', '#33812e', '#b03434', '#a07719', '#0e7c86', '#8a4b6d']

function withColor(name: string): PresenceUser {
  let hash = 0
  for (const ch of name) hash = (hash * 31 + ch.charCodeAt(0)) | 0
  const color = palette[Math.abs(hash) % palette.length] ?? '#666666'
  return { name, color, colorLight: color + '33' }
}

/** Presence identity: the display name from settings when set, else the
 * signed-in account's username, else the local fallback. */
export function presence(): PresenceUser {
  const u = user()
  if (!u) return localAuthor()
  return withColor(displayName() || u.username)
}

export class SyncClient {
  readonly doc = new Y.Doc()
  readonly text: Y.Text
  readonly awareness: Awareness
  readonly author: string

  private readonly noteID: string
  private readonly events: SyncEvents
  private readonly idb: IndexeddbPersistence
  private ws: WebSocket | null = null
  private attempts = 0
  private retryTimer: number | undefined
  private flushTimer: number | undefined
  private keepaliveTimer: number | undefined
  private pending: Uint8Array[] = []
  private awarenessDirty = false
  private awarenessTimer: number | undefined
  private lastSentAt = 0
  private synced = false
  private everSynced = false
  private localReady = false
  private destroyed = false
  private composing = false

  constructor(noteID: string, events: SyncEvents = {}) {
    this.noteID = noteID
    this.events = events
    this.text = this.doc.getText('body')
    const me = presence()
    this.author = `user:${me.name}`
    this.awareness = new Awareness(this.doc)
    this.awareness.setLocalStateField('user', me)

    // The document persists per note. Whatever this device edited before
    // — online or offline — loads into the doc here; updates it brings
    // queue like local edits and merge with the server on connect.
    this.idb = new IndexeddbPersistence('yana-note-' + noteID, this.doc)
    this.idb.whenSynced.then(
      () => this.markLocalReady(),
      () => this.markLocalReady(), // storage blocked: the doc still works in memory
    )

    this.doc.on('update', (update: Uint8Array, origin: unknown) => {
      if (origin === 'remote') return
      this.pending.push(update)
      this.scheduleFlush()
      // What the device copy brings back was typed before, not now.
      if (origin !== this.idb) this.events.onEdit?.()
    })
    // Only this client's own state goes out; remote states arrive
    // through the relay and must not be echoed back.
    this.awareness.on('update', (_changes: unknown, origin: unknown) => {
      if (origin === 'remote') return
      this.awarenessDirty = true
      this.scheduleAwareness()
    })
    this.awareness.on('change', () => this.events.onPresence?.())
    this.connect()
  }

  private markLocalReady(): void {
    this.localReady = true
    this.events.onLocal?.()
  }

  get status(): SyncStatus {
    if (this.synced) return 'synced'
    return this.ws && this.ws.readyState === WebSocket.OPEN ? 'connecting' : 'offline'
  }

  /** True once the local copy of the document is in place. */
  get isLocalReady(): boolean {
    return this.localReady
  }

  /** Local editing composition state; remote splices wait for it to end. */
  setComposing(v: boolean): void {
    this.composing = v
  }

  private connect(): void {
    if (this.destroyed) return
    this.events.onStatus?.(this.attempts === 0 ? 'connecting' : 'offline')
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
      const failedEarly = !this.synced
      this.synced = false
      this.ws = null
      if (!this.destroyed && failedEarly) {
        // A handshake rejection is often an expired token; renew it so
        // the next attempt carries a fresh one. A renewal the server
        // refuses means this session was revoked: the shell signs out.
        tryRefresh()
          .then((ok) => {
            if (!ok) expire()
          })
          .catch(() => {
            // The server is unreachable; the reconnect loop keeps trying.
          })
      }
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
        this.everSynced = true
        this.attempts = 0
        this.events.onStatus?.('synced')
        // Push anything queued while offline; the server merges both ways.
        // Presence is re-announced since a new connection starts blank.
        this.awarenessDirty = true
        this.flush()
        break
      case 'upd':
        if (frame.u && frame.u.length > 0) Y.applyUpdate(this.doc, frame.u, 'remote')
        break
      case 'aw':
        if (frame.p && frame.p.length > 0) applyAwarenessUpdate(this.awareness, frame.p, 'remote')
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

  private scheduleAwareness(): void {
    if (this.awarenessTimer !== undefined || !this.synced) return
    this.awarenessTimer = window.setTimeout(() => {
      this.awarenessTimer = undefined
      this.flushAwareness()
    }, AWARENESS_MS)
  }

  /** Send queued updates, merged into one payload per batch. */
  private flush(): void {
    this.flushTimer = undefined
    if (!this.synced) return
    if (this.pending.length > 0) {
      this.send({ t: 'upd', n: this.noteID, u: Y.mergeUpdates(this.pending), a: this.author })
      this.pending = []
    }
    this.flushAwareness()
  }

  /** Send this client's current awareness state once, if it changed. */
  private flushAwareness(): void {
    if (!this.synced || !this.awarenessDirty) return
    this.awarenessDirty = false
    this.send({ t: 'aw', n: this.noteID, p: encodeAwarenessUpdate(this.awareness, [this.awareness.clientID]) })
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
    // Whatever was typed in the last batch window goes out before the
    // socket closes; leaving a note is not a reason to drop an edit.
    this.flush()
    if (this.awarenessTimer !== undefined) window.clearTimeout(this.awarenessTimer)
    this.stopKeepalive()
    const ws = this.ws
    this.ws = null
    if (ws) {
      ws.onclose = null
      ws.onerror = null
      ws.close(1000, 'leaving')
    }
    this.awareness.destroy()
    // The persistence provider closes its database after writing out
    // what it has; the doc outlives it just long enough for that.
    void this.idb.destroy().then(() => this.doc.destroy())
  }
}
