// The Android reader's runtime, bundled by the web toolchain as one
// classic script (build.mjs writes it to dist/android/reader.js) and
// shipped in the app as an APK asset. It is the web reader's other half
// (panels.ts, note.tsx, rich.ts) reshaped for a WebView with no bridge:
// the page config and the first render arrive inside the document, every
// later render arrives through window.yanaReader.setBody, and every tap
// leaves as a yana:// navigation the app intercepts in
// shouldOverrideUrlLoading. Mermaid and KaTeX are drawn with the same
// rich.ts the web client and the exports use, and each lives in its own
// chunk loaded the first time a note needs it: mermaid alone is most of
// the bundle, and every note open parses what the page loads up front.

import { drawMermaid, hasMath, hasMermaid, typeset } from './rich'
import type { KatexLike, MermaidLike } from './rich'
// The stylesheet rides through the same entry: build.mjs emits it as
// reader.css beside this script, KaTeX's stylesheet and fonts included.
import './android-reader.css'

/** Where the app serves note images from (WebViewAssetLoader + the auth interceptor). */
const IMAGE_PATH = '/yana-img/'

interface LinkInfo {
  /** The raw [[target]] as written. */
  raw: string
  /** The note it resolves to, when it does. */
  to: string
  ok: boolean
}

interface Config {
  dark: boolean
  /** The note's directory, for relative images and create paths. */
  base: string
  /** The note's space; create paths fall back to its root. */
  space: string
  /** A viewer's page wires no task boxes. */
  readOnly: boolean
  links: LinkInfo[]
}

function readConfig(): Config {
  const el = document.getElementById('yana-config')
  if (el?.textContent) {
    try {
      const c = JSON.parse(el.textContent) as Partial<Config>
      return { dark: c.dark === true, base: c.base ?? '', space: c.space ?? '', readOnly: c.readOnly === true, links: c.links ?? [] }
    } catch {
      // fall through to the empty config
    }
  }
  return { dark: false, base: '', space: '', readOnly: false, links: [] }
}

// join is api.ts's join, copied so this bundle stays standalone; the
// semantics are the ones panels.ts resolves image and create paths with.
function join(base: string, rel: string): string {
  const parts = base ? base.split('/') : []
  for (const seg of rel.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') parts.pop()
    else parts.push(seg)
  }
  return parts.join('/')
}

// The create affordance's path: the raw target joined onto the linking
// note's directory, falling back to the space root when that escapes the
// space — the web's createPathFor (panels.ts) rule.
function createPathFor(cfg: Config, raw: string): string {
  let rel = raw
  if (!/\.(md|markdown|html|htm)$/i.test(rel)) rel += '.md'
  if (cfg.space) {
    const joined = join(cfg.base, rel)
    if (joined.startsWith(cfg.space + '/')) return joined
    return cfg.space + '/' + rel
  }
  return join(cfg.base, rel)
}

function yanaURL(path: string): string {
  return 'yana://' + path
}

/** Rewrites what the server rendered into what the reader shows. */
function wire(root: HTMLElement, cfg: Config): void {
  // Images resolve against the note's directory and load through the
  // app's image interceptor, which adds the auth header and caches.
  for (const img of root.querySelectorAll<HTMLImageElement>('img[src]')) {
    const src = img.getAttribute('src') ?? ''
    if (/^(?:[a-z]+:|\/|#|data:)/i.test(src)) continue
    const joined = join(cfg.base, src).split('/').map(encodeURIComponent).join('/')
    img.src = IMAGE_PATH + joined
    img.loading = 'lazy'
  }
  // External links open in the system browser; the app decides in
  // shouldOverrideUrlLoading, so they stay ordinary anchors.
  for (const a of root.querySelectorAll<HTMLAnchorElement>('a[href]')) {
    const href = a.getAttribute('href') ?? ''
    if (/^https?:/i.test(href)) {
      a.target = '_blank'
      a.rel = 'noopener'
    }
  }
  wireWikiLinks(root, cfg)
  wireTags(root)
  if (!cfg.readOnly) wireTasks(root)
}

function wireWikiLinks(root: HTMLElement, cfg: Config): void {
  const byRaw = new Map(cfg.links.map((l) => [l.raw, l]))
  for (const span of [...root.querySelectorAll<HTMLSpanElement>('span.wikilink[data-target]')]) {
    const raw = span.dataset['target'] ?? ''
    const link = byRaw.get(raw)
    if (link?.ok && link.to) {
      const a = document.createElement('a')
      a.className = 'wikilink resolved'
      a.href = yanaURL('note/' + encodeURIComponent(link.to))
      a.title = raw
      a.textContent = span.textContent ?? raw
      span.replaceWith(a)
      continue
    }
    const path = createPathFor(cfg, raw)
    const a = document.createElement('a')
    a.className = 'wikilink unresolved'
    a.href = yanaURL('create/' + encodeURIComponent(path))
    a.title = 'Create ' + path
    a.textContent = span.textContent ?? raw
    span.replaceWith(a)
  }
}

function wireTags(root: HTMLElement): void {
  for (const span of root.querySelectorAll<HTMLSpanElement>('span.tag[data-tag]')) {
    const tag = span.dataset['tag'] ?? ''
    if (!tag) continue
    const a = document.createElement('a')
    a.className = 'tag'
    a.href = yanaURL('tag/' + tag)
    a.title = 'Notes tagged #' + tag
    a.textContent = '#' + (span.textContent?.replace(/^#/, '') ?? tag)
    span.replaceWith(a)
  }
}

// The box is already ticked visually the moment it is tapped; the
// navigation tells the app, which ticks it through the server (or the
// offline queue) and the next render confirms it.
function wireTasks(root: HTMLElement): void {
  for (const box of root.querySelectorAll<HTMLInputElement>('input[type="checkbox"][data-line]')) {
    box.disabled = false
    box.addEventListener('change', () => {
      const line = box.dataset['line'] ?? ''
      location.href = yanaURL('task/' + line + '?done=' + (box.checked ? '1' : '0'))
    })
  }
}

let mermaidLoad: Promise<MermaidLike> | undefined
let katexLoad: Promise<KatexLike> | undefined

function loadMermaid(): Promise<MermaidLike> {
  mermaidLoad ??= import('mermaid').then((m) => m.default as unknown as MermaidLike)
  return mermaidLoad
}

function loadKatex(): Promise<KatexLike> {
  katexLoad ??= import('katex').then((m) => m.default as unknown as KatexLike)
  return katexLoad
}

/** A fresh render from the app: replace the note, rewire, redraw. */
function apply(html: string, cfg: Config): void {
  const root = document.getElementById('yana-note')
  if (!root) return
  const y = scrollY
  root.innerHTML = html
  wire(root, cfg)
  scrollTo(0, y)
  if (hasMermaid(root)) void loadMermaid().then((m) => drawMermaid(root, m, cfg.dark))
  if (hasMath(root)) {
    void loadKatex().then((k) => {
      const at = scrollY
      typeset(root, k)
      scrollTo(0, at)
    })
  }
}

const cfg = readConfig()

declare global {
  interface Window {
    yanaReader?: { setBody: (html: string) => void }
  }
}

window.yanaReader = {
  setBody(html: string): void {
    apply(html, cfg)
  },
}

// The first render rides in the document itself (a template's content is
// inert, so nothing in it runs before this script wires it); later ones
// arrive through setBody.
const template = document.getElementById('yana-body') as HTMLTemplateElement | null
const first = template?.innerHTML ?? ''
template?.remove()
if (first !== '') apply(first, cfg)
