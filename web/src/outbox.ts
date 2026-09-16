// The outbox: REST-only changes made while offline (create a note, open
// today's daily note, upload a file, land a share) are queued here in
// order and replayed one by one when the server answers again. Edits to
// an open note do not pass through this; they are CRDT updates that
// persist per note and merge on reconnect.

import { api, ApiError } from './api'
import * as cache from './cache'
import { appendToNote, composeShareBlock, today } from './sharelib'

export type OutboxEntry =
  | { kind: 'create'; path: string; content?: string }
  | { kind: 'daily'; space: string; date: string }
  | { kind: 'upload'; path: string; name: string; blob: Blob }
  | { kind: 'share'; space: string; date: string; title: string; text: string; url: string }

const DB_NAME = 'yana-outbox'
const DB_VERSION = 1
const QUEUE = 'queue' // auto-increment key: insertion order is replay order

export interface OutboxState {
  /** Entries waiting for the network. */
  pending: number
  /** A replay is running right now. */
  sending: boolean
  /** The last replay result worth a line in the UI, cleared on read. */
  message: string | null
}

let state: OutboxState = { pending: 0, sending: false, message: null }
const listeners = new Set<() => void>()

function set(patch: Partial<OutboxState>): void {
  state = { ...state, ...patch }
  for (const l of listeners) l()
}

export function outboxState(): OutboxState {
  return state
}

export function subscribeOutbox(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

let dbPromise: Promise<IDBDatabase | null> | null = null

function db(): Promise<IDBDatabase | null> {
  dbPromise ??= new Promise((resolve) => {
    if (!('indexedDB' in window)) {
      resolve(null)
      return
    }
    const req = indexedDB.open(DB_NAME, DB_VERSION)
    req.onupgradeneeded = () => {
      if (!req.result.objectStoreNames.contains(QUEUE)) req.result.createObjectStore(QUEUE, { autoIncrement: true })
    }
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => resolve(null)
  })
  return dbPromise
}

/** Add one change to the queue. Order is the order entries were made. */
export async function enqueue(entry: OutboxEntry): Promise<void> {
  const d = await db()
  if (!d) return
  await new Promise<void>((resolve) => {
    const t = d.transaction(QUEUE, 'readwrite')
    t.objectStore(QUEUE).add(entry)
    t.oncomplete = () => resolve()
    t.onerror = () => resolve()
  })
  set({ pending: state.pending + 1 })
}

/** Queue today's daily note as the share target. */
export function enqueueShare(space: string, title: string, text: string, url: string): void {
  void enqueue({ kind: 'share', space, date: today(), title, text, url })
}

async function readAll(): Promise<Array<{ key: IDBValidKey; entry: OutboxEntry }>> {
  const d = await db()
  if (!d) return []
  return new Promise((resolve) => {
    const t = d.transaction(QUEUE, 'readonly')
    const req = t.objectStore(QUEUE).openCursor()
    const out: Array<{ key: IDBValidKey; entry: OutboxEntry }> = []
    req.onsuccess = () => {
      const cursor = req.result
      if (!cursor) {
        resolve(out)
        return
      }
      const v = cursor.value as OutboxEntry | null
      if (v?.kind) out.push({ key: cursor.key, entry: v })
      cursor.continue()
    }
    req.onerror = () => resolve([])
  })
}

async function removeKey(key: IDBValidKey): Promise<void> {
  const d = await db()
  if (!d) return
  await new Promise<void>((resolve) => {
    const t = d.transaction(QUEUE, 'readwrite')
    t.objectStore(QUEUE).delete(key)
    t.oncomplete = () => resolve()
    t.onerror = () => resolve()
  })
}

let draining = false

/** Replay the queue in order. A network failure stops the replay where it
 * is; the server refusing an entry (a path that now exists, an upload
 * past its limit) drops that entry with a message and keeps going. */
export async function drain(): Promise<void> {
  if (draining) return
  draining = true
  set({ sending: true })
  try {
    let sent = 0
    const messages: string[] = []
    for (;;) {
      const all = await readAll()
      if (all.length === 0) break
      const first = all[0]
      if (!first) break
      const { key, entry } = first
      const outcome = await send(entry)
      if (outcome === 'network') break
      await removeKey(key)
      if (outcome === 'sent') sent++
      else messages.push(outcome)
    }
    const all = await readAll()
    set({
      pending: all.length,
      sending: false,
      message:
        messages.length > 0
          ? messages.join(' ')
          : sent > 0
            ? `Sent ${sent} queued change${sent === 1 ? '' : 's'}.`
            : null,
    })
  } finally {
    draining = false
  }
}

/** Runs one entry. Returns 'sent', 'network' (stop the replay and retry
 * later), or a message string for an entry the server refused (the entry
 * is dropped). */
async function send(entry: OutboxEntry): Promise<'sent' | 'network' | string> {
  try {
    switch (entry.kind) {
      case 'create':
        await api.createNote(entry.path, entry.content)
        return 'sent'
      case 'daily':
        await api.daily(entry.space, entry.date)
        return 'sent'
      case 'upload': {
        const res = await api.upload(entry.path, entry.blob)
        if (res.name !== entry.name) {
          return `Uploaded ${entry.name} as ${res.name}; the link in the note may need a fix.`
        }
        return 'sent'
      }
      case 'share': {
        const res = await api.daily(entry.space, entry.date)
        await appendToNote(res.id, composeShareBlock(entry.title, entry.text, entry.url))
        return 'sent'
      }
    }
  } catch (err) {
    if (cache.networkDown(err)) return 'network'
    // Without a session the queue keeps its place; sign-in or a refresh
    // can still make it sendable.
    if (err instanceof ApiError && err.status === 401) return 'network'
    if (err instanceof ApiError) return `Could not replay a queued change: ${err.message}`
    return 'Could not replay a queued change.'
  }
}
