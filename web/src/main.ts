import { api, ApiError } from './api'
import type { Note } from './api'
import * as auth from './auth'
import { h, clear } from './dom'
import { createEditor, type EditorHandle } from './edit'
import { renderUnresolvedReport } from './links'
import { createNoteView } from './note'
import { createSearch } from './search'
import { createTree } from './tree'
import './app.css'

const rootNode = document.getElementById('app')
if (!rootNode) throw new Error('missing #app')
const appEl: HTMLElement = rootNode

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

const userEl = h('span', { class: 'user', title: '' })

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
  userEl,
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

let shellBuilt = false
function buildShell(): void {
  if (shellBuilt) return
  shellBuilt = true
  appEl.append(header, h('div', { class: 'body' }, sidebar, main))
}

// --- state ---------------------------------------------------------------

let noteView: ReturnType<typeof createNoteView> | null = null
let tree: ReturnType<typeof createTree> | null = null
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
  if (!tree) return
  try {
    const { spaces } = await api.tree()
    tree.render(spaces)
    tree.select(current)
  } catch (err) {
    treeEl.replaceChildren(h('p', { class: 'error' }, err instanceof ApiError ? err.message : 'Could not load the tree.'))
  }
}

async function openNote(id: string): Promise<void> {
  if (!noteView) return
  closeEditor()
  current = id
  tree?.select(id)
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
    tree?.select(null)
    document.title = 'YANA/'
    noteView?.showEmpty()
  }
}

function openLinks(push = true): void {
  closeEditor()
  current = null
  tree?.select(null)
  document.title = 'Unresolved links — YANA/'
  renderUnresolvedReport(main, (id) => navigate(id), () => openLinks(false))
  if (push && location.pathname !== '/links') history.pushState(null, '', '/links')
}

function route(): void {
  if (!noteView) return
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

function renderUser(): void {
  const u = auth.user()
  if (!u) {
    userEl.replaceChildren()
    return
  }
  const btn = h('button', {
    class: 'btn',
    onClick: () => {
      void auth.logout().then(() => {
        clear(appEl)
        shellBuilt = false
        void boot()
      })
    },
  }, 'Sign out')
  userEl.replaceChildren(h('span', { class: 'user-name' }, u.username), btn)
}

// --- the account gate ---------------------------------------------------

function field(label: string, input: HTMLInputElement): HTMLElement {
  return h('label', { class: 'field' }, h('span', { class: 'field-label' }, label), input)
}

function authScreen(title: string, fields: HTMLElement[], button: string, onSubmit: () => Promise<void>): void {
  clear(appEl)
  const msg = h('p', { class: 'auth-msg', role: 'status' })
  const submit = h('button', { class: 'btn primary', type: 'submit' }, button) as HTMLButtonElement
  const form = h(
    'form',
    {
      class: 'auth-form',
      onSubmit: (ev) => {
        ev.preventDefault()
        submit.disabled = true
        msg.textContent = ''
        onSubmit()
          .catch((err) => {
            msg.textContent = err instanceof auth.AuthError ? err.message : 'Something went wrong.'
          })
          .finally(() => { submit.disabled = false })
      },
    },
    h('h1', { class: 'wordmark large' }, 'YANA/'),
    h('p', { class: 'auth-sub' }, title),
    ...fields,
    msg,
    submit,
  )
  appEl.append(h('div', { class: 'auth-wrap' }, form))
  ;(fields[0]?.querySelector('input') as HTMLInputElement | undefined | null)?.focus()
}

function showSetup(): void {
  const username = h('input', { class: 'auth-input', name: 'username', autocomplete: 'username', required: true }) as HTMLInputElement
  const password = h('input', { class: 'auth-input', name: 'password', type: 'password', autocomplete: 'new-password', required: true }) as HTMLInputElement
  authScreen(
    'This server has no account yet. Create the owner account; there are no default credentials.',
    [field('Username', username), field('Password (8 characters or more)', password)],
    'Create the owner account',
    async () => {
      await auth.setup(username.value, password.value)
      await startApp()
    },
  )
}

function showSignIn(): void {
  const username = h('input', { class: 'auth-input', name: 'username', autocomplete: 'username', required: true }) as HTMLInputElement
  const password = h('input', { class: 'auth-input', name: 'password', type: 'password', autocomplete: 'current-password', required: true }) as HTMLInputElement
  authScreen(
    'Sign in to read and edit your notes.',
    [field('Username', username), field('Password', password)],
    'Sign in',
    async () => {
      await auth.login(username.value, password.value)
      await startApp()
    },
  )
}

async function startApp(): Promise<void> {
  clear(appEl)
  shellBuilt = false
  buildShell()
  noteView = createNoteView(main, { onEdit: (note) => startEdit(note), onOpen: (id) => navigate(id) })
  tree = createTree(treeEl, (id) => navigate(id))
  createSearch(searchInput, regexToggle, resultsEl, (id) => navigate(id), (active) => {
    treeEl.hidden = active
    resultsEl.hidden = !active
  })
  renderUser()
  route()
  void loadTree()
  void loadStatus()
}

// Files can change under us; keep the tree fresh without a websocket.
window.setInterval(() => { void loadTree() }, 30_000)
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') void loadTree()
})

async function boot(): Promise<void> {
  const { setupRequired, open } = await auth.authState()
  if (setupRequired && !open) {
    showSetup()
    return
  }
  if (!open) {
    const ok = await auth.tryRefresh()
    if (!ok || !auth.token()) {
      showSignIn()
      return
    }
  }
  await startApp()
}

void boot()
