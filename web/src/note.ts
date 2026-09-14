import { api, ApiError } from './api'
import type { Backlink, HistoryEntry, Note } from './api'
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

  // The history panel is the git repository under the notes root: one row
  // per revision of this note, renames followed. Selecting two revisions
  // shows the diff between them; restoring writes the old text back as a
  // live edit.
  function historyPanel(note: Note): HTMLElement {
    const panel = h('section', { class: 'history', 'aria-label': 'history' })
    const list = h('ul', { class: 'history-list' })
    const diffBox = h('pre', { class: 'history-diff', hidden: true })
    const notice = h('p', { class: 'history-notice' })
    const snapshotBtn = h('button', { class: 'btn btn-snapshot' }, 'Snapshot now')
    snapshotBtn.addEventListener('click', () => {
      snapshotBtn.disabled = true
      api
        .gitSnapshot()
        .then(() => load())
        .catch(() => {
          notice.textContent = 'Could not commit now.'
        })
        .finally(() => {
          snapshotBtn.disabled = false
        })
    })
    panel.append(
      h('div', { class: 'history-head' }, h('h2', { class: 'history-title' }, 'History'), snapshotBtn),
      notice,
      list,
      diffBox,
    )

    let entries: HistoryEntry[] = []
    const selected = new Map<string, HTMLLIElement>()

    function load(): void {
      selected.clear()
      diffBox.hidden = true
      notice.textContent = ''
      api
        .history(note.id)
        .then(({ entries: es }) => {
          entries = es
          clear(list)
          if (es.length === 0) {
            panel.classList.add('empty')
            return
          }
          panel.classList.remove('empty')
          for (const e of es) list.append(row(e))
        })
        .catch((err: unknown) => {
          if (err instanceof ApiError && err.status === 501) panel.remove()
        })
    }

    function row(e: HistoryEntry): HTMLLIElement {
      const li = h('li', { class: 'history-row' })
      const pick = h(
        'button',
        { class: 'history-pick', title: 'Compare two revisions', onClick: () => toggle(e, li) },
        h('span', { class: 'history-date' }, fmtDate(e.date)),
        h('span', { class: 'history-author' }, e.name),
        h('span', { class: 'history-subject' }, e.subject),
      )
      const restore = h(
        'button',
        {
          class: 'history-restore',
          title: 'Restore this revision',
          onClick: () => restoreRevision(e),
        },
        'Restore',
      )
      li.append(pick, restore)
      return li
    }

    function toggle(e: HistoryEntry, li: HTMLLIElement): void {
      if (selected.has(e.hash)) {
        selected.delete(e.hash)
        li.classList.remove('selected')
      } else {
        if (selected.size >= 2) {
          const [firstHash, firstLi] = [...selected.entries()][selected.size - 1] as [string, HTMLLIElement]
          selected.delete(firstHash)
          firstLi.classList.remove('selected')
        }
        selected.set(e.hash, li)
        li.classList.add('selected')
      }
      if (selected.size === 2) {
        // entries are newest first; the diff runs from the older to the
        // newer.
        const byIndex = new Map(entries.map((e2, i) => [e2.hash, i]))
        const hashes = [...selected.keys()].sort((x, y) => (byIndex.get(y) ?? 0) - (byIndex.get(x) ?? 0))
        const a = hashes[0]
        const b = hashes[1]
        if (a === undefined || b === undefined) return
        diffBox.hidden = false
        diffBox.textContent = 'Loading diff…'
        api
          .historyDiff(note.id, a, b)
          .then(({ diff }) => {
            diffBox.textContent = diff || 'No changes between these revisions.'
          })
          .catch((err: unknown) => {
            diffBox.textContent = err instanceof ApiError ? err.message : 'Could not load the diff.'
          })
      } else {
        diffBox.hidden = true
      }
    }

    function restoreRevision(e: HistoryEntry): void {
      if (!window.confirm(`Restore the version from ${fmtDate(e.date)}? The current text becomes a new edit in the history.`)) {
        return
      }
      notice.textContent = 'Restoring…'
      api
        .restoreNote(note.id, e.hash, e.path)
        .then(() => {
          notice.textContent = 'Restored. The note reloads in a moment.'
          window.setTimeout(() => hooks.onOpen?.(note.id), 2600)
        })
        .catch((err: unknown) => {
          notice.textContent = err instanceof ApiError ? err.message : 'Could not restore.'
        })
    }

    load()
    return panel
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
      article.append(backlinksPanel(note), historyPanel(note))
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
