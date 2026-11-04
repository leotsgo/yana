// The graph payload, fetched once and shared by every graph on screen. While
// one is subscribed, and for a minute after, the spaces in the payload are
// watched over the relay. A change refetches it (debounced) and, when the
// payload differs, hands every subscriber the new one; with nobody
// subscribed it only marks the payload stale, and the next subscribe
// refetches. Sign-out drops it all (resetGraphData).

import { api } from './api'
import type { GraphPayload } from './graph/model'
import { SpaceWatch } from './watch'

/** How long a change signal waits before the refetch, so a run of typing
 * arrives as one. */
const REFETCH_MS = 1500
/** How long the watch outlives its last subscriber: the details panel closes
 * on every note switch, and reopening it on the next note should not refetch. */
const LINGER_MS = 60_000

let cache: Promise<GraphPayload> | undefined
const subs = new Set<(g: GraphPayload) => void>()
let watch: SpaceWatch | null = null
let timer: number | undefined
let linger: number | undefined
let stale = false // the cache may be behind: refetch on the next subscribe
let json = '' // the cached payload as JSON, to tell a refetch that changed nothing
let gen = 0 // bumped by resetGraphData: a fetch from before it is dropped

function watchSpaces(g: GraphPayload): void {
  if (!watch) return
  watch.watch([...new Set(g.notes.map((n) => n.space))])
  watch.start()
}

function refetch(): void {
  // Nobody shows it (the watch is lingering): leave it to the next subscribe.
  if (!subs.size) {
    stale = true
    return
  }
  const at = gen
  api
    .graph()
    .then((g) => {
      if (at !== gen) return
      // Most changes are typing that touches no link: nothing to redraw.
      const text = JSON.stringify(g)
      if (text === json) return
      json = text
      cache = Promise.resolve(g)
      watchSpaces(g)
      for (const fn of subs) fn(g)
    })
    .catch(() => {
      // Keep the payload we have; the next change tries again.
    })
}

/** The payload: one in-flight fetch, then the cached one. */
export function graphData(): Promise<GraphPayload> {
  if (!cache) {
    const p = api.graph()
    cache = p
    p.then((g) => {
      if (cache !== p) return
      json = JSON.stringify(g)
      watchSpaces(g)
    }, () => {
      if (cache === p) cache = undefined // let the next open retry
    })
  }
  return cache
}

/** Calls fn with each refetched payload; returns the unsubscribe. */
export function onGraphData(fn: (g: GraphPayload) => void): () => void {
  subs.add(fn)
  window.clearTimeout(linger)
  if (!watch) {
    watch = new SpaceWatch('user:graph', () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(refetch, REFETCH_MS)
    })
    if (!stale) cache?.then(watchSpaces, () => {})
  }
  if (stale) {
    stale = false
    refetch()
  }
  return () => {
    subs.delete(fn)
    if (subs.size || !watch) return
    window.clearTimeout(linger)
    linger = window.setTimeout(() => {
      if (subs.size || !watch) return
      window.clearTimeout(timer)
      watch.destroy()
      watch = null
      stale ||= cache !== undefined
    }, LINGER_MS)
  }
}

/** Forget the payload and stop watching: the next account must not see the
 * last one's graph. Called on sign-out, however it happens. */
export function resetGraphData(): void {
  gen++
  cache = undefined
  json = ''
  stale = false
  window.clearTimeout(timer)
  window.clearTimeout(linger)
  watch?.destroy()
  watch = null
}
