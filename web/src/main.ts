import { api, ApiError } from './api'
import type { Note } from './api'
import { h } from './dom'
import { createEditor, type EditorHandle } from './edit'
import { renderUnresolvedReport } from './links'
import { createNoteView } from './note'
import { createSearch } from './search'
import { createTree } from './tree'
import './app.css'

const app = document.getElementById('app')
if (!app) throw new Error('missing #app')

// --- layout ------------------------------------------------------------

const searchInput = h('input', {
  type: 'search',
  class: 'search-input',
  placeholder: 'Search notes  (/)',
  autocomplete: 'off',
  spellcheck: 'false',
  'aria-label': 'Search notes',
}) as HTMLInputElement

const regexToggle = h('input', { type: 'checkbox', id: 'regex-toggle' }) as HTMLInputElement

const status = h('span', { class: 'status' })

const header = h(
  'header',
  { class: 'topbar' },
  h('a', { class: 'wordmark', href: '/', onClick: (ev) => { ev.preventDefault(); navigate(null) } }, 'YANA/'),
  h(
    'div',
    { class: 'search' },
    searchInput,
    h('label', { class: 'regex-label', for: 'regex-toggle', title: 'Regular expression search over the files (ripgrep)' }, regexToggle, 're'),
  ),
  status,
)

const treeEl = h('nav', { class: 'tree', 'aria-label': 'notes' })
const resultsEl = h('div', { class: 'results', hidden: true })
const unresolvedLink = h(
  'a',
  {
    class: 'unresolved-nav',
    href: '/links',
    onClick: (ev) => {
      ev.preventDefault()
      openLinks()
    },
  },
  'Unresolved links',
)
const sidebar = h('aside', { class: 'sidebar' }, treeEl, resultsEl, unresolvedLink)
const main = h('main', { class: 'content' })

app.append(header, h('div', { class: 'body' }, sidebar, main))

// --- state -------------------------------------------------------------

const noteView = createNoteView(main, { onEdit: (note) => startEdit(note), onOpen: (id) => navigate(id) })
const tree = createTree(treeEl, (id) => navigate(id))
createSearch(searchInput, regexToggle, resultsEl, (id) => navigate(id), (active) => {
  treeEl.hidden = active
  resultsEl.hidden = !active
})

let current: string | null = null
let editor: EditorHandle | null = null

function closeEditor(): void {
  const e = editor
  editor = null
  e?.destroy()
}

function startEdit(note: Note): void {
  closeEditor()
  editor = createEditor(main, note, () => {
    editor = null
    void openNote(note.id)
  })
}

async function loadTree(): Promise<void> {
  try {
    const { spaces } = await api.tree()
    tree.render(spaces)
    tree.select(current)
  } catch (err) {
    treeEl.replaceChildren(h('p', { class: 'error' }, err instanceof ApiError ? err.message : 'Could not load the tree.'))
  }
}

async function openNote(id: string): Promise<void> {
  closeEditor()
  current = id
  tree.select(id)
  try {
    const note = await api.note(id)
    if (current !== id) return
    document.title = `${note.title} — YANA/`
    noteView.show(note)
  } catch (err) {
    if (current !== id) return
    noteView.showError(err instanceof ApiError ? err.message : 'Could not load the note.')
  }
}

function navigate(id: string | null, push = true): void {
  const path = id ? `/n/${id}` : '/'
  if (push && location.pathname !== path) history.pushState(null, '', path)
  if (id) {
    void openNote(id)
  } else {
    closeEditor()
    current = null
    tree.select(null)
    document.title = 'YANA/'
    noteView.showEmpty()
  }
}

function openLinks(push = true): void {
  if (push && location.pathname !== '/links') history.pushState(null, '', '/links')
  closeEditor()
  current = null
  tree.select(null)
  document.title = 'Unresolved links — YANA/'
  renderUnresolvedReport(main, (id) => navigate(id), () => openLinks(false))
}

function route(): void {
  if (location.pathname === '/links') {
    openLinks(false)
    return
  }
  const m = location.pathname.match(/^\/n\/([0-9A-Za-z]{26})$/)
  navigate(m ? (m[1] ?? null) : null, false)
}

window.addEventListener('popstate', route)

document.addEventListener('keydown', (ev) => {
  if (ev.key === '/' && document.activeElement !== searchInput && !isEditable(document.activeElement)) {
    ev.preventDefault()
    searchInput.focus()
    searchInput.select()
  }
})

function isEditable(el: Element | null): boolean {
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || (el as HTMLElement).isContentEditable
}

async function loadStatus(): Promise<void> {
  try {
    const s = await api.status()
    status.textContent = `${s.notes} notes · ${s.version}`
    status.title = s.ready ? `index built ${s.last_scan}` : 'index is still building'
    if (!s.regex_search) {
      regexToggle.disabled = true
      regexToggle.parentElement?.setAttribute('title', 'Regex search needs ripgrep on the server.')
    }
    if (!s.ready) window.setTimeout(() => { void loadStatus(); void loadTree() }, 2000)
  } catch {
    status.textContent = ''
  }
}

// Files can change under us; keep the tree fresh without a websocket.
window.setInterval(() => { void loadTree() }, 30_000)
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') void loadTree()
})

route()
void loadTree()
void loadStatus()
