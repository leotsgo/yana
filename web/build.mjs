// Builds the client into dist/: index.html plus hashed assets/app-*.js and
// assets/app-*.css. The hash in the name is what lets the server send the
// assets with a long immutable cache lifetime and still ship updates.
// `--watch` rebuilds on change for development against a running server.
//
// The same script writes the installable-app files: the icons copied from
// icons/, manifest.webmanifest, and sw.js (from sw.template.js) with the
// hashed bundle names and a version stamp baked into its precache list.
import * as esbuild from 'esbuild'
import { createHash } from 'node:crypto'
import { copyFileSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'

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

// The icon set lives in icons/ and is copied as-is: the favicon in .ico
// and PNG, the apple-touch icon, and the install icons at 192 and 512
// plus opaque maskable variants (the mark on the background colour,
// inside the safe zone).
for (const name of readdirSync('icons')) copyFileSync('icons/' + name, 'dist/' + name)

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
    '/favicon.ico',
    '/favicon-32x32.png',
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
