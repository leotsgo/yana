// Loads the graph bundle (WebGL renderer and input) the first time the graph
// opens. It and its d3-force worker are built apart from the app under
// assets/graph/ and are not precached: the service worker keeps them once
// fetched, through its cacheFirst rule for /assets/.
type Graph = typeof import('./graph/view')

// The hashed bundle paths, filled in by build.mjs.
declare const __GRAPH_JS__: string
declare const __GRAPH_WORKER__: string

/** URL of the physics worker, for MountOptions.workerURL. */
export const GRAPH_WORKER: string = __GRAPH_WORKER__

let load: Promise<Graph> | undefined

export function loadGraph(): Promise<Graph> {
  if (load) return load
  // A variable, so esbuild leaves the import to the browser instead of
  // bundling the graph into the app.
  const url = __GRAPH_JS__
  load = (import(url) as Promise<Graph>).catch((err: unknown) => {
    load = undefined // offline the first time: let the next open retry
    throw err
  })
  return load
}
