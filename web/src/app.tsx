// The app shell: top bar, sidebar (tree or search results), the open note
// or report, and the overlays (command palette, quick switcher, prompts).
// Global hotkeys live here too; see hotkeys.ts for the bindings.

import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks'

import { api, ApiError, baseOf, dirOf, saveBlob } from './api'
import type { Note, SpaceTree, Status } from './api'
import * as auth from './auth'
import { Confirm } from './confirm'
import type { ConfirmSpec } from './confirm'
import { isEditable, keys, label, matches } from './hotkeys'
import { renderUnresolvedReport } from './links'
import { NotePage } from './note'
import { Palette } from './palette'
import type { PaletteItem, PaletteSpec } from './palette'
import { SearchResults } from './search'
import { TrashPage } from './trash'
import { Tree, flatten } from './tree'

type Route = { kind: 'home' } | { kind: 'note'; id: string } | { kind: 'links' } | { kind: 'trash' }

function parseRoute(): Route {
  if (location.pathname === '/links') return { kind: 'links' }
  if (location.pathname === '/trash') return { kind: 'trash' }
  const m = location.pathname.match(/^\/n\/([0-9A-Za-z]{26})$/)
  return m && m[1] ? { kind: 'note', id: m[1] } : { kind: 'home' }
}

const PREVIEW_KEY = 'yana.preview'

export function App({ onSignOut }: { onSignOut: () => void }) {
  const [route, setRoute] = useState<Route>(parseRoute)
  const [spaces, setSpaces] = useState<SpaceTree[]>([])
  const [treeError, setTreeError] = useState<string | null>(null)
  const [status, setStatus] = useState<Status | null>(null)
  const [query, setQuery] = useState('')
  const [regex, setRegex] = useState(false)
  const [palette, setPalette] = useState<PaletteSpec | null>(null)
  const [confirmSpec, setConfirmSpec] = useState<ConfirmSpec | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const [preview, setPreview] = useState(() => localStorage.getItem(PREVIEW_KEY) !== '0')
  const [fresh, setFresh] = useState<string | null>(null)
  const [rev, setRev] = useState(0) // bumps to reopen the current note after a move
  const searchInput = useRef<HTMLInputElement>(null)
  const current = useRef<Note | null>(null)
  const user = auth.user()

  // --- navigation --------------------------------------------------------

  const navigate = useCallback((id: string | null, push = true) => {
    const path = id ? `/n/${id}` : '/'
    if (push && location.pathname !== path) history.pushState(null, '', path)
    setRoute(id ? { kind: 'note', id } : { kind: 'home' })
    if (!id) document.title = 'YANA/'
  }, [])

  const openLinks = useCallback((push = true) => {
    if (push && location.pathname !== '/links') history.pushState(null, '', '/links')
    setRoute({ kind: 'links' })
    document.title = 'Unresolved links — YANA/'
  }, [])

  const openTrash = useCallback((push = true) => {
    if (push && location.pathname !== '/trash') history.pushState(null, '', '/trash')
    setRoute({ kind: 'trash' })
    document.title = 'Trash — YANA/'
  }, [])

  useEffect(() => {
    const onPop = () => setRoute(parseRoute())
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  // --- data --------------------------------------------------------------

  const loadTree = useCallback(async () => {
    try {
      const { spaces } = await api.tree()
      setSpaces(spaces)
      setTreeError(null)
    } catch (err) {
      setTreeError(err instanceof ApiError ? err.message : 'Could not load the tree.')
    }
  }, [])

  const loadStatus = useCallback(async () => {
    try {
      const s = await api.status()
      setStatus(s)
      if (!s.ready) window.setTimeout(() => { void loadStatus(); void loadTree() }, 2000)
    } catch {
      setStatus(null)
    }
  }, [loadTree])

  useEffect(() => {
    void loadTree()
    void loadStatus()
    // Files can change under us; keep the tree fresh without a websocket.
    const t = window.setInterval(() => { void loadTree() }, 30_000)
    const onVis = () => { if (document.visibilityState === 'visible') void loadTree() }
    document.addEventListener('visibilitychange', onVis)
    return () => {
      window.clearInterval(t)
      document.removeEventListener('visibilitychange', onVis)
    }
  }, [loadTree, loadStatus])

  const say = useCallback((msg: string) => setToast(msg), [])
  useEffect(() => {
    if (!toast) return
    const t = window.setTimeout(() => setToast(null), 4500)
    return () => window.clearTimeout(t)
  }, [toast])

  // --- actions -----------------------------------------------------------

  const notes = useMemo(() => flatten(spaces), [spaces])

  /** The space new things go into: the open note's, else the first one. */
  function defaultSpace(): string {
    if (current.current) return current.current.space
    return spaces[0]?.name ?? ''
  }

  const createNote = useCallback(
    async (path: string) => {
      let p = path.trim().replace(/^\/+/, '')
      if (!/\.(md|markdown|html?)$/i.test(p)) p += '.md'
      const title = baseOf(p).replace(/\.(md|markdown|html?)$/i, '')
      try {
        const res = await api.createNote(p, `# ${title}\n\n`)
        setFresh(res.id)
        navigate(res.id)
        void loadTree()
      } catch (err) {
        say(err instanceof ApiError ? err.message : 'Could not create the note.')
      }
    },
    [navigate, loadTree, say],
  )

  function newNotePrompt(space?: string): void {
    const sp = space ?? defaultSpace()
    const dir = current.current && (space === undefined || space === current.current.space) ? dirOf(current.current.path) : sp
    const initial = dir ? dir + '/' : ''
    setPalette({
      mode: 'prompt',
      placeholder: 'Path for the new note',
      initial,
      hint: 'A path inside the tree, like projects/kiln.md. Folders that do not exist yet are created. Enter to create and start writing.',
      onSubmit: (v) => void createNote(v),
    })
  }

  const openDaily = useCallback(async () => {
    const d = new Date()
    const date = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
    try {
      const res = await api.daily(defaultSpace(), date)
      if (res.created) {
        setFresh(res.id)
        void loadTree()
      }
      navigate(res.id)
    } catch (err) {
      say(err instanceof ApiError ? err.message : 'Could not open the daily note.')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navigate, loadTree, say, spaces])

  const moveNote = useCallback(
    async (id: string, from: string, to: string) => {
      try {
        const res = await api.moveNote(id, to)
        current.current = null
        void loadTree()
        const n = res.rewritten
        say(
          n > 0
            ? `Moved to ${res.note.path}. ${n} link${n === 1 ? '' : 's'} updated${res.broken ? `, ${res.broken} left unresolved` : ''}.`
            : `Moved to ${res.note.path}.`,
        )
        if (route.kind === 'note' && route.id === id) setRev((r) => r + 1)
      } catch (err) {
        say(err instanceof ApiError ? err.message : `Could not move ${from}.`)
      }
    },
    [loadTree, say, route],
  )

  function renamePrompt(): void {
    const n = current.current
    if (!n) return
    const base = baseOf(n.path)
    const dot = base.lastIndexOf('.')
    const start = n.path.length - base.length
    setPalette({
      mode: 'prompt',
      placeholder: 'New path',
      initial: n.path,
      select: [start, dot > 0 ? start + dot : n.path.length],
      hint: 'Moving the file rewrites wikilinks that point at it.',
      onSubmit: (v) => void moveNote(n.id, n.path, v),
    })
  }

  // Deleting shows the note's inbound links first: whoever points at it
  // is about to hold an unresolved link until it comes back.
  const deleteNotePrompt = useCallback(() => {
    const n = current.current
    if (!n) return
    const ask = (rows: Array<{ label: string; detail?: string }>) => {
      setConfirmSpec({
        title:
          rows.length > 0
            ? `Delete ${n.title}? ${rows.length} ${rows.length === 1 ? 'note links' : 'notes link'} here.`
            : `Delete ${n.title}?`,
        body: 'The file moves to the trash and its links go unresolved. Restore it from the trash any time in the next 30 days.',
        rows,
        confirmLabel: 'Delete',
        danger: true,
        onConfirm: () => {
          api
            .deleteNote(n.id)
            .then(() => {
              if (route.kind === 'note' && route.id === n.id) navigate(null)
              void loadTree()
              say(`Deleted ${n.title}. It is in the trash.`)
            })
            .catch((err: unknown) => {
              say(err instanceof ApiError ? err.message : `Could not delete ${n.title}.`)
            })
        },
      })
    }
    api
      .backlinks(n.id)
      .then(({ backlinks }) =>
        ask(backlinks.map((b) => ({ label: b.note.title || b.note.path, detail: b.note.path }))),
      )
      .catch(() => ask([]))
  }, [navigate, loadTree, say, route])

  const togglePreview = useCallback(() => {
    setPreview((p) => {
      localStorage.setItem(PREVIEW_KEY, p ? '0' : '1')
      return !p
    })
  }, [])

  function focusSearch(): void {
    const el = searchInput.current
    if (!el) return
    el.focus()
    el.select()
  }

  function openSwitcher(): void {
    setPalette({
      mode: 'list',
      placeholder: 'Open a note',
      items: notes.map((n) => ({ id: n.id, label: n.title, detail: n.path, run: () => navigate(n.id) })),
      onCreate: (q) => {
        const sp = defaultSpace()
        void createNote(q.includes('/') || !sp ? q : `${sp}/${q}`)
      },
    })
  }

  // Exports are downloads the token has to travel with, so they run
  // through the API wrapper rather than a plain navigation.
  function exportNote(): void {
    const n = current.current
    if (!n) return
    api
      .exportNote(n.id)
      .then(({ blob, name }) => {
        saveBlob(blob, name)
        say(`Exported ${n.title} as a single HTML file.`)
      })
      .catch((err: unknown) => {
        say(err instanceof ApiError ? err.message : 'Could not export the note.')
      })
  }

  function exportSpace(mode: 'site' | 'zip'): void {
    const space = defaultSpace()
    const run = mode === 'site' ? api.exportSite(space) : api.exportTree(space)
    const what = space === '' ? 'the root' : space
    run
      .then(({ blob, name }) => {
        saveBlob(blob, name)
        say(mode === 'site' ? `Exported ${what} as a static site.` : `Exported ${what} as a zip.`)
      })
      .catch((err: unknown) => {
        say(err instanceof ApiError ? err.message : 'Could not export the space.')
      })
  }

  function openPalette(): void {
    const items: PaletteItem[] = [
      { id: 'new', label: 'New note', hint: label(keys.newNote), run: () => newNotePrompt() },
      { id: 'daily', label: 'Daily note', detail: 'today', hint: label(keys.daily), run: () => void openDaily() },
      { id: 'open', label: 'Open a note', hint: label(keys.switcher), run: openSwitcher },
      { id: 'search', label: 'Search notes', hint: label(keys.search), run: focusSearch },
      { id: 'preview', label: preview ? 'Hide the preview' : 'Show the preview', hint: label(keys.preview), run: togglePreview },
    ]
    if (current.current) {
      items.push({ id: 'rename', label: 'Rename or move this note', detail: current.current.path, run: renamePrompt })
      items.push({
        id: 'export-note',
        label: 'Export this note as HTML',
        detail: 'one self-contained file',
        run: exportNote,
      })
      items.push({ id: 'delete', label: 'Delete this note', detail: 'moves it to the trash', run: deleteNotePrompt })
    }
    if (spaces.length > 0) {
      const space = defaultSpace() || 'the root'
      items.push({
        id: 'export-site',
        label: `Export ${space} as a site`,
        detail: 'offline HTML with search',
        run: () => exportSpace('site'),
      })
      items.push({
        id: 'export-zip',
        label: `Export ${space} as a zip`,
        detail: 'markdown and assets, unchanged',
        run: () => exportSpace('zip'),
      })
    }
    items.push({ id: 'links', label: 'Unresolved links', detail: 'every wikilink that points nowhere', run: () => openLinks() })
    items.push({ id: 'trash', label: 'Trash', detail: 'deleted notes, kept for 30 days', run: () => openTrash() })
    if (status?.git?.available) {
      items.push({
        id: 'snapshot',
        label: 'Snapshot history now',
        detail: 'commit the tree',
        run: () => {
          api
            .gitSnapshot()
            .then((r) => say(`Committed. ${r.commits} commits in the history.`))
            .catch(() => say('Could not commit now.'))
        },
      })
    }
    if (user) items.push({ id: 'signout', label: 'Sign out', detail: user.username, run: () => void auth.logout().then(onSignOut) })
    setPalette({ mode: 'list', placeholder: 'Type a command', items })
  }

  // --- hotkeys -----------------------------------------------------------

  const actions = useRef({ openPalette, openSwitcher, newNotePrompt, openDaily, focusSearch, togglePreview })
  actions.current = { openPalette, openSwitcher, newNotePrompt, openDaily, focusSearch, togglePreview }

  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      const a = actions.current
      if (ev.key === 'Escape' && palette) {
        setPalette(null)
        return
      }
      let handled = true
      if (matches(ev, keys.palette)) a.openPalette()
      else if (matches(ev, keys.switcher)) a.openSwitcher()
      else if (matches(ev, keys.newNote)) a.newNotePrompt()
      else if (matches(ev, keys.daily)) void a.openDaily()
      else if (matches(ev, keys.search)) a.focusSearch()
      else if (matches(ev, keys.preview)) a.togglePreview()
      else if (ev.key === '/' && !ev.ctrlKey && !ev.metaKey && !ev.altKey && !isEditable(document.activeElement)) a.focusSearch()
      else handled = false
      if (handled) {
        ev.preventDefault()
        ev.stopPropagation()
      }
    }
    // Capture phase, so the editor's own keymap does not see these first.
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [palette])

  // --- render ------------------------------------------------------------

  const onNote = useCallback((n: Note | null) => { current.current = n }, [])
  const searching = query.trim() !== ''
  const selected = route.kind === 'note' ? route.id : null

  return (
    <>
      <header class="topbar">
        <a class="wordmark" href="/" onClick={(ev) => { ev.preventDefault(); navigate(null) }}>
          YANA/
        </a>
        <div class="search">
          <input
            ref={searchInput}
            type="search"
            class="search-input"
            placeholder="Search notes  (/)"
            autocomplete="off"
            spellcheck={false}
            aria-label="Search notes"
            value={query}
            onInput={(ev) => setQuery((ev.target as HTMLInputElement).value)}
            onKeyDown={(ev) => {
              if (ev.key === 'Escape') {
                setQuery('')
                ;(ev.target as HTMLInputElement).blur()
              }
            }}
          />
          <label
            class="regex-label"
            title={status && !status.regex_search ? 'Regex search needs ripgrep on the server.' : 'Regular expression search over the files (ripgrep)'}
          >
            <input type="checkbox" checked={regex} disabled={status ? !status.regex_search : false} onChange={(ev) => setRegex((ev.target as HTMLInputElement).checked)} />
            re
          </label>
        </div>
        <button type="button" class="btn btn-new" title={`New note (${label(keys.newNote)})`} onClick={() => newNotePrompt()}>
          New
        </button>
        <button type="button" class="btn" title={`Command palette (${label(keys.palette)})`} onClick={openPalette}>
          …
        </button>
        <span class="status" title={status ? (status.ready ? `index built ${status.last_scan}` : 'index is still building') : ''}>
          {status ? `${status.notes} notes · ${status.version}` : ''}
        </span>
        {user && (
          <span class="user">
            <span class="user-name">{user.username}</span>
          </span>
        )}
      </header>
      <div class="body">
        <aside class="sidebar">
          {searching ? (
            <SearchResults query={query} regex={regex} onOpen={navigate} />
          ) : (
            <nav class="tree" aria-label="notes">
              {treeError ? (
              <p class="error">{treeError}</p>
            ) : (
              <Tree
                spaces={spaces}
                selected={selected}
                onOpen={navigate}
                onMove={(id, from, dir) => void moveNote(id, from, dir ? `${dir}/${baseOf(from)}` : baseOf(from))}
                onNew={newNotePrompt}
              />
            )}
            </nav>
          )}
          {!searching && (
            <div class="sidebar-nav">
              <a class="unresolved-nav" href="/links" onClick={(ev) => { ev.preventDefault(); openLinks() }}>
                Unresolved links
              </a>
              <a class="unresolved-nav" href="/trash" onClick={(ev) => { ev.preventDefault(); openTrash() }}>
                Trash
              </a>
            </div>
          )}
        </aside>
        <main class="content">
          {route.kind === 'note' && (
            <NotePage
              key={`${route.id}:${rev}`}
              id={route.id}
              preview={preview}
              onTogglePreview={togglePreview}
              onOpen={navigate}
              onNote={onNote}
              onToast={say}
              fresh={fresh === route.id}
              onDelete={deleteNotePrompt}
            />
          )}
          {route.kind === 'links' && <LinksReport onOpen={navigate} />}
          {route.kind === 'trash' && (
            <TrashPage onOpen={navigate} onToast={say} confirm={setConfirmSpec} onChanged={() => void loadTree()} />
          )}
          {route.kind === 'home' && (
            <div class="placeholder">
              <p class="wordmark large">YANA/</p>
              <p>Pick a note from the tree, or start one.</p>
              <ul class="hints">
                <li>
                  <kbd>{label(keys.newNote)}</kbd> new note
                </li>
                <li>
                  <kbd>{label(keys.daily)}</kbd> today's note
                </li>
                <li>
                  <kbd>{label(keys.switcher)}</kbd> open a note by name
                </li>
                <li>
                  <kbd>{label(keys.palette)}</kbd> everything else
                </li>
              </ul>
              <p class="muted">Everything you expect. Nothing you don't.</p>
            </div>
          )}
        </main>
      </div>
      {palette && <Palette spec={palette} onClose={() => setPalette(null)} />}
      {confirmSpec && <Confirm spec={confirmSpec} onClose={() => setConfirmSpec(null)} />}
      {toast && (
        <div class="toast" role="status">
          {toast}
        </div>
      )}
    </>
  )
}

function LinksReport({ onOpen }: { onOpen: (id: string) => void }) {
  const host = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = host.current
    if (!el) return
    const reload = () => renderUnresolvedReport(el, onOpen, reload)
    reload()
  }, [onOpen])
  return <div class="page-scroll" ref={host} />
}
