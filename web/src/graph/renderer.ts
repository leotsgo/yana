// Draws the graph: WebGL2 for nodes (instanced circles) and edges (lines),
// a 2D canvas on top for labels. setPositions + draw run every animation
// frame while the layout moves, so they reuse preallocated arrays only.
import type { View } from './model.ts'

/** screen = (world - camera) * k + size / 2, in CSS px. */
export interface Camera { x: number; y: number; k: number }

const EDGE_ALPHA = 0.15
const DIM = 0.15
const LABEL_MIN_PX = 8
const LABEL_MAX = 300
const LABEL_PX = 12

// State per node while a focus is set.
const PLAIN = 0, NEIGHBOUR = 1, FOCUS = 2

const NODE_VS = `#version 300 es
layout(location = 0) in vec2 corner;
layout(location = 1) in vec3 inst; // world x, y, radius (0 = unplaced)
layout(location = 2) in vec4 color;
uniform vec2 cam;
uniform float k;
uniform vec2 size;
uniform float dpr;
out vec2 local;
out float rad;
out vec4 col;
void main() {
  if (inst.z <= 0.0) { gl_Position = vec4(2.0, 2.0, 2.0, 1.0); return; } // unplaced: outside clip space
  rad = max(inst.z * k, 1.0) * dpr; // device px
  local = corner * (rad + 1.0); // one extra px for the anti-aliased rim
  col = color;
  vec2 px = ((inst.xy - cam) * k + size * 0.5) * dpr + local;
  vec2 clip = px / (size * dpr) * 2.0 - 1.0;
  gl_Position = vec4(clip.x, -clip.y, 0.0, 1.0);
}`

const NODE_FS = `#version 300 es
precision highp float; // mediump (fp16 on phones) overflows rad and local at high zoom
in vec2 local;
in float rad;
in vec4 col;
out vec4 frag;
void main() {
  float a = col.a * clamp(rad - length(local) + 0.5, 0.0, 1.0);
  if (a <= 0.0) discard;
  frag = vec4(col.rgb * a, a);
}`

const EDGE_VS = `#version 300 es
layout(location = 0) in vec2 pos;
uniform vec2 cam;
uniform float k;
uniform vec2 size;
void main() {
  vec2 clip = ((pos - cam) * k + size * 0.5) / size * 2.0 - 1.0;
  gl_Position = vec4(clip.x, -clip.y, 0.0, 1.0);
}`

const EDGE_FS = `#version 300 es
precision mediump float;
uniform vec4 color;
out vec4 frag;
void main() { frag = vec4(color.rgb * color.a, color.a); }`

/** Parse a computed CSS colour (`#rgb`, `#rrggbb[aa]`, `rgb(...)`) into 0..1 rgba. */
export function parseColor(s: string): number[] {
  s = s.trim()
  if (s.startsWith('#')) {
    let h = s.slice(1)
    if (h.length <= 4) h = [...h].map((c) => c + c).join('')
    const v = [0, 2, 4, 6].map((i) => parseInt(h.slice(i, i + 2) || 'ff', 16) / 255)
    return v.map((x) => (Number.isNaN(x) ? 1 : x))
  }
  const m = s.match(/[\d.]+%?/g) ?? []
  const n = (i: number, scale: number) => {
    const t = m[i]
    if (t === undefined) return 1
    return t.endsWith('%') ? parseFloat(t) / 100 : parseFloat(t) / scale
  }
  return [n(0, 255), n(1, 255), n(2, 255), n(3, 1)]
}

const withAlpha = (c: number[], a: number) => [c[0]!, c[1]!, c[2]!, (c[3] ?? 1) * a]

/** Graph colours from the theme's custom properties on `el`. */
export function themeColors(el: Element) {
  const css = getComputedStyle(el)
  const get = (name: string) => parseColor(css.getPropertyValue(name) || '#888')
  const ink3 = get('--ink-3'), accent = get('--accent')
  return {
    node: get('--ink-2'),
    edge: withAlpha(ink3, EDGE_ALPHA),
    focus: accent,
    label: get('--ink'),
    ghost: withAlpha(ink3, 0.5),
    tag: withAlpha(accent, 0.6),
  }
}

const PALETTE = [
  [0.86, 0.42, 0.38, 1], [0.89, 0.62, 0.29, 1], [0.80, 0.74, 0.32, 1], [0.45, 0.72, 0.42, 1],
  [0.33, 0.70, 0.70, 1], [0.40, 0.58, 0.86, 1], [0.62, 0.48, 0.84, 1], [0.84, 0.46, 0.70, 1],
]

/** One of 8 fixed hues, picked by a stable hash (FNV-1a) of the name. */
export function groupColor(group: string): number[] {
  let h = 0x811c9dc5
  for (let i = 0; i < group.length; i++) h = Math.imul(h ^ group.charCodeAt(i), 0x01000193)
  return PALETTE[(h >>> 0) % PALETTE.length]!.slice()
}

function program(gl: WebGL2RenderingContext, vs: string, fs: string): WebGLProgram {
  const p = gl.createProgram()!
  for (const [type, src] of [[gl.VERTEX_SHADER, vs], [gl.FRAGMENT_SHADER, fs]] as const) {
    const s = gl.createShader(type)!
    gl.shaderSource(s, src)
    gl.compileShader(s)
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(s) ?? 'shader')
    gl.attachShader(p, s)
    gl.deleteShader(s)
  }
  gl.linkProgram(p)
  if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(p) ?? 'link')
  return p
}

function uniforms(gl: WebGL2RenderingContext, p: WebGLProgram, names: string[]) {
  return Object.fromEntries(names.map((n) => [n, gl.getUniformLocation(p, n)]))
}

export class Renderer {
  camera: Camera = { x: 0, y: 0, k: 1 }

  private canvas: HTMLCanvasElement
  private labels: HTMLCanvasElement
  private gl: WebGL2RenderingContext
  private ctx: CanvasRenderingContext2D
  private nodeProg: WebGLProgram
  private edgeProg: WebGLProgram
  private nodeU: Record<string, WebGLUniformLocation | null>
  private edgeU: Record<string, WebGLUniformLocation | null>
  private nodeVao: WebGLVertexArrayObject
  private edgeVao: WebGLVertexArrayObject
  private cornerBuf: WebGLBuffer
  private instBuf: WebGLBuffer
  private colorBuf: WebGLBuffer
  private edgeBuf: WebGLBuffer

  private w = 0
  private h = 0
  private dpr = 1

  private view: View = { nodes: [], links: new Int32Array(0) }
  private radii: Float32Array = new Float32Array(0)
  private colors: Float32Array = new Float32Array(0)
  private theme = { edge: [0, 0, 0, 0], focus: [0, 0, 0, 1], label: [0, 0, 0, 1] }
  private font = ''
  private labelStyle = ''
  /** x, y, radius per node; radius 0 while the node is unplaced. */
  private inst = new Float32Array(0)
  /** Colours as drawn: `colors` with the focus dimming applied. */
  private drawColors = new Float32Array(0)
  /** Edge endpoints: plain edges from the front, focus edges from the back. */
  private edgeVerts = new Float32Array(0)
  private plainEdges = 0
  private litEdges = 0
  private state = new Uint8Array(0)
  private focus: number | null = null
  private neighbours: number[] = []
  /** Node indices, highest degree first, for picking labels. */
  private byDegree = new Int32Array(0)

  constructor(canvas: HTMLCanvasElement, labels: HTMLCanvasElement) {
    const gl = canvas.getContext('webgl2', { antialias: true, premultipliedAlpha: true })
    const ctx = labels.getContext('2d')
    if (!gl || !ctx) throw new Error('WebGL2 unavailable')
    this.canvas = canvas
    this.labels = labels
    this.gl = gl
    this.ctx = ctx
    this.nodeProg = program(gl, NODE_VS, NODE_FS)
    this.edgeProg = program(gl, EDGE_VS, EDGE_FS)
    this.nodeU = uniforms(gl, this.nodeProg, ['cam', 'k', 'size', 'dpr'])
    this.edgeU = uniforms(gl, this.edgeProg, ['cam', 'k', 'size', 'color'])

    this.cornerBuf = gl.createBuffer()!
    this.instBuf = gl.createBuffer()!
    this.colorBuf = gl.createBuffer()!
    this.edgeBuf = gl.createBuffer()!

    this.nodeVao = gl.createVertexArray()!
    gl.bindVertexArray(this.nodeVao)
    gl.bindBuffer(gl.ARRAY_BUFFER, this.cornerBuf)
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 1, -1, -1, 1, 1, 1]), gl.STATIC_DRAW)
    gl.enableVertexAttribArray(0)
    gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0)
    gl.bindBuffer(gl.ARRAY_BUFFER, this.instBuf)
    gl.enableVertexAttribArray(1)
    gl.vertexAttribPointer(1, 3, gl.FLOAT, false, 0, 0)
    gl.vertexAttribDivisor(1, 1)
    gl.bindBuffer(gl.ARRAY_BUFFER, this.colorBuf)
    gl.enableVertexAttribArray(2)
    gl.vertexAttribPointer(2, 4, gl.FLOAT, false, 0, 0)
    gl.vertexAttribDivisor(2, 1)

    this.edgeVao = gl.createVertexArray()!
    gl.bindVertexArray(this.edgeVao)
    gl.bindBuffer(gl.ARRAY_BUFFER, this.edgeBuf)
    gl.enableVertexAttribArray(0)
    gl.vertexAttribPointer(0, 2, gl.FLOAT, false, 0, 0)
    gl.bindVertexArray(null)

    gl.enable(gl.BLEND)
    gl.blendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA) // shaders output premultiplied alpha
    this.resize()
  }

  /** New nodes and links; positions start unplaced until setPositions. */
  setGraph(view: View, radii: Float32Array, colors: Float32Array): void {
    const n = view.nodes.length
    this.view = view
    this.radii = radii
    this.colors = colors
    const t = themeColors(this.canvas)
    this.theme = { edge: t.edge, focus: t.focus, label: t.label }
    this.font = `${LABEL_PX}px ${getComputedStyle(document.body).fontFamily}`
    const [r, g, b] = t.label.map((x) => Math.round(x * 255))
    this.labelStyle = `rgb(${r}, ${g}, ${b})`

    this.inst = new Float32Array(n * 3)
    this.drawColors = new Float32Array(n * 4)
    this.edgeVerts = new Float32Array(view.links.length * 2)
    this.state = new Uint8Array(n)
    this.byDegree = Int32Array.from(view.nodes.keys()).sort((a, b) => view.nodes[b]!.degree - view.nodes[a]!.degree)
    this.focus = null
    this.neighbours = []

    const gl = this.gl
    gl.bindBuffer(gl.ARRAY_BUFFER, this.instBuf)
    gl.bufferData(gl.ARRAY_BUFFER, this.inst.byteLength, gl.DYNAMIC_DRAW)
    gl.bindBuffer(gl.ARRAY_BUFFER, this.colorBuf)
    gl.bufferData(gl.ARRAY_BUFFER, this.drawColors.byteLength, gl.DYNAMIC_DRAW)
    gl.bindBuffer(gl.ARRAY_BUFFER, this.edgeBuf)
    gl.bufferData(gl.ARRAY_BUFFER, this.edgeVerts.byteLength, gl.DYNAMIC_DRAW)
    this.uploadColors()
    this.uploadPositions()
  }

  /** x,y per node in world units; NaN marks a node not placed yet. */
  setPositions(pos: Float32Array): void {
    const inst = this.inst, radii = this.radii
    const n = Math.min(radii.length, pos.length >> 1)
    for (let i = 0; i < n; i++) {
      const x = pos[2 * i]!, y = pos[2 * i + 1]!
      const placed = !Number.isNaN(x) && !Number.isNaN(y)
      inst[3 * i] = placed ? x : 0
      inst[3 * i + 1] = placed ? y : 0
      inst[3 * i + 2] = placed ? radii[i]! : 0
    }
    this.uploadPositions()
  }

  /** Highlight `focus` and its neighbours; null clears. */
  setFocus(focus: number | null, neighbours: number[]): void {
    this.focus = focus
    this.neighbours = focus === null ? [] : neighbours
    this.state.fill(PLAIN)
    if (focus !== null) {
      for (const j of this.neighbours) this.state[j] = NEIGHBOUR
      this.state[focus] = FOCUS
    }
    this.uploadColors()
    this.uploadPositions() // re-splits plain and focus edges
  }

  /** Match both canvases' backing stores to their CSS size × devicePixelRatio. */
  resize(): void {
    this.dpr = window.devicePixelRatio || 1
    this.w = this.canvas.clientWidth
    this.h = this.canvas.clientHeight
    for (const c of [this.canvas, this.labels]) {
      c.width = Math.max(1, Math.round(this.w * this.dpr))
      c.height = Math.max(1, Math.round(this.h * this.dpr))
    }
    this.gl.viewport(0, 0, this.canvas.width, this.canvas.height)
  }

  draw(): void {
    const gl = this.gl, { x, y, k } = this.camera
    gl.clearColor(0, 0, 0, 0)
    gl.clear(gl.COLOR_BUFFER_BIT)

    const edgeCount = this.edgeVerts.length >> 1
    const dimmed = this.focus !== null
    gl.useProgram(this.edgeProg)
    gl.uniform2f(this.edgeU.cam!, x, y)
    gl.uniform1f(this.edgeU.k!, k)
    gl.uniform2f(this.edgeU.size!, this.w, this.h)
    gl.bindVertexArray(this.edgeVao)
    const e = this.theme.edge
    gl.uniform4f(this.edgeU.color!, e[0]!, e[1]!, e[2]!, e[3]! * (dimmed ? DIM : 1))
    gl.drawArrays(gl.LINES, 0, this.plainEdges * 2)
    const f = this.theme.focus
    gl.uniform4f(this.edgeU.color!, f[0]!, f[1]!, f[2]!, f[3]!)
    gl.drawArrays(gl.LINES, edgeCount - this.litEdges * 2, this.litEdges * 2)

    gl.useProgram(this.nodeProg)
    gl.uniform2f(this.nodeU.cam!, x, y)
    gl.uniform1f(this.nodeU.k!, k)
    gl.uniform2f(this.nodeU.size!, this.w, this.h)
    gl.uniform1f(this.nodeU.dpr!, this.dpr)
    gl.bindVertexArray(this.nodeVao)
    gl.drawArraysInstanced(gl.TRIANGLE_STRIP, 0, 4, this.view.nodes.length)
    gl.bindVertexArray(null)

    this.drawLabels()
  }

  /** Index of the nearest node whose circle holds the CSS-px point, or null. */
  pick(sx: number, sy: number): number | null {
    const inst = this.inst, { x, y, k } = this.camera
    let best: number | null = null, bestD = Infinity
    for (let i = 0; i < this.view.nodes.length; i++) {
      const r = inst[3 * i + 2]!
      if (r === 0) continue
      const dx = (inst[3 * i]! - x) * k + this.w / 2 - sx
      const dy = (inst[3 * i + 1]! - y) * k + this.h / 2 - sy
      const d = dx * dx + dy * dy
      const rp = Math.max(r * k, 1)
      if (d <= rp * rp && d < bestD) { best = i; bestD = d }
    }
    return best
  }

  destroy(): void {
    const gl = this.gl
    for (const b of [this.cornerBuf, this.instBuf, this.colorBuf, this.edgeBuf]) gl.deleteBuffer(b)
    gl.deleteVertexArray(this.nodeVao)
    gl.deleteVertexArray(this.edgeVao)
    gl.deleteProgram(this.nodeProg)
    gl.deleteProgram(this.edgeProg)
    gl.getExtension('WEBGL_lose_context')?.loseContext()
  }

  /** Copy `colors` into drawColors, dimming what is outside the focus. */
  private uploadColors() {
    const c = this.colors, out = this.drawColors, f = this.theme.focus
    const dimmed = this.focus !== null
    for (let i = 0; i < out.length >> 2; i++) {
      const s = this.state[i]
      for (let j = 0; j < 4; j++) out[4 * i + j] = s === FOCUS ? f[j]! : c[4 * i + j] ?? 1
      if (dimmed && s === PLAIN) out[4 * i + 3] = out[4 * i + 3]! * DIM
    }
    const gl = this.gl
    gl.bindBuffer(gl.ARRAY_BUFFER, this.colorBuf)
    gl.bufferSubData(gl.ARRAY_BUFFER, 0, out)
  }

  /** Upload node positions and rebuild the edge vertices from them. */
  private uploadPositions() {
    const inst = this.inst, links = this.view.links, out = this.edgeVerts, state = this.state
    let plain = 0, lit = 0
    const last = out.length - 4
    for (let i = 0; i < links.length; i += 2) {
      const a = links[i]!, b = links[i + 1]!
      if (inst[3 * a + 2] === 0 || inst[3 * b + 2] === 0) continue
      const isLit = state[a] === FOCUS || state[b] === FOCUS
      const o = isLit ? last - 4 * lit++ : 4 * plain++
      out[o] = inst[3 * a]!
      out[o + 1] = inst[3 * a + 1]!
      out[o + 2] = inst[3 * b]!
      out[o + 3] = inst[3 * b + 1]!
    }
    this.plainEdges = plain
    this.litEdges = lit

    const gl = this.gl
    gl.bindBuffer(gl.ARRAY_BUFFER, this.instBuf)
    gl.bufferSubData(gl.ARRAY_BUFFER, 0, inst)
    gl.bindBuffer(gl.ARRAY_BUFFER, this.edgeBuf)
    if (plain) gl.bufferSubData(gl.ARRAY_BUFFER, 0, out, 0, plain * 4)
    if (lit) gl.bufferSubData(gl.ARRAY_BUFFER, (out.length - lit * 4) * 4, out, out.length - lit * 4, lit * 4)
  }

  /** Labels for the focus and its neighbours, then the biggest on-screen nodes. */
  private drawLabels() {
    const ctx = this.ctx, { k } = this.camera
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0)
    ctx.clearRect(0, 0, this.w, this.h)
    ctx.font = this.font
    ctx.fillStyle = this.labelStyle
    ctx.textAlign = 'center'
    ctx.textBaseline = 'top'

    if (this.focus !== null) {
      ctx.globalAlpha = 1
      this.label(this.focus)
      for (const j of this.neighbours) this.label(j)
    }
    ctx.globalAlpha = this.focus !== null ? DIM * 2 : 1
    let shown = 0
    for (let n = 0; n < this.byDegree.length && shown < LABEL_MAX; n++) {
      const i = this.byDegree[n]!
      if (this.radii[i]! * k < LABEL_MIN_PX || this.state[i] !== PLAIN || !this.onScreen(i)) continue
      this.label(i)
      shown++
    }
    ctx.globalAlpha = 1
  }

  private onScreen(i: number): boolean {
    const r = this.inst[3 * i + 2]!
    if (r === 0) return false
    const { x, y, k } = this.camera
    const sx = (this.inst[3 * i]! - x) * k + this.w / 2
    const sy = (this.inst[3 * i + 1]! - y) * k + this.h / 2
    const m = r * k
    return sx > -m && sx < this.w + m && sy > -m && sy < this.h + m
  }

  private label(i: number) {
    if (this.inst[3 * i + 2] === 0) return
    const { x, y, k } = this.camera
    const sx = (this.inst[3 * i]! - x) * k + this.w / 2
    const sy = (this.inst[3 * i + 1]! - y) * k + this.h / 2
    this.ctx.fillText(this.view.nodes[i]!.label, sx, sy + Math.max(this.radii[i]! * k, 1) + 2)
  }
}
