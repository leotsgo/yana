// Links into `_assets/` that are not images become attachment cards in
// the read view: a name, a size, a PDF's page count, a download link,
// and — for a PDF — an inline viewer that expands on the content origin,
// sandboxed like an HTML note (docs/html-notes.md). Other attachments
// carry a download link only; on a phone the whole card opens the
// system viewer instead of expanding anything.

import { api, ApiError, join } from './api'
import type { AttachmentMeta, Note } from './api'
import { assetURL } from './auth'
import { fmtBytes, h } from './dom'

const fileIconPath =
  '<path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/><path d="M10 9H8"/><path d="M16 13H8"/><path d="M16 17H8"/>'

function icon(path: string, size = 20): SVGSVGElement {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
  svg.setAttribute('viewBox', '0 0 24 24')
  svg.setAttribute('width', String(size))
  svg.setAttribute('height', String(size))
  svg.setAttribute('fill', 'none')
  svg.setAttribute('stroke', 'currentColor')
  svg.setAttribute('stroke-width', '1.75')
  svg.setAttribute('stroke-linecap', 'round')
  svg.setAttribute('stroke-linejoin', 'round')
  svg.setAttribute('aria-hidden', 'true')
  svg.innerHTML = path
  return svg
}

/** Finds every link into `_assets/` in the rendered body and turns it
 * into a card. Images are `<img>`, not `<a>`, so this only ever touches
 * plain file links. */
export function wireAttachments(host: HTMLElement, note: Note, phone: boolean): void {
  for (const a of [...host.querySelectorAll<HTMLAnchorElement>('a[href]')]) {
    const href = a.getAttribute('href') ?? ''
    if (/^(?:[a-z][a-z0-9+.-]*:|\/|#)/i.test(href)) continue
    const clean = href.split(/[?#]/)[0] ?? ''
    if (!clean) continue
    const path = join(note.base, clean)
    if (!/(^|\/)_assets\//.test(path)) continue
    buildCard(a, path, note, phone)
  }
}

/** True when a is the only meaningful content of parent (whitespace text
 * nodes aside) — the common case for an uploaded file on its own line. */
function isSoleContent(parent: Element, a: Element): boolean {
  for (const node of parent.childNodes) {
    if (node === a) continue
    if (node.nodeType === Node.TEXT_NODE && (node.textContent ?? '').trim() === '') continue
    return false
  }
  return true
}

function buildCard(a: HTMLAnchorElement, path: string, note: Note, phone: boolean): void {
  const name = (a.textContent ?? '').trim() || path.slice(path.lastIndexOf('/') + 1)
  const card = h('div', { class: 'attach-card' })
  const iconWrap = h('div', { class: 'attach-card-icon' })
  iconWrap.append(icon(fileIconPath))
  const main = h(
    'div',
    { class: 'attach-card-main' },
    h('div', { class: 'attach-card-name' }, name),
    h('div', { class: 'attach-card-meta muted' }, 'Loading…'),
  )
  const actions = h('div', { class: 'attach-card-actions' })
  card.append(iconWrap, main, actions)

  const parent = a.parentElement
  if (parent && isSoleContent(parent, a) && /^(P|LI|DIV)$/.test(parent.tagName)) {
    parent.replaceWith(card)
  } else {
    a.replaceWith(card)
  }

  api
    .attachment(path)
    .then((meta) => renderCard(card, main, actions, meta, note, phone))
    .catch(() => {
      main.replaceChildren(h('div', { class: 'attach-card-name' }, name), h('div', { class: 'attach-card-meta muted' }, 'Not indexed yet.'))
      actions.replaceChildren(downloadLink(path, name))
    })
}

function downloadLink(path: string, name: string): HTMLAnchorElement {
  return h('a', { class: 'btn small attach-card-download', href: assetURL(path), download: name }, 'Download')
}

function renderCard(card: HTMLElement, main: HTMLElement, actions: HTMLElement, meta: AttachmentMeta, note: Note, phone: boolean): void {
  const bits = [fmtBytes(meta.size)]
  if (meta.pages) bits.push(`${meta.pages} ${meta.pages === 1 ? 'page' : 'pages'}`)
  main.replaceChildren(h('div', { class: 'attach-card-name' }, meta.name), h('div', { class: 'attach-card-meta' }, bits.join(' · ')))
  actions.replaceChildren()
  const download = downloadLink(meta.path, meta.name)
  if (meta.kind === 'pdf') {
    const open = h('button', { class: 'btn small', type: 'button' }, 'Open')
    open.addEventListener('click', () => {
      if (phone) {
        window.location.href = assetURL(meta.path)
        return
      }
      toggleViewer(card, note, meta, open)
    })
    actions.append(open, download)
    return
  }
  if (phone) {
    card.classList.add('attach-card-tappable')
    card.addEventListener('click', (ev) => {
      if ((ev.target as HTMLElement).closest('a, button')) return
      window.location.href = assetURL(meta.path)
    })
  }
  actions.append(download)
}

function toggleViewer(card: HTMLElement, note: Note, meta: AttachmentMeta, btn: HTMLButtonElement): void {
  const existing = card.querySelector<HTMLElement>('.attach-card-viewer')
  if (existing) {
    existing.remove()
    btn.textContent = 'Open'
    return
  }
  btn.disabled = true
  btn.textContent = 'Loading…'
  api
    .assetViewURL(note.id, meta.path)
    .then(({ url }) => {
      btn.disabled = false
      btn.textContent = 'Close'
      const frame = h('iframe', {
        class: 'attach-frame',
        // allow-scripts only: the frame's origin cannot reach this app's
        // DOM, storage or API, the same sandbox an HTML note renders in.
        sandbox: 'allow-scripts',
        referrerpolicy: 'no-referrer',
        src: url,
        title: meta.name,
      })
      const viewer = h('div', { class: 'attach-card-viewer' }, frame)
      card.append(viewer)
    })
    .catch((err: unknown) => {
      btn.disabled = false
      btn.textContent = 'Open'
      window.alert(err instanceof ApiError ? err.message : 'Could not open the file.')
    })
}
