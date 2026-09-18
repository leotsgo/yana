// Builds the client into dist/: index.html plus hashed assets/app-*.js and
// assets/app-*.css. The hash in the name is what lets the server send the
// assets with a long immutable cache lifetime and still ship updates.
// `--watch` rebuilds on change for development against a running server.
//
// Mermaid and KaTeX are split into their own chunks, loaded the first time
// a note needs one; KaTeX's stylesheet and fonts are built beside them
// under assets/. The exports get the same two libraries as classic
// scripts under dist/export/, which the server embeds and copies into
// static sites and single-file exports.
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

// Keeps only the woff2 face of each KaTeX font: the woff and ttf
// fallbacks would triple what the offline cache and the exports carry.
const woff2Only = {
  name: 'katex-woff2-only',
  setup(build) {
    build.onLoad({ filter: /katex\.min\.css$/ }, (args) => ({
      contents: readFileSync(args.path, 'utf8').replace(/,url\([^)]*\.(?:woff|ttf)\) format\("(?:woff|truetype)"\)/g, ''),
      loader: 'css',
      resolveDir: args.path.replace(/\/[^/]*$/, ''),
    }))
  },
}

// The KaTeX stylesheet is its own hashed file, linked by the client the
// first time a note holds math; the fonts land beside it under assets/.
// It is built first so the app bundle can carry its name.
const katexCSS = await esbuild.build({
  entryPoints: { katex: 'src/katex.css' },
  entryNames: '[name]-[hash]',
  assetNames: '[name]-[hash]',
  bundle: true,
  minify: true,
  loader: { '.woff2': 'file' },
  outdir: 'dist/assets',
  metafile: true,
  logLevel: 'info',
  plugins: [woff2Only],
})
const katexCSSPath = '/' + Object.keys(katexCSS.metafile.outputs).find((o) => /assets\/katex-[^/]+\.css$/.test(o)).replace(/^dist\//, '')

const ctx = await esbuild.context({
  entryPoints: { app: 'src/main.tsx' },
  entryNames: '[name]-[hash]',
  chunkNames: '[name]-[hash]',
  bundle: true,
  splitting: true,
  minify: !watch,
  sourcemap: watch ? 'inline' : false,
  target: ['es2022'],
  format: 'esm',
  jsx: 'automatic',
  jsxImportSource: 'preact',
  outdir: 'dist/assets',
  metafile: true,
  logLevel: 'info',
  define: { __PWA__: JSON.stringify(!watch), __KATEX_CSS__: JSON.stringify(katexCSSPath) },
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

// The exports' diagram and math runtimes: mermaid and KaTeX as classic
// scripts (they work from file://), KaTeX's stylesheet with the fonts
// beside it at stable names, all under dist/export/. A static site copies
// what its pages use; the single-file export inlines it.
const exportRichCtx = await esbuild.context({
  entryPoints: {
    mermaid: 'src/export-mermaid.ts',
    katex: 'src/export-katex.ts',
    'katex-style': 'src/katex.css',
  },
  assetNames: 'katex-fonts/[name]',
  bundle: true,
  minify: true,
  sourcemap: false,
  target: ['es2020'],
  format: 'iife',
  loader: { '.woff2': 'file' },
  outdir: 'dist/export',
  logLevel: 'info',
  plugins: [woff2Only],
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

// Every file under assets/ is precached: the app bundle, the mermaid and
// KaTeX chunks, and the KaTeX stylesheet and fonts, so a note with a
// diagram or an equation renders offline even if none was opened before.
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
    ...names.sort().map((f) => '/assets/' + f),
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
  await exportRichCtx.rebuild()
  await exportRichCtx.dispose()
  writeServiceWorker()
}
