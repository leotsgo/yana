import { api, ApiError } from './api'
import type { Backlink, Note } from './api'
import { h, clear, fmtBytes, fmtDate } from './dom'

export interface NoteView {
  show(note: Note): void
  showEmpty(): void
  showError(message: string): void
}

// The create affordance needs a path for the note it will make: the raw
// target joined onto the linking note's directory, falling back to the
// space root when that escapes the space (a root-style target like
// docs/foo spelled from a nested note).
function createPathFor(note: Note, raw: string): string {
  let rel = raw
  if (!/\.(md|markdown|html|htm)$/i.test(rel)) rel += '.md'
  if (note.space) {
    const joined = join(note.base, rel)
    if (joined.startsWith(note.space + '/')) return joined
    return note.space + '/' + rel
  }
  return join(note.base, rel)
}

export function createNoteView(
  container: HTMLElement,
  hooks: { onEdit?: (note: Note) => void; onOpen?: (id: string) => void },
): NoteView {
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
    if (note.kind === 'md' && hooks.onEdit) {
      row.append(
        h(
          'button',
          { class: 'btn', onClick: () => hooks.onEdit?.(note) },
          'Edit',
        ),
      )
    }
    return row
  }

  // Wikilinks render as spans carrying the raw target. Resolution from the
  // note payload turns them into note links; unresolved ones become the
  // create affordance.
  function wireWikiLinks(body: HTMLElement, note: Note): void {
    const byRaw = new Map(note.links.map((l) => [l.raw_target, l]))
    for (const span of [...body.querySelectorAll<HTMLSpanElement>('span.wikilink[data-target]')]) {
      const raw = span.dataset['target'] ?? ''
      const link = byRaw.get(raw)
      if (link?.resolved && link.to_id) {
        const id = link.to_id
        const a = h(
          'a',
          {
            class: 'wikilink resolved',
            href: `/n/${id}`,
            title: raw,
            onClick: (ev) => {
              ev.preventDefault()
              hooks.onOpen?.(id)
            },
          },
          span.textContent ?? raw,
        )
        span.replaceWith(a)
        continue
      }
      const path = createPathFor(note, raw)
      span.classList.add('unresolved')
      span.title = `Create ${path}`
      span.setAttribute('role', 'link')
      span.setAttribute('tabindex', '0')
      const create = () => {
        span.classList.add('creating')
        api
          .createNote(path)
          .then((res) => hooks.onOpen?.(res.id))
          .catch((err: unknown) => {
            span.classList.remove('creating')
            window.alert(err instanceof ApiError ? err.message : 'Could not create the note.')
          })
      }
      span.addEventListener('click', create)
      span.addEventListener('keydown', (ev) => {
        if (ev.key === 'Enter' || ev.key === ' ') {
          ev.preventDefault()
          create()
        }
      })
    }
  }

  function backlinksPanel(note: Note): HTMLElement {
    const panel = h('section', { class: 'backlinks', 'aria-label': 'linked from' }, h('h2', { class: 'backlinks-title' }, 'Linked from'))
    const list = h('ul', { class: 'backlinks-list' })
    panel.append(list)
    api
      .backlinks(note.id)
      .then(({ backlinks }) => {
        if (backlinks.length === 0) return
        for (const b of backlinks) list.append(backlinkRow(b))
      })
      .catch(() => {
        // A failed fetch leaves the panel empty; the note stays usable.
      })
    return panel
  }

  function backlinkRow(b: Backlink): HTMLElement {
    return h(
      'li',
      { class: 'backlink' },
      h(
        'a',
        {
          class: 'backlink-note',
          href: `/n/${b.note.id}`,
          onClick: (ev) => {
            ev.preventDefault()
            hooks.onOpen?.(b.note.id)
          },
        },
        b.note.title || b.note.path,
      ),
      b.context ? h('p', { class: 'backlink-context' }, b.context) : null,
    )
  }

  // Relative image and link targets in a note resolve against the note's
  // directory. Images come from the asset endpoint; other relative links
  // are left alone.
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
        wireWikiLinks(body, note)
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
      article.append(backlinksPanel(note))
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
