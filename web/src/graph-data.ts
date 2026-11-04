// The graph payload, fetched once and shared by every graph on screen. While
// one is subscribed, the spaces in the payload are watched over the relay and
// a change refetches it (debounced), handing every subscriber the new one.

import { api } from './api'
import type { GraphPayload } from './graph/model'
import { SpaceWatch } from './watch'

/** How long a change signal waits before the refetch, so a run of typing
 * arrives as one. */
const REFETCH_MS = 1500

let cache: Promise<GraphPayload> | undefined
const subs = new Set<(g: GraphPayload) => void>()
let watch: SpaceWatch | null = null
let timer: number | undefined
let stale = false // nobody watched for a while: refetch on the next subscribe

function watchSpaces(g: GraphPayload): void {
  if (!watch) return
  watch.watch([...new Set(g.notes.map((n) => n.space))])
  watch.start()
}

function refetch(): void {
  api
    .graph()
    .then((g) => {
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
    p.then(watchSpaces, () => {
      if (cache === p) cache = undefined // let the next open retry
    })
  }
  return cache
}

/** Calls fn with each refetched payload; returns the unsubscribe. */
export function onGraphData(fn: (g: GraphPayload) => void): () => void {
  subs.add(fn)
  if (!watch) {
    watch = new SpaceWatch('user:graph', () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(refetch, REFETCH_MS)
    })
    if (stale) {
      stale = false
      refetch()
    } else cache?.then(watchSpaces, () => {})
  }
  return () => {
    subs.delete(fn)
    if (subs.size || !watch) return
    window.clearTimeout(timer)
    watch.destroy()
    watch = null
    stale = cache !== undefined
  }
}
