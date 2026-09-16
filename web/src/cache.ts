// The offline read cache: the note list (tree), the status, and the last
// N opened notes with their rendered HTML, kept in IndexedDB so an
// installed app opened with no network still shows yesterday's reading.
// The cache is a copy of what the API already sent; it never serves as
// the source of truth, and edits do not pass through it (they live in
// each note's CRDT document, persisted by y-indexeddb in sync.ts).

import type { Note, SpaceTree, Status } from './api'

const DB_NAME = 'yana-cache'
const DB_VERSION = 1
const NOTES = 'notes' // key: note id, value: { note, at }
const KV = 'kv' // key: 'tree' | 'status' | 'user'
const NOTES_MAX = 20

let dbPromise: Promise<IDBDatabase | null> | null = null

function db(): Promise<IDBDatabase | null> {
  dbPromise ??= new Promise((resolve) => {
    if (!('indexedDB' in window)) {
      resolve(null)
      return
    }
    const req = indexedDB.open(DB_NAME, DB_VERSION)
    req.onupgradeneeded = () => {
      const d = req.result
      if (!d.objectStoreNames.contains(NOTES)) d.createObjectStore(NOTES)
      if (!d.objectStoreNames.contains(KV)) d.createObjectStore(KV)
    }
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => resolve(null)
    req.onblocked = () => resolve(null)
  })
  return dbPromise
}

async function tx<T>(store: string, mode: IDBTransactionMode, run: (s: IDBObjectStore) => IDBRequest<T>): Promise<T | null> {
  const d = await db()
  if (!d) return null
  return new Promise((resolve) => {
    const t = d.transaction(store, mode)
    const req = run(t.objectStore(store))
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => resolve(null)
  })
}

// --- notes -----------------------------------------------------------------

/** Remember a note and its render; evicts the least recently opened. */
export async function putNote(note: Note): Promise<void> {
  await tx(NOTES, 'readwrite', (s) => s.put({ note, at: Date.now() }, note.id))
  void evict()
}

async function evict(): Promise<void> {
  const all = await allNotes()
  if (!all || all.length <= NOTES_MAX) return
  const excess = all
    .sort((a, b) => a.at - b.at)
    .slice(0, all.length - NOTES_MAX)
  const d = await db()
  if (!d) return
  const t = d.transaction(NOTES, 'readwrite')
  const s = t.objectStore(NOTES)
  for (const e of excess) s.delete(e.note.id)
}

/** Every cached note entry, most recent first. */
export async function allNotes(): Promise<Array<{ note: Note; at: number }>> {
  const got = await tx<unknown[]>(NOTES, 'readonly', (s) => s.getAll() as IDBRequest<unknown[]>)
  return (got ?? []).filter((e): e is { note: Note; at: number } => {
    const v = e as { note?: Note; at?: number } | null
    return Boolean(v?.note?.id)
  })
}

/** The cached copy of a note, for reading while offline. */
export async function getNote(id: string): Promise<Note | null> {
  const got = await tx<{ note: Note; at: number } | undefined>(NOTES, 'readonly', (s) => s.get(id))
  return got?.note ?? null
}

// --- key/value -------------------------------------------------------------

export async function getTree(): Promise<SpaceTree[] | null> {
  const got = await tx<unknown>(KV, 'readonly', (s) => s.get('tree'))
  const v = got as { spaces?: SpaceTree[] } | null | undefined
  return Array.isArray(v?.spaces) ? v.spaces : null
}

export async function putTree(spaces: SpaceTree[]): Promise<void> {
  await tx(KV, 'readwrite', (s) => s.put({ spaces }, 'tree'))
}

export async function getStatus(): Promise<Status | null> {
  const got = await tx<Status>(KV, 'readonly', (s) => s.get('status'))
  return got ?? null
}

export async function putStatus(status: Status): Promise<void> {
  await tx(KV, 'readwrite', (s) => s.put(status, 'status'))
}

/** True when the error is the network being unreachable, not the server
 * refusing; fetched through here so every caller agrees on the test. */
export function networkDown(err: unknown): boolean {
  return err instanceof TypeError
}
