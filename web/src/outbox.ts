// The outbox: REST-only changes made while offline (create a note, open
// today's daily note, upload a file, land a share) are queued here in
// order and replayed one by one when the server answers again. Edits to
// an open note do not pass through this; they are CRDT updates that
// persist per note and merge on reconnect.
//
// A markdown note made offline (New, or Today when today's note is not
// on the device) is made here and now: a ULID like the server's, a cache
// entry, and its first text in its device document, so it opens and
// takes typing like any other note. Its entry carries the id; the replay
// creates the file under that id with an empty body, and the text,
// heading included, reaches the server only through the document — one
// path, so nothing lands twice. The editor's subscription is refused
// until then and asks again (sync.ts).

import { api, ApiError } from './api'
import type { Note } from './api'
import * as cache from './cache'
import { dailySeed, numbered, offlineBlock } from './localnote'
import type { LocalNote } from './localnote'
import { appendToNote, composeShareBlock, today } from './sharelib'
import { dropOnDevice, readOnDevice, seedOnDevice } from './sync'

export type OutboxEntry =
  | { kind: 'create'; path: string; content?: string; id?: string }
  | { kind: 'daily'; space: string; date: string; folder?: string; id?: string; path?: string }
  | { kind: 'upload'; path: string; name: string; blob: Blob }
  | { kind: 'share'; space: string; date: string; title: string; text: string; url: string; folder?: string }

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
  /** Notes made on this device that the server has not seen yet. */
  local: LocalNote[]
}

let state: OutboxState = { pending: 0, sending: false, message: null, local: [] }
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
  set({ pending: state.pending + 1, local: [...state.local, ...localOf([entry])] })
}

function localOf(entries: OutboxEntry[]): LocalNote[] {
  const out: LocalNote[] = []
  for (const e of entries) {
    if (e.kind === 'create' && e.id) out.push({ id: e.id, path: e.path })
    if (e.kind === 'daily' && e.id && e.path) out.push({ id: e.id, path: e.path })
  }
  return out
}

/** Makes a markdown note on this device: the entry that will create it
 * (carrying its id and path), its cache entry, and its first text in
 * the device copy of its document. */
export async function makeLocal(entry: OutboxEntry & { id: string }, path: string, body: string): Promise<void> {
  const slash = path.lastIndexOf('/')
  const now = new Date().toISOString()
  const note: Note = {
    id: entry.id, space: slash > 0 ? path.slice(0, path.indexOf('/')) : '', path,
    title: path.slice(slash + 1).replace(/\.(md|markdown)$/i, ''),
    preview: '', kind: 'md', size: 0, mtime: now, created: now, tags: [], base: slash > 0 ? path.slice(0, slash) : '', links: [],
    trusted: false, content_hash: '', role: 'editor', public: false, html: '', local: true,
  }
  await seedOnDevice(entry.id, body)
  await cache.putNote(note)
  await enqueue(entry)
}

/** Renames a note made offline before it is sent: its queued create
 * takes the new path. False when it is not waiting (sent already). */
export async function moveLocal(id: string, path: string): Promise<boolean> {
  const d = await db()
  if (!d) return false
  const found = await new Promise<boolean>((resolve) => {
    const t = d.transaction(QUEUE, 'readwrite')
    let hit = false
    const req = t.objectStore(QUEUE).openCursor()
    req.onsuccess = () => {
      const cursor = req.result
      if (!cursor) return
      const e = cursor.value as OutboxEntry
      if (e.kind === 'create' && e.id === id) {
        hit = true
        cursor.update({ ...e, path })
      } else cursor.continue()
    }
    t.oncomplete = () => resolve(hit)
    t.onerror = () => resolve(false)
  })
  if (found) set({ local: state.local.map((l) => (l.id === id ? { id, path } : l)) })
  return found
}

const moved = new Set<(from: string, to: string) => void>()

/** Runs fn when a note made here turns out to be another one on the
 * server (today's note, made on another device meanwhile): tabs on the
 * first should show the second. */
export function onMoved(fn: (from: string, to: string) => void): () => void {
  moved.add(fn)
  return () => moved.delete(fn)
}

/** Queue today's daily note as the share target. */
export function enqueueShare(space: string, title: string, text: string, url: string, folder?: string): void {
  void enqueue({ kind: 'share', space, date: today(), title, text, url, folder })
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
      local: localOf(all.map((a) => a.entry)),
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
        if (entry.id) return await createLocal(entry.id, entry.path)
        await api.createNote(entry.path, entry.content)
        return 'sent'
      case 'daily': {
        // A note made here goes up empty under its id; its text follows
        // through its document.
        const res = await api.daily(entry.space, entry.date, entry.folder, entry.id, entry.id ? '' : undefined)
        if (!entry.id || res.id === entry.id) return 'sent'
        return await mergeDaily(entry.id, res.id, entry.date)
      }
      case 'upload': {
        const res = await api.upload(entry.path, entry.blob)
        if (res.name !== entry.name) {
          return `Uploaded ${entry.name} as ${res.name}; the link in the note may need a fix.`
        }
        return 'sent'
      }
      case 'share': {
        const res = await api.daily(entry.space, entry.date, entry.folder)
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

/** Creates a note made offline under its id. A name taken meanwhile
 * gets a number, as a new note does online; an id already there means
 * an earlier replay made it and its answer was lost. */
async function createLocal(id: string, path: string): Promise<string> {
  for (let n = 2, p = path; ; p = numbered(path, n++)) {
    try {
      await api.createNote(p, '', id)
      return p === path ? 'sent' : `${path} was taken; the note made offline is ${p}.`
    } catch (err) {
      if (!(err instanceof ApiError) || (err.status !== 409 && err.status !== 400) || n >= 100) throw err
      if (err.status === 409 && (await exists(id))) return 'sent'
    }
  }
}

async function exists(id: string): Promise<boolean> {
  try {
    await api.note(id)
    return true
  } catch {
    return false
  }
}

/** Today's note was made on another device while this one made its own:
 * the tabs move to the server's note, what was written here goes onto
 * its end under a rule, and the note made here is forgotten. */
async function mergeDaily(local: string, server: string, date: string): Promise<'network' | string> {
  for (const fn of moved) fn(local, server)
  // The editor lets go of the note made here; its last keystrokes reach
  // the device copy first.
  await new Promise((r) => window.setTimeout(r, 400))
  const block = offlineBlock(await readOnDevice(local), dailySeed(date))
  if (block) {
    try {
      await appendToNote(server, block)
    } catch {
      return 'network' // the device copy stays; the next replay tries again
    }
  }
  await cache.deleteNote(local)
  await dropOnDevice(local)
  return block
    ? "Today's note was also made on another device; what you wrote offline was added to its end."
    : "Today's note was also made on another device; it is the one open now."
}
