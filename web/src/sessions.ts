// One realtime session per note, however many panes show it. The first
// pane to open a note starts its SyncClient; a second pane on the same
// note joins that client (the same Yjs document, the same socket), and
// the session ends when the last pane lets go. Each pane registers its
// own event handlers; the client's events fan out to all of them.

import { SyncClient } from './sync'
import type { SyncEvents } from './sync'

interface Session {
  client: SyncClient
  subs: Set<SyncEvents>
}

const live = new Map<string, Session>()

/** The note's session, started if it is not running. `joined` is true
 * when it was already running: its state is read from the client, since
 * the events that set it have been and gone. */
export function acquire(id: string, events: SyncEvents): { client: SyncClient; joined: boolean } {
  const had = live.get(id)
  if (had) {
    had.subs.add(events)
    return { client: had.client, joined: true }
  }
  const subs = new Set<SyncEvents>([events])
  const each = (fn: (e: SyncEvents) => void) => {
    for (const e of subs) fn(e)
  }
  const client = new SyncClient(id, {
    onStatus: (s, d) => each((e) => e.onStatus?.(s, d)),
    onError: (c, r) => each((e) => e.onError?.(c, r)),
    onGone: (k, p) => each((e) => e.onGone?.(k, p)),
    onPresence: () => each((e) => e.onPresence?.()),
    onLocal: () => each((e) => e.onLocal?.()),
    onEdit: () => each((e) => e.onEdit?.()),
  })
  live.set(id, { client, subs })
  return { client, joined: false }
}

/** Lets go of a session; the last one out ends it. */
export function release(id: string, events: SyncEvents): void {
  const s = live.get(id)
  if (!s) return
  s.subs.delete(events)
  if (s.subs.size > 0) return
  live.delete(id)
  s.client.destroy()
}
