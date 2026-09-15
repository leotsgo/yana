// Builds the client into dist/: index.html plus hashed assets/app-*.js and
// assets/app-*.css. The hash in the name is what lets the server send the
// assets with a long immutable cache lifetime and still ship updates.
// `--watch` rebuilds on change for development against a running server.
import * as esbuild from 'esbuild'
import { mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'

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

if (watch) {
  await ctx.watch()
} else {
  await ctx.rebuild()
  await ctx.dispose()
  await exportCtx.rebuild()
  await exportCtx.dispose()
}
