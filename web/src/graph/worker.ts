/// <reference lib="webworker" />
// Runs the d3-force layout off the main thread and posts positions each step.
import { forceLink, forceManyBody, forceSimulation, forceX, forceY } from 'd3-force'
import type { ForceLink, ForceManyBody, ForceX, ForceY, Simulation, SimulationLinkDatum, SimulationNodeDatum } from 'd3-force'
import type { Forces, FromWorker, ToWorker } from './protocol.ts'

declare const self: DedicatedWorkerGlobalScope

const STEP_MS = 16
const MAX_STATIC_TICKS = 600
const REHEAT = 0.3

type Node = SimulationNodeDatum
type Link = SimulationLinkDatum<Node>

let sim: Simulation<Node, Link> | null = null
let nodes: Node[] = []
let forces: Forces
let fx: ForceX<Node>, fy: ForceY<Node>, charge: ForceManyBody<Node>, lf: ForceLink<Node, Link>
let linkDefault: (l: Link, i: number, ls: Link[]) => number
let timer: ReturnType<typeof setTimeout> | undefined
let settled = true

function post(done: boolean) {
  const pos = new Float32Array(nodes.length * 2)
  nodes.forEach((d, i) => { pos[2 * i] = d.x ?? NaN; pos[2 * i + 1] = d.y ?? NaN })
  const msg: FromWorker = { t: 'tick', pos, settled: done }
  self.postMessage(msg, [pos.buffer])
}

function step() {
  timer = undefined
  if (!sim) return
  const t = performance.now()
  sim.tick()
  settled = sim.alpha() < sim.alphaMin()
  post(settled)
  // At most one step per STEP_MS; a slow tick (a big vault) waits no more.
  if (!settled) timer = setTimeout(step, Math.max(0, STEP_MS - (performance.now() - t)))
}

/** Start the animated loop, or leave it running: a drag sends a message
 * per pointer move, and restarting the wait on each one starves the steps. */
function run() {
  settled = false
  timer ??= setTimeout(step, STEP_MS)
}

function stop() {
  clearTimeout(timer)
  timer = undefined
}

/** Tick to rest synchronously and post once (reduced motion). */
function settleNow() {
  stop()
  for (let k = 0; k < MAX_STATIC_TICKS && sim!.alpha() >= sim!.alphaMin(); k++) sim!.tick()
  settled = true
  post(true)
}

function applyForces(f: Forces) {
  forces = f
  fx.strength(f.center)
  fy.strength(f.center)
  charge.strength(-f.repel)
  lf.distance(f.distance)
  lf.strength((l, i, ls) => linkDefault(l, i, ls) * f.link) // re-reads degrees, so call after links change
}

let animate = true

self.onmessage = (e: MessageEvent<ToWorker>) => {
  const m = e.data
  switch (m.t) {
    case 'init': {
      stop()
      animate = m.animate
      let carried = false
      nodes = Array.from({ length: m.n }, (_, i) => {
        const x = m.pos[2 * i]!, y = m.pos[2 * i + 1]!
        if (Number.isNaN(x) || Number.isNaN(y)) return {}
        carried = true
        return { x, y }
      })
      const links: Link[] = []
      for (let k = 0; k + 1 < m.links.length; k += 2) links.push({ source: m.links[k]!, target: m.links[k + 1]! })
      fx = forceX<Node>(0); fy = forceY<Node>(0); charge = forceManyBody<Node>()
      lf = forceLink<Node, Link>(links)
      linkDefault = lf.strength() // d3's 1 / min(degree) default
      sim = forceSimulation(nodes).stop().alpha(carried ? REHEAT : 1)
        .force('x', fx).force('y', fy).force('charge', charge).force('link', lf)
      applyForces(m.forces)
      if (animate) run(); else settleNow()
      break
    }
    case 'forces':
      if (!sim) break
      applyForces(m.forces)
      sim.alpha(REHEAT)
      run()
      break
    case 'drag': {
      const d = nodes[m.i]
      if (!sim || !d) break
      d.fx = m.x; d.fy = m.y
      sim.alphaTarget(REHEAT)
      run()
      break
    }
    case 'release': {
      const d = nodes[m.i]
      if (!sim || !d) break
      d.fx = null; d.fy = null
      sim.alphaTarget(0)
      run()
      break
    }
    case 'pause':
      stop()
      break
    case 'resume':
      if (sim && !settled) run()
      break
  }
}
