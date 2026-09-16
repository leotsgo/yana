// Builds the client into dist/: index.html plus hashed assets/app-*.js and
// assets/app-*.css. The hash in the name is what lets the server send the
// assets with a long immutable cache lifetime and still ship updates.
// `--watch` rebuilds on change for development against a running server.
//
// The same script writes the installable-app files: PNG icons rasterised
// from the slash mark, manifest.webmanifest, and sw.js (from
// sw.template.js) with the hashed bundle names and a version stamp baked
// into its precache list.
import * as esbuild from 'esbuild'
import { createHash } from 'node:crypto'
import { mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { deflateSync } from 'node:zlib'

const watch = process.argv.includes('--watch')

rmSync('dist', { recursive: true, force: true })
mkdirSync('dist/assets', { recursive: true })
writeFileSync('dist/.gitkeep', '') // keeps the embed target present in a fresh checkout

// Rewrites index.html so it points at the hashed bundle names.
const html = {
  name: 'index-html',
  setup(build) {
    build.onEnd((result) => {
      if (!result.metafile) return
      const outputs = Object.keys(result.metafile.outputs)
      const js = outputs.find((o) => /assets\/app-[^/]+\.js$/.test(o))
      const css = outputs.find((o) => /assets\/app-[^/]+\.css$/.test(o))
      if (!js || !css) return
      const page = readFileSync('index.html', 'utf8')
        .replace('/assets/app.js', '/' + js.replace(/^dist\//, ''))
        .replace('/assets/app.css', '/' + css.replace(/^dist\//, ''))
      writeFileSync('dist/index.html', page)
    })
  },
}

const ctx = await esbuild.context({
  entryPoints: { app: 'src/main.tsx' },
  entryNames: '[name]-[hash]',
  bundle: true,
  minify: !watch,
  sourcemap: watch ? 'inline' : false,
  target: ['es2022'],
  format: 'esm',
  jsx: 'automatic',
  jsxImportSource: 'preact',
  outdir: 'dist/assets',
  metafile: true,
  logLevel: 'info',
  define: { __PWA__: JSON.stringify(!watch) },
  plugins: [html],
})

// The export search runtime: minisearch plus the search page's wiring,
// bundled as a classic script under a stable name so the server can
// embed it into static site exports.
const exportCtx = await esbuild.context({
  entryPoints: { 'export-search': 'src/export-search.ts' },
  bundle: true,
  minify: !watch,
  sourcemap: false,
  target: ['es2020'],
  format: 'iife',
  outdir: 'dist',
  logLevel: 'info',
})

// --- icons and manifest ---------------------------------------------------

// A PNG encoder: 8-bit RGBA, no filtering, zlib deflate. The icons are
// flat colour, so this is all they need and the build stays dependency
// free.
let crcTable = null
function crc32(buf) {
  if (!crcTable) {
    crcTable = new Int32Array(256)
    for (let n = 0; n < 256; n++) {
      let c = n
      for (let k = 0; k < 8; k++) c = c & 1 ? (0xedb88320 ^ (c >>> 1)) : c >>> 1
      crcTable[n] = c
    }
  }
  let crc = -1
  for (let i = 0; i < buf.length; i++) crc = (crc >>> 8) ^ crcTable[(crc ^ buf[i]) & 0xff]
  return (crc ^ -1) >>> 0
}

function pngChunk(type, data) {
  const out = Buffer.alloc(8 + data.length + 4)
  out.writeUInt32BE(data.length, 0)
  out.write(type, 4, 'ascii')
  data.copy(out, 8)
  out.writeUInt32BE(crc32(out.subarray(4, 8 + data.length)), 8 + data.length)
  return out
}

function png(size, rgba) {
  const sig = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(size, 0)
  ihdr.writeUInt32BE(size, 4)
  ihdr[8] = 8 // bit depth
  ihdr[9] = 6 // colour type: RGBA
  const stride = size * 4 + 1
  const raw = Buffer.alloc(stride * size)
  for (let y = 0; y < size; y++) {
    raw[y * stride] = 0 // filter: none
    rgba.copy(raw, y * stride + 1, y * size * 4, (y + 1) * size * 4)
  }
  return Buffer.concat([
    sig,
    pngChunk('IHDR', ihdr),
    pngChunk('IDAT', deflateSync(raw, { level: 9 })),
    pngChunk('IEND', Buffer.alloc(0)),
  ])
}

function distToSegment(px, py, ax, ay, bx, by) {
  const dx = bx - ax
  const dy = by - ay
  const len2 = dx * dx + dy * dy
  let t = len2 === 0 ? 0 : ((px - ax) * dx + (py - ay) * dy) / len2
  t = Math.max(0, Math.min(1, t))
  return Math.hypot(px - (ax + t * dx), py - (ay + t * dy))
}

// The slash mark from the favicon: a 32-unit grid, the stroke from
// (21, 4) to (11, 28), width 4, round caps, on the light background.
// `scale` shrinks it about the centre for the maskable safe zone.
function icon(size, scale) {
  const SS = 4 // supersampling for the anti-aliased edge
  const rgba = Buffer.alloc(size * size * 4)
  const ax = 16 + (21 - 16) * scale
  const ay = 16 + (4 - 16) * scale
  const bx = 16 + (11 - 16) * scale
  const by = 16 + (28 - 16) * scale
  const half = (4 * scale) / 2
  const bg = [0xfa, 0xf7, 0xf2]
  const fg = [0xb8, 0x69, 0x1e]
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      let cover = 0
      for (let sy = 0; sy < SS; sy++) {
        for (let sx = 0; sx < SS; sx++) {
          const px = ((x + (sx + 0.5) / SS) / size) * 32
          const py = ((y + (sy + 0.5) / SS) / size) * 32
          if (distToSegment(px, py, ax, ay, bx, by) <= half) cover++
        }
      }
      const a = cover / (SS * SS)
      const i = (y * size + x) * 4
      rgba[i] = Math.round(bg[0] + (fg[0] - bg[0]) * a)
      rgba[i + 1] = Math.round(bg[1] + (fg[1] - bg[1]) * a)
      rgba[i + 2] = Math.round(bg[2] + (fg[2] - bg[2]) * a)
      rgba[i + 3] = 255
    }
  }
  return png(size, rgba)
}

writeFileSync('dist/icon-192.png', icon(192, 1))
writeFileSync('dist/icon-512.png', icon(512, 1))
writeFileSync('dist/icon-192-maskable.png', icon(192, 0.72))
writeFileSync('dist/icon-512-maskable.png', icon(512, 0.72))
writeFileSync('dist/apple-touch-icon.png', icon(180, 1))

const manifest = {
  name: 'YANA/',
  short_name: 'YANA/',
  description: 'Notes as markdown files you already own.',
  start_url: '/',
  scope: '/',
  display: 'standalone',
  background_color: '#faf7f2',
  theme_color: '#f3efe7',
  icons: [
    { src: '/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
    { src: '/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
    { src: '/icon-192-maskable.png', sizes: '192x192', type: 'image/png', purpose: 'maskable' },
    { src: '/icon-512-maskable.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
  ],
  share_target: {
    action: '/share',
    method: 'GET',
    params: { title: 'title', text: 'text', url: 'url' },
  },
}
writeFileSync('dist/manifest.webmanifest', JSON.stringify(manifest, null, 2) + '\n')

// --- service worker -------------------------------------------------------

function writeServiceWorker() {
  const names = readdirSync('dist/assets')
  const js = names.find((f) => /^app-[^/]+\.js$/.test(f))
  const css = names.find((f) => /^app-[^/]+\.css$/.test(f))
  if (!js || !css) throw new Error('hashed bundles not found for the service worker')
  const version = createHash('sha256')
    .update(readFileSync('dist/assets/' + js))
    .update(readFileSync('dist/assets/' + css))
    .update(readFileSync('dist/index.html'))
    .digest('hex')
    .slice(0, 16)
  const precache = [
    '/',
    '/index.html',
    '/assets/' + js,
    '/assets/' + css,
    '/manifest.webmanifest',
    '/icon-192.png',
    '/icon-512.png',
    '/icon-192-maskable.png',
    '/icon-512-maskable.png',
  ]
  const sw = readFileSync('sw.template.js', 'utf8')
    .replace("'__BUILD_VERSION__'", JSON.stringify('v1-' + version))
    .replace('__PRECACHE_MANIFEST__', JSON.stringify(precache, null, 2))
  writeFileSync('dist/sw.js', sw)
}

if (watch) {
  await ctx.watch()
} else {
  await ctx.rebuild()
  await ctx.dispose()
  await exportCtx.rebuild()
  await exportCtx.dispose()
  writeServiceWorker()
}
