// DOM-built pieces of the note page that predate the Preact shell: the
// wikilink wiring and relative-path rewriting applied to rendered
// markdown, and the backlinks and history panels. Each takes a container
// or returns an element; the page mounts them through refs.

import { api, ApiError, join } from './api'
import type { Backlink, HistoryEntry, Note } from './api'
import { assetURL } from './auth'
import { h, clear, fmtDate } from './dom'

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

// Wikilinks render as spans carrying the raw target. Resolution from the
// note payload turns them into note links; unresolved ones become the
// create affordance.
export function wireWikiLinks(body: HTMLElement, note: Note, onOpen: (id: string) => void): void {
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
            onOpen(id)
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
        .then((res) => onOpen(res.id))
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

export function backlinksPanel(note: Note, onOpen: (id: string) => void): HTMLElement {
  const panel = h('section', { class: 'backlinks', 'aria-label': 'linked from' }, h('h2', { class: 'backlinks-title' }, 'Linked from'))
  const list = h('ul', { class: 'backlinks-list' })
  panel.append(list)
  api
    .backlinks(note.id)
    .then(({ backlinks }) => {
      if (backlinks.length === 0) return
      for (const b of backlinks) list.append(backlinkRow(b, onOpen))
    })
    .catch(() => {
      // A failed fetch leaves the panel empty; the note stays usable.
    })
  return panel
}

function backlinkRow(b: Backlink, onOpen: (id: string) => void): HTMLElement {
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
          onOpen(b.note.id)
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
export function historyPanel(note: Note, onOpen: (id: string) => void): HTMLElement {
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
        window.setTimeout(() => onOpen(note.id), 2600)
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
export function rewriteRelative(body: HTMLElement, base: string): void {
  for (const img of body.querySelectorAll<HTMLImageElement>('img[src]')) {
    const src = img.getAttribute('src') ?? ''
    if (/^(?:[a-z]+:|\/|#|data:)/i.test(src)) continue
    img.src = assetURL(join(base, src))
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

