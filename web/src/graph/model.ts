// Pure model of the graph view: turns the /api/graph payload into the nodes
// and links that get drawn. No imports; the test runner loads it directly.

export interface GraphNote { id: string; space: string; path: string; title: string }
export interface GraphGhost { space: string; target: string; from: number[] }
export interface GraphTag { space: string; tag: string; notes: number[] }
export interface GraphPayload {
  notes: GraphNote[]
  links: [number, number][]
  ghosts: GraphGhost[]
  tags: GraphTag[]
}

export type ColorBy = 'none' | 'space' | 'folder'
export interface ViewOptions {
  search: string
  orphans: boolean
  ghosts: boolean
  tags: boolean
  colorBy: ColorBy
  center?: string
  depth?: number
}
export interface ViewNode {
  key: string
  kind: 'note' | 'ghost' | 'tag'
  id: string
  label: string
  group: string
  degree: number
}
/** `links` is flat: [a0, b0, a1, b1, …], indices into `nodes`. */
export interface View { nodes: ViewNode[]; links: Int32Array }

export const RADIUS_MIN = 3
export const RADIUS_MAX = 24

function label(n: GraphNote): string {
  if (n.title) return n.title
  const file = n.path.slice(n.path.lastIndexOf('/') + 1)
  return file.replace(/\.excalidraw\.md$|\.md$|\.html$/i, '')
}

function group(n: GraphNote, by: ColorBy): string {
  if (by === 'space') return n.space
  if (by !== 'folder') return ''
  // path starts with the space; a folder is the segment after it.
  const rest = n.path.split('/').slice(1)
  return rest.length > 1 ? `${n.space}/${rest[0]}` : n.space
}

/** Walk links both ways from `start`, up to `depth` hops. */
function walk(g: GraphPayload, start: number, depth: number): Set<number> {
  const seen = new Set([start])
  let frontier = new Set([start])
  for (let d = 0; d < depth && frontier.size; d++) {
    const next = new Set<number>()
    for (const [a, b] of g.links) {
      if (frontier.has(a) && !seen.has(b)) { seen.add(b); next.add(b) }
      if (frontier.has(b) && !seen.has(a)) { seen.add(a); next.add(a) }
    }
    frontier = next
  }
  return seen
}

/** Apply the view options to the payload: filters, local walk, labels. */
export function buildView(g: GraphPayload, o: ViewOptions): View {
  let kept: Set<number>
  let center = -1
  if (o.center !== undefined) {
    center = g.notes.findIndex((n) => n.id === o.center)
    if (center < 0) return { nodes: [], links: new Int32Array(0) }
    kept = walk(g, center, o.depth ?? 1)
  } else {
    kept = new Set(g.notes.keys())
  }

  const q = o.search.trim().toLowerCase()
  if (q) {
    for (const i of [...kept]) {
      const n = g.notes[i]!
      if (i !== center && !n.title.toLowerCase().includes(q) && !n.path.toLowerCase().includes(q)) kept.delete(i)
    }
  }

  // Candidate nodes (notes, then ghosts, then tags) and edges between them.
  const nodes: ViewNode[] = []
  const at = new Map<number, number>()
  for (const i of [...kept].sort((a, b) => a - b)) {
    const n = g.notes[i]!
    at.set(i, nodes.length)
    nodes.push({ key: n.id, kind: 'note', id: n.id, label: label(n), group: group(n, o.colorBy), degree: 0 })
  }
  const edges: [number, number][] = []
  for (const [a, b] of g.links) {
    if (kept.has(a) && kept.has(b)) edges.push([at.get(a)!, at.get(b)!])
  }
  const attach = (key: string, kind: 'ghost' | 'tag', id: string, text: string, sources: number[]) => {
    const srcs = sources.filter((s) => kept.has(s))
    if (!srcs.length) return
    const idx = nodes.length
    nodes.push({ key, kind, id, label: text, group: kind, degree: 0 })
    for (const s of srcs) edges.push([at.get(s)!, idx])
  }
  if (o.ghosts) {
    for (const gh of g.ghosts) attach(`ghost:${gh.space}:${gh.target.toLowerCase()}`, 'ghost', gh.target, gh.target, gh.from)
  }
  if (o.tags) {
    for (const t of g.tags) attach(`tag:${t.space}:${t.tag}`, 'tag', t.tag, `#${t.tag}`, t.notes)
  }

  // Ghost and tag nodes only exist with a kept source, and a note with such an
  // edge is never an orphan, so dropping orphan notes cannot strand them.
  const deg = new Array<number>(nodes.length).fill(0)
  for (const [a, b] of edges) { deg[a]!++; deg[b]!++ }
  const centerNode = center >= 0 ? at.get(center) : undefined
  const remap = new Array<number>(nodes.length).fill(-1)
  const out: ViewNode[] = []
  nodes.forEach((n, i) => {
    if (!o.orphans && deg[i] === 0 && i !== centerNode) return
    remap[i] = out.length
    out.push({ ...n, degree: deg[i]! })
  })
  const links = new Int32Array(edges.length * 2)
  edges.forEach(([a, b], i) => { links[2 * i] = remap[a]!; links[2 * i + 1] = remap[b]! })
  return { nodes: out, links }
}

/** Dot radius: grows with sqrt(degree), capped. */
export function nodeRadius(degree: number): number {
  return Math.min(RADIUS_MAX, RADIUS_MIN + 2 * Math.sqrt(degree))
}

/** Neighbour lists per node index. */
export function adjacency(v: View): number[][] {
  const adj: number[][] = v.nodes.map(() => [])
  for (let i = 0; i < v.links.length; i += 2) {
    const a = v.links[i]!, b = v.links[i + 1]!
    adj[a]!.push(b)
    adj[b]!.push(a)
  }
  return adj
}

/** Copy x,y pairs of nodes that survive into the next view; NaN for new ones. */
export function carryPositions(prevKeys: string[], prev: Float32Array, nextKeys: string[]): Float32Array {
  const at = new Map(prevKeys.map((k, i) => [k, i]))
  const out = new Float32Array(nextKeys.length * 2).fill(NaN)
  nextKeys.forEach((k, i) => {
    const j = at.get(k)
    if (j !== undefined) { out[2 * i] = prev[2 * j]!; out[2 * i + 1] = prev[2 * j + 1]! }
  })
  return out
}
