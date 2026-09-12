// Builds the client into dist/: index.html, assets/app.js, assets/app.css.
// `--watch` rebuilds on change for development against a running server.
import * as esbuild from 'esbuild'
import { cpSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'

const watch = process.argv.includes('--watch')

rmSync('dist', { recursive: true, force: true })
mkdirSync('dist/assets', { recursive: true })
writeFileSync('dist/.gitkeep', '') // keeps the embed target present in a fresh checkout
cpSync('index.html', 'dist/index.html')

const ctx = await esbuild.context({
  entryPoints: { app: 'src/main.ts' },
  bundle: true,
  minify: !watch,
  sourcemap: watch ? 'inline' : false,
  target: ['es2022'],
  format: 'esm',
  outdir: 'dist/assets',
  logLevel: 'info',
})

if (watch) {
  await ctx.watch()
} else {
  await ctx.rebuild()
  await ctx.dispose()
}
