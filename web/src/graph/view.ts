// The graph view bundle: the physics worker, the WebGL renderer, the camera
// and mouse/touch input, behind mountGraph. Built apart from the app under
// assets/graph/ and loaded by graph-load.ts the first time the graph opens.
import { adjacency, carryPositions, nodeRadius } from './model'
import type { View, ViewNode } from './model'
import type { Forces, FromWorker, ToWorker } from './protocol'
import { Renderer, groupColor, themeColors } from './renderer'

export interface MountOptions {
  workerURL: string
  view: View
  forces: Forces
  center?: string            // key kept labelled and highlighted (local graph)
  reducedMotion: boolean
  onOpen(node: ViewNode, newTab: boolean): void   // never called for ghosts
}
export interface GraphHandle { update(view: View): void; setForces(f: Forces): void; destroy(): void }

const K_MIN = 0.05
const K_MAX = 8
const FIT_K_MAX = 2 // a fit never zooms a tiny graph in further than this
const FIT_PAD = 40 // CSS px around the fitted graph
const TAP_PX = 6 // movement below this is a tap or click, not a drag
const WHEEL_SPEED = 0.002

const clampK = (k: number) => Math.min(K_MAX, Math.max(K_MIN, k))

interface Gesture {
  id: number
  node: number | null // node under the pointer at the start, or null for the background
  sx: number
  sy: number
  cx: number // camera at the start
  cy: number
  moved: boolean
}

export function mountGraph(host: HTMLElement, o: MountOptions): GraphHandle {
  // Fresh canvases per mount: Renderer.destroy loses the WebGL context.
  const canvas = document.createElement('canvas')
  const labels = document.createElement('canvas')
  for (const c of [canvas, labels]) {
    Object.assign(c.style, { position: 'absolute', inset: '0', width: '100%', height: '100%', display: 'block' })
  }
  canvas.style.touchAction = 'none'
  labels.style.pointerEvents = 'none'
  if (getComputedStyle(host).position === 'static') host.style.position = 'relative'
  host.append(canvas, labels)

  let r: Renderer
  try {
    r = new Renderer(canvas, labels)
  } catch (err) {
    canvas.remove()
    labels.remove()
    throw err
  }
  const worker = new Worker(o.workerURL, { type: 'module' })

  let view: View = { nodes: [], links: new Int32Array(0) }
  let keys: string[] = []
  let pos: Float32Array = new Float32Array(0)
  let adj: number[][] = []
  let centerIdx = -1
  let forces = o.forces
  let focus: number | null = null // hovered (mouse) or tapped (touch) node
  let moved = false // the person panned or zoomed: no more automatic fits
  let fitted = false
  let paused = false
  let frame = 0

  const redraw = () => {
    if (!frame) frame = requestAnimationFrame(() => { frame = 0; r.draw() })
  }

  const send = (m: ToWorker) => {
    worker.postMessage(m)
    // init and forces restart the worker's loop even while paused.
    if (paused && (m.t === 'init' || m.t === 'forces')) worker.postMessage({ t: 'pause' } satisfies ToWorker)
  }

  function applyFocus() {
    const f = focus ?? (centerIdx >= 0 ? centerIdx : null)
    r.setFocus(f, f === null ? [] : adj[f]!)
    redraw()
  }

  /** Hand the renderer the graph with colours read from the current theme. */
  function paint() {
    const t = themeColors(canvas)
    const colors = new Float32Array(view.nodes.length * 4)
    view.nodes.forEach((n, i) => {
      colors.set(n.kind === 'ghost' ? t.ghost : n.kind === 'tag' ? t.tag : n.group ? groupColor(n.group) : t.node, 4 * i)
    })
    r.setGraph(view, Float32Array.from(view.nodes, (n) => nodeRadius(n.degree)), colors)
    r.setPositions(pos) // setGraph resets positions and focus
    applyFocus()
  }

  function init(next: View) {
    const nextKeys = next.nodes.map((n) => n.key)
    const focusKey = focus === null ? undefined : keys[focus]
    pos = carryPositions(keys, pos, nextKeys)
    keys = nextKeys
    view = next
    adj = adjacency(next)
    centerIdx = o.center === undefined ? -1 : nextKeys.indexOf(o.center)
    const f = focusKey === undefined ? -1 : nextKeys.indexOf(focusKey)
    focus = f >= 0 ? f : null
    paint()
    send({ t: 'init', n: next.nodes.length, links: next.links, pos: pos.slice(), forces, animate: !o.reducedMotion })
  }

  /** Frame the placed nodes. */
  function fit() {
    let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity
    for (let i = 0; i + 1 < pos.length; i += 2) {
      const x = pos[i]!, y = pos[i + 1]!
      if (Number.isNaN(x) || Number.isNaN(y)) continue
      x0 = Math.min(x0, x); x1 = Math.max(x1, x)
      y0 = Math.min(y0, y); y1 = Math.max(y1, y)
    }
    if (x0 > x1) return
    const w = Math.max(1, canvas.clientWidth - 2 * FIT_PAD), h = Math.max(1, canvas.clientHeight - 2 * FIT_PAD)
    const k = Math.min(FIT_K_MAX, w / Math.max(1, x1 - x0), h / Math.max(1, y1 - y0))
    r.camera = { x: (x0 + x1) / 2, y: (y0 + y1) / 2, k: clampK(k) }
  }

  worker.onmessage = (e: MessageEvent<FromWorker>) => {
    // ponytail: a tick from before the last init is dropped only when the node
    // count changed; one of the same count shows for a frame. Add a generation
    // to the protocol if that ever shows.
    if (e.data.pos.length !== keys.length * 2) return
    pos = e.data.pos
    r.setPositions(pos)
    // Keep the graph framed as it spreads, until it first settles; not under
    // a held pointer, or a dragged node would slide away from it.
    if (!fitted && !moved && !gesture) fit()
    if (e.data.settled && keys.length) fitted = true
    redraw()
  }

  // --- input ---------------------------------------------------------------

  const pointers = new Map<number, { x: number; y: number }>()
  let gesture: Gesture | null = null
  let pinch: { d: number; k: number; wx: number; wy: number } | null = null

  const local = (ev: { clientX: number; clientY: number }) => {
    const b = canvas.getBoundingClientRect()
    return { x: ev.clientX - b.left, y: ev.clientY - b.top }
  }
  const toWorld = (sx: number, sy: number) => {
    const { x, y, k } = r.camera
    return { x: (sx - canvas.clientWidth / 2) / k + x, y: (sy - canvas.clientHeight / 2) / k + y }
  }
  /** Zoom to `k` keeping world point (wx, wy) under screen point (sx, sy). */
  const zoomAt = (k: number, wx: number, wy: number, sx: number, sy: number) => {
    k = clampK(k)
    r.camera = { x: wx - (sx - canvas.clientWidth / 2) / k, y: wy - (sy - canvas.clientHeight / 2) / k, k }
    moved = true
    redraw()
  }
  const setFocus = (i: number | null) => {
    if (i === focus) return
    focus = i
    applyFocus()
  }
  const startPan = (id: number, p: { x: number; y: number }) => {
    gesture = { id, node: null, sx: p.x, sy: p.y, cx: r.camera.x, cy: r.camera.y, moved: true }
  }

  const onDown = (ev: PointerEvent) => {
    if (ev.button !== 0) {
      if (ev.button === 1) ev.preventDefault() // no autoscroll; auxclick opens
      return
    }
    const p = local(ev)
    canvas.setPointerCapture(ev.pointerId)
    pointers.set(ev.pointerId, p)
    if (pointers.size === 2) {
      if (gesture?.node != null && gesture.moved) send({ t: 'release', i: gesture.node })
      gesture = null
      const [a, b] = [...pointers.values()] as [{ x: number; y: number }, { x: number; y: number }]
      const m = toWorld((a.x + b.x) / 2, (a.y + b.y) / 2)
      pinch = { d: Math.max(1, Math.hypot(a.x - b.x, a.y - b.y)), k: r.camera.k, wx: m.x, wy: m.y }
    } else if (pointers.size === 1) {
      gesture = { id: ev.pointerId, node: r.pick(p.x, p.y), sx: p.x, sy: p.y, cx: r.camera.x, cy: r.camera.y, moved: false }
    }
  }

  const onMove = (ev: PointerEvent) => {
    const p = local(ev)
    if (!pointers.has(ev.pointerId)) {
      if (ev.pointerType === 'mouse') {
        const i = r.pick(p.x, p.y)
        canvas.style.cursor = i === null ? '' : 'pointer'
        setFocus(i)
      }
      return
    }
    pointers.set(ev.pointerId, p)
    if (pinch && pointers.size === 2) {
      const [a, b] = [...pointers.values()] as [{ x: number; y: number }, { x: number; y: number }]
      const d = Math.max(1, Math.hypot(a.x - b.x, a.y - b.y))
      zoomAt(pinch.k * d / pinch.d, pinch.wx, pinch.wy, (a.x + b.x) / 2, (a.y + b.y) / 2)
      return
    }
    const g = gesture
    if (!g || g.id !== ev.pointerId) return
    const dx = p.x - g.sx, dy = p.y - g.sy
    if (!g.moved && Math.hypot(dx, dy) < TAP_PX) return
    g.moved = true
    if (g.node !== null) {
      const w = toWorld(p.x, p.y)
      send({ t: 'drag', i: g.node, x: w.x, y: w.y })
    } else {
      const k = r.camera.k
      r.camera = { x: g.cx - dx / k, y: g.cy - dy / k, k }
      moved = true
      redraw()
    }
  }

  const onUp = (ev: PointerEvent) => {
    if (!pointers.delete(ev.pointerId)) return
    if (pinch) {
      if (pointers.size >= 2) return
      pinch = null
      // The finger left on the glass carries on panning.
      const [rest] = [...pointers.entries()]
      if (rest) startPan(rest[0], rest[1])
      return
    }
    const g = gesture
    if (!g || g.id !== ev.pointerId) return
    gesture = null
    if (g.moved) {
      if (g.node !== null) send({ t: 'release', i: g.node })
      return
    }
    if (ev.type !== 'pointerup') return
    const node = g.node === null ? undefined : view.nodes[g.node]
    if (ev.pointerType === 'mouse') {
      if (node && node.kind !== 'ghost') o.onOpen(node, ev.ctrlKey || ev.metaKey)
      return
    }
    // Touch: the first tap focuses, a second tap on the focus opens.
    if (g.node !== null && g.node === focus && node && node.kind !== 'ghost') o.onOpen(node, false)
    else setFocus(g.node)
  }

  const onAux = (ev: MouseEvent) => {
    if (ev.button !== 1) return
    const p = local(ev)
    const i = r.pick(p.x, p.y)
    const node = i === null ? undefined : view.nodes[i]
    if (node && node.kind !== 'ghost') { ev.preventDefault(); o.onOpen(node, true) }
  }

  const onLeave = (ev: PointerEvent) => {
    if (ev.pointerType === 'mouse' && !gesture) setFocus(null)
  }

  const onWheel = (ev: WheelEvent) => {
    ev.preventDefault()
    const p = local(ev)
    const w = toWorld(p.x, p.y)
    const dy = ev.deltaMode === 1 ? ev.deltaY * 16 : ev.deltaMode === 2 ? ev.deltaY * canvas.clientHeight : ev.deltaY
    zoomAt(r.camera.k * Math.exp(-dy * WHEEL_SPEED), w.x, w.y, p.x, p.y)
  }

  canvas.addEventListener('pointerdown', onDown)
  canvas.addEventListener('pointermove', onMove)
  canvas.addEventListener('pointerup', onUp)
  canvas.addEventListener('pointercancel', onUp)
  canvas.addEventListener('pointerleave', onLeave)
  canvas.addEventListener('auxclick', onAux)
  canvas.addEventListener('wheel', onWheel, { passive: false })

  // --- environment ---------------------------------------------------------

  const resize = new ResizeObserver(() => { r.resize(); redraw() })
  resize.observe(host)

  // The theme lives in <html data-theme> and, for "system", the media query.
  const theme = new MutationObserver(paint)
  theme.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  const dark = window.matchMedia('(prefers-color-scheme: dark)')
  dark.addEventListener('change', paint)

  let inView = true
  const syncPause = () => {
    const p = !inView || document.hidden
    if (p === paused) return
    paused = p
    worker.postMessage({ t: p ? 'pause' : 'resume' } satisfies ToWorker)
  }
  const seen = new IntersectionObserver((entries) => {
    inView = entries[entries.length - 1]!.isIntersecting
    syncPause()
  })
  seen.observe(host)
  document.addEventListener('visibilitychange', syncPause)

  init(o.view)

  return {
    update: init,
    setForces(f) {
      forces = f
      send({ t: 'forces', forces: f })
    },
    destroy() {
      worker.terminate()
      resize.disconnect()
      theme.disconnect()
      seen.disconnect()
      dark.removeEventListener('change', paint)
      document.removeEventListener('visibilitychange', syncPause)
      cancelAnimationFrame(frame)
      r.destroy()
      canvas.remove()
      labels.remove()
    },
  }
}
