import type { Note } from './api'
import { h, clear, fmtBytes, fmtDate } from './dom'

export interface NoteView {
  show(note: Note): void
  showEmpty(): void
  showError(message: string): void
}

export function createNoteView(container: HTMLElement, onEdit?: (note: Note) => void): NoteView {
  function header(note: Note): HTMLElement {
    const crumbs = h('nav', { class: 'crumbs', 'aria-label': 'path' })
    const parts = note.path.split('/')
    parts.forEach((p, i) => {
      if (i > 0) crumbs.append(h('span', { class: 'crumb-sep' }, '/'))
      crumbs.append(h('span', { class: i === parts.length - 1 ? 'crumb crumb-file' : 'crumb' }, p))
    })
    const meta = h(
      'div',
      { class: 'note-meta' },
      h('span', { title: 'created' }, fmtDate(note.created)),
      h('span', { class: 'meta-sep' }, '·'),
      h('span', { title: 'last modified on disk' }, fmtDate(note.mtime)),
      h('span', { class: 'meta-sep' }, '·'),
      h('span', {}, fmtBytes(note.size)),
      note.tags.length ? h('span', { class: 'meta-sep' }, '·') : null,
      ...note.tags.map((t) => h('span', { class: 'tag' }, '#' + t)),
    )
    return h('header', { class: 'note-header' }, crumbs, meta)
  }

  function actions(note: Note): HTMLElement {
    const row = h('div', { class: 'note-actions' })
    if (note.kind === 'md' && onEdit) {
      row.append(
        h(
          'button',
          { class: 'btn', onClick: () => onEdit(note) },
          'Edit',
        ),
      )
    }
    return row
  }

  // Relative image and link targets in a note resolve against the note's
  // directory. Images come from the asset endpoint; other relative links
  // are left alone until wikilink resolution lands.
  function rewriteRelative(body: HTMLElement, base: string): void {
    for (const img of body.querySelectorAll<HTMLImageElement>('img[src]')) {
      const src = img.getAttribute('src') ?? ''
      if (/^(?:[a-z]+:|\/|#|data:)/i.test(src)) continue
      const joined = join(base, src)
      img.src = '/api/files/' + joined.split('/').map(encodeURIComponent).join('/')
      img.loading = 'lazy'
    }
    for (const a of body.querySelectorAll<HTMLAnchorElement>('a[href]')) {
      const href = a.getAttribute('href') ?? ''
      if (/^(?:https?:|mailto:)/i.test(href)) {
        a.target = '_blank'
        a.rel = 'noopener'
      }
    }
  }

  return {
    show(note) {
      clear(container)
      const article = h('article', { class: 'note' }, header(note), actions(note))
      if (note.kind === 'md' && note.html !== undefined) {
        const body = h('div', { class: 'note-body markdown' })
        body.innerHTML = note.html
        rewriteRelative(body, note.base)
        article.append(body)
      } else if (note.kind === 'html') {
        article.append(
          h(
            'p',
            { class: 'notice' },
            'HTML notes render in a sandboxed frame once that part is built. The source is shown as text for now.',
          ),
          h('pre', { class: 'note-source' }, h('code', {}, note.source ?? '')),
        )
      }
      container.append(article)
      container.scrollTop = 0
    },
    showEmpty() {
      clear(container)
      container.append(
        h(
          'div',
          { class: 'placeholder' },
          h('p', { class: 'wordmark large' }, 'YANA/'),
          h('p', {}, 'Pick a note from the tree, or search.'),
          h('p', { class: 'muted' }, 'Everything you expect. Nothing you don’t.'),
        ),
      )
    },
    showError(message) {
      clear(container)
      container.append(h('div', { class: 'placeholder' }, h('p', { class: 'error' }, message)))
    },
  }
}

function join(base: string, rel: string): string {
  const parts = base ? base.split('/') : []
  for (const seg of rel.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') parts.pop()
    else parts.push(seg)
  }
  return parts.join('/')
}
