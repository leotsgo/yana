// Loads mermaid and KaTeX on demand and draws what a render holds. Both
// libraries are large, so they live in their own chunks and are fetched
// the first time a note needs one; the service worker keeps them for
// offline use once seen. KaTeX's stylesheet (with its fonts) is a
// separate file the build names, linked the first time math appears.
import { drawMermaid, hasMath, hasMermaid, typeset, undrawMermaid } from './rich'
import type { KatexLike, MermaidLike } from './rich'

// The hashed path of the KaTeX stylesheet, filled in by build.mjs.
declare const __KATEX_CSS__: string

let mermaidLoad: Promise<MermaidLike> | undefined
let katexLoad: Promise<KatexLike> | undefined
let watching = false

function isDark(): boolean {
  return document.documentElement.dataset['theme'] === 'dark'
}

function loadMermaid(): Promise<MermaidLike> {
  mermaidLoad ??= import('mermaid').then((m) => m.default as unknown as MermaidLike)
  return mermaidLoad
}

function loadKatex(): Promise<KatexLike> {
  katexLoad ??= Promise.all([import('katex'), katexStyles()]).then(([m]) => m.default as unknown as KatexLike)
  return katexLoad
}

// Links the stylesheet and resolves once it has loaded, so the first
// typeset does not flash unstyled; a stylesheet that never fires load
// (blocked, offline without the cache) stops the wait after a moment.
function katexStyles(): Promise<void> {
  return new Promise((resolve) => {
    const link = document.createElement('link')
    link.rel = 'stylesheet'
    link.href = __KATEX_CSS__
    const done = () => resolve()
    link.addEventListener('load', done)
    link.addEventListener('error', done)
    window.setTimeout(done, 3000)
    document.head.append(link)
  })
}

/**
 * Draws the diagrams and typesets the math in a freshly rendered note.
 * Safe to call on every render: it only loads what the render needs and
 * skips what is already drawn.
 */
export function renderRich(root: HTMLElement): void {
  watchTheme()
  if (hasMermaid(root)) {
    loadMermaid()
      .then((mermaid) => drawMermaid(root, mermaid, isDark()))
      .catch(() => {
        // The chunk did not load (offline, first time); the fence keeps
        // showing its source, which is still readable.
      })
  }
  if (hasMath(root)) {
    loadKatex()
      .then((katex) => typeset(root, katex))
      .catch(() => {})
  }
}

// A theme change redraws every diagram on the page with the new palette.
function watchTheme(): void {
  if (watching) return
  watching = true
  let last = isDark()
  new MutationObserver(() => {
    const dark = isDark()
    if (dark === last) return
    last = dark
    if (!mermaidLoad || !document.querySelector('figure.mermaid-figure')) return
    undrawMermaid(document.body)
    void loadMermaid().then((mermaid) => drawMermaid(document.body, mermaid, dark))
  }).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
}
