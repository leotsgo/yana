// The app shell: top bar, sidebar (search, tree or results, nav), the open
// note or report, and the overlays (command palette, quick switcher,
// prompts, menus). Three layouts share this one tree: on phones the
// sidebar is a drawer and a bar runs along the bottom; on tablets the
// drawer stays but the top bar has room for actions; on desktops the
// sidebar is a column that can be collapsed. Global hotkeys live here
// too; see hotkeys.ts for the bindings.

import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks'

import { api, ApiError, baseOf, dirOf, saveBlob } from './api'
import type { MoveResult, Note, SpaceInfo, SpaceTree, Status } from './api'
import * as auth from './auth'
import { Confirm } from './confirm'
import type { ConfirmSpec } from './confirm'
import { isEditable, keys, label, matches } from './hotkeys'
import { Icon } from './icons'
import { useVisualViewport } from './keyboard'
import { useLayout } from './layout'
import { renderUnresolvedReport } from './links'
import { Menu } from './menu'
import type { MenuSpec } from './menu'
import { NotePage } from './note'
import { Palette } from './palette'
import type { PaletteItem, PaletteSpec } from './palette'
import * as prefs from './prefs'
import { SearchResults } from './search'
import { SettingsPage, isSection } from './settings'
import type { Section } from './settings'
import { TrashPage } from './trash'
import { Tree, flatten } from './tree'

type Route =
  | { kind: 'home' }
  | { kind: 'note'; id: string }
  | { kind: 'links' }
  | { kind: 'trash' }
  | { kind: 'settings'; section: Section | null }

function parseRoute(): Route {
  if (location.pathname === '/links') return { kind: 'links' }
  if (location.pathname === '/trash') return { kind: 'trash' }
  if (location.pathname === '/settings') return { kind: 'settings', section: null }
  const st = location.pathname.match(/^\/settings\/([a-z]+)$/)
  if (st && st[1]) return { kind: 'settings', section: isSection(st[1]) ? st[1] : null }
  const m = location.pathname.match(/^\/n\/([0-9A-Za-z]{26})$/)
  return m && m[1] ? { kind: 'note', id: m[1] } : { kind: 'home' }
}

export function App({ onSignOut }: { onSignOut: () => void }) {
  const layout = useLayout()
  const [route, setRoute] = useState<Route>(parseRoute)
  const [spaces, setSpaces] = useState<SpaceTree[] | null>(null)
  const [spaceList, setSpaceList] = useState<SpaceInfo[] | null>(null)
  const [treeError, setTreeError] = useState<string | null>(null)
  const [status, setStatus] = useState<Status | null>(null)
  const [query, setQuery] = useState('')
  const [regex, setRegex] = useState(false)
  const [palette, setPalette] = useState<PaletteSpec | null>(null)
  const [menu, setMenu] = useState<MenuSpec | null>(null)
  const [confirmSpec, setConfirmSpec] = useState<ConfirmSpec | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const [mode, setMode] = useState<prefs.OpenMode>(prefs.openMode)
  const [openPref, setOpenPref] = useState<prefs.OpenMode>(prefs.openMode)
  const [live, setLive] = useState(prefs.livePreview)
  const [editing, setEditing] = useState(false) // the phone is showing an editor
  const [collapsed, setCollapsed] = useState(prefs.sidebarCollapsed) // desktop column
  const [drawer, setDrawer] = useState(false) // phone and tablet
  const [fresh, setFresh] = useState<string | null>(null)
  const [themePref, setThemePref] = useState(prefs.theme)
  const [rev, setRev] = useState(0) // bumps to reopen the current note after a move
  const searchInput = useRef<HTMLInputElement>(null)
  const current = useRef<Note | null>(null)
  const user = auth.user()
  const narrow = layout !== 'desktop'

  // --- navigation --------------------------------------------------------

  // Every note opens in the preferred mode; a new one opens in the editor.
  const navigate = useCallback((id: string | null, push = true, edit = false) => {
    const path = id ? `/n/${id}` : '/'
    if (push && location.pathname !== path) history.pushState(null, '', path)
    setRoute(id ? { kind: 'note', id } : { kind: 'home' })
    setMode(edit ? 'edit' : prefs.openMode())
    setDrawer(false)
    if (!id) document.title = 'YANA/'
  }, [])

  const openLinks = useCallback((push = true) => {
    if (push && location.pathname !== '/links') history.pushState(null, '', '/links')
    setRoute({ kind: 'links' })
    setDrawer(false)
    document.title = 'Unresolved links — YANA/'
  }, [])

  const openTrash = useCallback((push = true) => {
    if (push && location.pathname !== '/trash') history.pushState(null, '', '/trash')
    setRoute({ kind: 'trash' })
    setDrawer(false)
    document.title = 'Trash — YANA/'
  }, [])

  const openSettings = useCallback((section: Section | null = null, push = true) => {
    const path = section ? `/settings/${section}` : '/settings'
    if (push && location.pathname !== path) history.pushState(null, '', path)
    setRoute({ kind: 'settings', section })
    setDrawer(false)
    document.title = 'Settings — YANA/'
  }, [])

  useEffect(() => {
    const onPop = () => {
      setRoute(parseRoute())
      setMode(prefs.openMode())
    }
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  // The drawer is a phone thing; a resize to desktop leaves it closed.
  useEffect(() => {
    if (!narrow) setDrawer(false)
  }, [narrow])

  // A session revoked from another device, or expired for good, puts
  // this one back at the sign-in screen on its next request.
  useEffect(() => auth.onSignedOut(onSignOut), [onSignOut])

  // The settings pages write preferences; the shell's copies follow.
  useEffect(
    () =>
      prefs.onChange(() => {
        setThemePref(prefs.theme())
        setOpenPref(prefs.openMode())
        setLive(prefs.livePreview())
      }),
    [],
  )

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

  const loadSpaces = useCallback(async () => {
    try {
      const { spaces } = await api.spaces()
      setSpaceList(spaces)
    } catch {
      // The tree carries the names; the settings pages retry on their own.
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
    void loadSpaces()
    // Files can change under us; keep the tree fresh without a websocket.
    const t = window.setInterval(() => { void loadTree() }, 30_000)
    const onVis = () => { if (document.visibilityState === 'visible') void loadTree() }
    document.addEventListener('visibilitychange', onVis)
    return () => {
      window.clearInterval(t)
      document.removeEventListener('visibilitychange', onVis)
    }
  }, [loadTree, loadStatus, loadSpaces])

  const say = useCallback((msg: string) => setToast(msg), [])
  useEffect(() => {
    if (!toast) return
    const t = window.setTimeout(() => setToast(null), 4500)
    return () => window.clearTimeout(t)
  }, [toast])

  // --- actions -----------------------------------------------------------

  const notes = useMemo(() => flatten(spaces ?? []), [spaces])

  /** The space new things go into: the open note's, else the preferred
   * one from settings, else the first one. */
  function defaultSpace(): string {
    if (current.current) return current.current.space
    const pref = prefs.defaultSpace()
    if (pref && spaces?.some((s) => s.name === pref)) return pref
    return spaces?.[0]?.name ?? ''
  }

  /** The daily note's space: the preference, else the same as new notes. */
  function dailySpace(): string {
    const pref = prefs.dailySpace()
    if (pref && spaces?.some((s) => s.name === pref)) return pref
    return defaultSpace()
  }

  const createNote = useCallback(
    async (path: string) => {
      let p = path.trim().replace(/^\/+/, '')
      if (!/\.(md|markdown|html?)$/i.test(p)) p += '.md'
      const title = baseOf(p).replace(/\.(md|markdown|html?)$/i, '')
      try {
        const res = await api.createNote(p, `# ${title}\n\n`)
        setFresh(res.id)
        navigate(res.id, true, true)
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
      placeholder: 'Name for the new note',
      initial,
      hint: 'A name, or a path inside the tree like projects/kiln. Folders that do not exist yet are created.',
      onSubmit: (v) => void createNote(v),
    })
  }

  const openDaily = useCallback(async () => {
    const d = new Date()
    const date = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
    try {
      const res = await api.daily(dailySpace(), date)
      if (res.created) {
        setFresh(res.id)
        void loadTree()
      }
      navigate(res.id, true, res.created)
    } catch (err) {
      say(err instanceof ApiError ? err.message : 'Could not open the daily note.')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navigate, loadTree, say, spaces])

  const movedToast = useCallback(
    (res: MoveResult) => {
      const n = res.rewritten
      say(
        n > 0
          ? `Moved to ${res.note.path}. ${n} link${n === 1 ? '' : 's'} updated${res.broken ? `, ${res.broken} left unresolved` : ''}.`
          : `Moved to ${res.note.path}.`,
      )
    },
    [say],
  )

  const moveNote = useCallback(
    async (id: string, from: string, to: string) => {
      try {
        const res = await api.moveNote(id, to)
        current.current = null
        void loadTree()
        movedToast(res)
        if (route.kind === 'note' && route.id === id) setRev((r) => r + 1)
      } catch (err) {
        say(err instanceof ApiError ? err.message : `Could not move ${from}.`)
      }
    },
    [loadTree, say, movedToast, route],
  )

  // A rename from the title keeps the page mounted; only the tree changes.
  // The new heading reaches the index a beat after the file moves, so the
  // tree is read twice.
  const onMoved = useCallback(
    (res: MoveResult) => {
      void loadTree()
      window.setTimeout(() => void loadTree(), 1500)
      movedToast(res)
    },
    [loadTree, movedToast],
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
              prefs.forgetRecent(n.id)
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

  // Mod+E: the editor beside the render on a wide screen; on a phone,
  // in and out of the editor.
  const toggleSplit = useCallback(() => {
    setMode((m) => (layout === 'phone' ? (m === 'edit' ? 'read' : 'edit') : m === 'split' ? 'edit' : 'split'))
  }, [layout])

  function setOpenPrefTo(m: prefs.OpenMode): void {
    prefs.setOpenMode(m)
    setOpenPref(m)
  }

  function toggleLive(): void {
    prefs.setLivePreview(!live)
    setLive(!live)
  }

  function toggleSidebar(): void {
    if (narrow) {
      setDrawer((d) => !d)
      return
    }
    setCollapsed((c) => {
      prefs.setSidebarCollapsed(!c)
      return !c
    })
  }

  function focusSearch(): void {
    if (narrow) setDrawer(true)
    else if (collapsed) {
      setCollapsed(false)
      prefs.setSidebarCollapsed(false)
    }
    // The input may be mounting; focus after the next paint.
    window.setTimeout(() => {
      const el = searchInput.current
      if (!el) return
      el.focus()
      el.select()
    }, 0)
  }

  function openSwitcher(): void {
    const recent = new Set(prefs.recents())
    const items = notes.map((n) => ({ id: n.id, label: n.title, detail: n.path, run: () => navigate(n.id) }))
    items.sort((a, b) => Number(recent.has(b.id)) - Number(recent.has(a.id)))
    setPalette({
      mode: 'list',
      placeholder: 'Open a note',
      items,
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

  function showShortcuts(): void {
    const rows: Array<[string, string]> = [
      ['New note', label(keys.newNote)],
      ["Today's note", label(keys.daily)],
      ['Open a note by name', label(keys.switcher)],
      ['Command palette', label(keys.palette)],
      ['Search', label(keys.search) + ' or /'],
      ['Edit the open note', 'E'],
      ['Back to reading', 'Esc'],
      ['Editor and preview side by side', label(keys.split)],
      ['Close a dialog', 'Esc'],
    ]
    setPalette({
      mode: 'list',
      placeholder: 'Keyboard shortcuts',
      items: rows.map(([what, key]) => ({ id: what, label: what, hint: key, run: () => undefined })),
    })
  }

  function openPalette(): void {
    const items: PaletteItem[] = [
      { id: 'new', label: 'New note', hint: label(keys.newNote), run: () => newNotePrompt() },
      { id: 'daily', label: "Today's note", hint: label(keys.daily), run: () => void openDaily() },
      { id: 'open', label: 'Open a note', hint: label(keys.switcher), run: openSwitcher },
      { id: 'search', label: 'Search notes', hint: label(keys.search), run: focusSearch },
    ]
    if (!narrow) {
      items.push({ id: 'sidebar', label: collapsed ? 'Show the sidebar' : 'Hide the sidebar', run: toggleSidebar })
    }
    if (current.current?.kind === 'md') {
      const shown = layout === 'phone' && mode === 'split' ? 'read' : mode
      if (shown !== 'read') items.push({ id: 'read', label: 'Read this note', hint: 'Esc', run: () => setMode('read') })
      if (shown !== 'edit') items.push({ id: 'edit', label: 'Edit this note', hint: 'E', run: () => setMode('edit') })
      if (layout !== 'phone' && shown !== 'split') {
        items.push({ id: 'split', label: 'Editor and preview side by side', hint: label(keys.split), run: () => setMode('split') })
      }
    }
    if (current.current) {
      items.push({ id: 'rename', label: 'Rename or move this note', detail: current.current.path, run: renamePrompt })
      if (current.current.role !== 'viewer') {
        items.push({ id: 'delete', label: 'Delete this note', detail: 'moves it to the trash', run: deleteNotePrompt })
      }
    }
    items.push({ id: 'links', label: 'Unresolved links', detail: 'every wikilink that points nowhere', run: () => openLinks() })
    items.push({ id: 'trash', label: 'Trash', detail: 'deleted notes, kept for 30 days', run: () => openTrash() })
    items.push({ id: 'settings', label: 'Settings', detail: 'account, people, spaces, agents, appearance, data', run: () => openSettings(narrow ? null : 'account') })
    items.push({ id: 'settings-account', label: 'Account settings', detail: 'display name, password, devices', run: () => openSettings('account') })
    items.push({ id: 'settings-spaces', label: 'Spaces and sharing', detail: 'members and roles, default spaces', run: () => openSettings('spaces') })
    items.push({ id: 'settings-data', label: 'Export, history, and the index', detail: 'in settings', run: () => openSettings('data') })
    if (user?.is_owner) {
      items.push({ id: 'settings-people', label: 'People', detail: 'the accounts on this server', run: () => openSettings('people') })
      items.push({ id: 'settings-agents', label: 'Agents', detail: 'MCP keys', run: () => openSettings('agents') })
    }
    items.push({ id: 'theme', label: 'Theme', detail: themePref, run: () => setThemeNext() })
    items.push({ id: 'open', label: 'Open notes in', detail: openLabel(openPref), run: setOpenPrefNext })
    items.push({ id: 'live', label: 'Hide markdown syntax while editing', detail: live ? 'on' : 'off', run: toggleLive })
    items.push({ id: 'settings-appearance', label: 'Appearance', detail: 'text size, line width, density', run: () => openSettings('appearance') })
    items.push({ id: 'keys', label: 'Keyboard shortcuts', run: showShortcuts })
    if (user) items.push({ id: 'signout', label: 'Sign out', detail: user.username, run: () => void auth.logout().then(onSignOut) })
    setPalette({ mode: 'list', placeholder: 'Type a command', items })
  }

  function setThemeTo(t: prefs.Theme): void {
    prefs.setTheme(t)
    setThemePref(t)
  }

  function setThemeNext(): void {
    const order: prefs.Theme[] = ['system', 'light', 'dark']
    const i = order.indexOf(themePref)
    setThemeTo(order[(i + 1) % order.length] ?? 'system')
  }

  function setOpenPrefNext(): void {
    const order: prefs.OpenMode[] = layout === 'phone' ? ['read', 'edit'] : ['read', 'edit', 'split']
    const i = order.indexOf(openPref)
    setOpenPrefTo(order[(i + 1) % order.length] ?? 'read')
  }

  function openUserMenu(anchor: HTMLElement): void {
    setMenu({
      anchor,
      label: 'account',
      items: [
        { id: 'light', label: 'Light', icon: 'sun', checked: themePref === 'light', run: () => setThemeTo('light') },
        { id: 'dark', label: 'Dark', icon: 'moon', checked: themePref === 'dark', run: () => setThemeTo('dark') },
        { id: 'system', label: 'Match the system', icon: 'monitor', checked: themePref === 'system', run: () => setThemeTo('system') },
        'sep',
        { id: 'open-read', label: 'Open notes to read', icon: 'book-open', checked: openPref === 'read', run: () => setOpenPrefTo('read') },
        { id: 'open-edit', label: 'Open notes to edit', icon: 'pencil', checked: openPref === 'edit', run: () => setOpenPrefTo('edit') },
        ...(layout === 'phone'
          ? []
          : [{ id: 'open-split', label: 'Open notes side by side', icon: 'columns' as const, checked: openPref === 'split', run: () => setOpenPrefTo('split') }]),
        { id: 'live', label: 'Hide syntax while editing', icon: 'eye', checked: live, run: toggleLive },
        'sep',
        { id: 'settings', label: 'Settings', icon: 'settings', run: () => openSettings(narrow ? null : 'account') },
        { id: 'keys', label: 'Keyboard shortcuts', icon: 'keyboard', run: showShortcuts },
        {
          id: 'about',
          label: status ? `${status.notes} notes · ${status.version}` : 'YANA/',
          icon: 'info',
          detail: status && !status.ready ? 'indexing' : undefined,
          disabled: true,
          run: () => undefined,
        },
        ...(user
          ? ['sep' as const, { id: 'signout', label: 'Sign out', icon: 'log-out' as const, detail: user.username, run: () => void auth.logout().then(onSignOut) }]
          : []),
      ],
    })
  }

  // --- hotkeys -----------------------------------------------------------

  const actions = useRef({ openPalette, openSwitcher, newNotePrompt, openDaily, focusSearch, toggleSplit })
  actions.current = { openPalette, openSwitcher, newNotePrompt, openDaily, focusSearch, toggleSplit }

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
      else if (matches(ev, keys.split)) a.toggleSplit()
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

  const onNote = useCallback((n: Note | null) => {
    current.current = n
    if (n) prefs.touchRecent(n.id)
  }, [])
  const searching = query.trim() !== ''
  const selected = route.kind === 'note' ? route.id : null
  const sidebarShown = narrow ? drawer : !collapsed

  // While the phone keyboard is up the shell is sized to what is left
  // above it, so the formatting bar and the caret stay in view.
  const phoneEditing = layout === 'phone' && editing
  const viewport = useVisualViewport(phoneEditing)
  const shellClass = ['shell', layout, sidebarShown ? 'sidebar-open' : 'sidebar-closed', viewport ? 'kb' : ''].join(' ')
  const shellStyle = viewport ? `height:${viewport.height}px;top:${viewport.top}px` : undefined

  return (
    <div class={shellClass} style={shellStyle}>
      <header class="topbar">
        <button
          type="button"
          class="icon-btn"
          title={narrow ? 'Notes' : collapsed ? 'Show the sidebar' : 'Hide the sidebar'}
          aria-label={narrow ? 'Open the notes drawer' : 'Toggle the sidebar'}
          aria-expanded={sidebarShown}
          onClick={toggleSidebar}
        >
          <Icon name={narrow ? 'menu' : 'panel-left'} size={18} />
        </button>
        <a class="wordmark" href="/" onClick={(ev) => { ev.preventDefault(); navigate(null) }}>
          YANA/
        </a>
        <span class="spacer" />
        {layout !== 'phone' && (
          <>
            <button type="button" class="btn" title={`New note (${label(keys.newNote)})`} onClick={() => newNotePrompt()}>
              <Icon name="plus" />
              New
            </button>
            <button type="button" class="btn" title={`Today's note (${label(keys.daily)})`} onClick={() => void openDaily()}>
              <Icon name="calendar" />
              Today
            </button>
          </>
        )}
        <button type="button" class="icon-btn" title={`Command palette (${label(keys.palette)})`} aria-label="Command palette" onClick={openPalette}>
          <Icon name="command" size={18} />
        </button>
        <button
          type="button"
          class="icon-btn user-btn"
          title={user ? user.username : 'Account'}
          aria-label="Account menu"
          onClick={(ev) => openUserMenu(ev.currentTarget as HTMLElement)}
        >
          {user ? <span class="avatar">{user.username.slice(0, 1).toUpperCase()}</span> : <Icon name="user" size={18} />}
        </button>
      </header>
      <div class="body">
        <aside class="sidebar" aria-label="notes" aria-hidden={!sidebarShown}>
          <div class="sidebar-search">
            <Icon name="search" class="sidebar-search-icon" />
            <input
              ref={searchInput}
              type="search"
              class="search-input"
              placeholder="Search notes"
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
            <button
              type="button"
              class={'regex-btn' + (regex ? ' on' : '')}
              disabled={status ? !status.regex_search : false}
              aria-pressed={regex}
              title={status && !status.regex_search ? 'Regex search needs ripgrep on the server.' : 'Match a regular expression against the files'}
              onClick={() => setRegex((r) => !r)}
            >
              .*
            </button>
          </div>
          <div class="sidebar-scroll">
            {searching ? (
              <SearchResults query={query} regex={regex} onOpen={navigate} />
            ) : (
              <nav class="tree" aria-label="tree">
                {treeError ? (
                  <div class="empty">
                    <p class="error">{treeError}</p>
                    <button type="button" class="btn" onClick={() => void loadTree()}>
                      <Icon name="refresh" />
                      Try again
                    </button>
                  </div>
                ) : spaces === null ? (
                  <div class="tree-skeleton" aria-busy="true">
                    <span /><span /><span /><span /><span />
                  </div>
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
          </div>
          {!searching && (
            <nav class="sidebar-nav" aria-label="more">
              <a class={'sidebar-link' + (route.kind === 'links' ? ' selected' : '')} href="/links" onClick={(ev) => { ev.preventDefault(); openLinks() }}>
                <Icon name="unlink" />
                Unresolved links
              </a>
              <a class={'sidebar-link' + (route.kind === 'trash' ? ' selected' : '')} href="/trash" onClick={(ev) => { ev.preventDefault(); openTrash() }}>
                <Icon name="trash" />
                Trash
              </a>
              <a class={'sidebar-link' + (route.kind === 'settings' ? ' selected' : '')} href="/settings" onClick={(ev) => { ev.preventDefault(); openSettings(narrow ? null : 'account') }}>
                <Icon name="settings" />
                Settings
              </a>
            </nav>
          )}
        </aside>
        {narrow && drawer && <div class="scrim" onClick={() => setDrawer(false)} />}
        <main class="content">
          {route.kind === 'note' && (
            <NotePage
              key={`${route.id}:${rev}`}
              id={route.id}
              layout={layout}
              mode={mode}
              onMode={setMode}
              live={live}
              onEditing={setEditing}
              onOpen={navigate}
              onNote={onNote}
              onToast={say}
              onMenu={setMenu}
              fresh={fresh === route.id}
              onDelete={deleteNotePrompt}
              onRename={renamePrompt}
              onExport={exportNote}
              onMoved={onMoved}
            />
          )}
          {route.kind === 'links' && <LinksReport onOpen={navigate} />}
          {route.kind === 'trash' && (
            <TrashPage onOpen={navigate} onToast={say} confirm={setConfirmSpec} onChanged={() => void loadTree()} />
          )}
          {route.kind === 'settings' && (
            <SettingsPage
              section={route.section}
              layout={layout}
              status={status}
              spaces={spaceList}
              notes={notes}
              onSection={(s) => openSettings(s)}
              onToast={say}
              confirm={setConfirmSpec}
              onChanged={() => { void loadTree(); void loadSpaces() }}
              onOpen={navigate}
              onOpenTrash={() => openTrash()}
              onSignOut={onSignOut}
              onStatus={() => void loadStatus()}
            />
          )}
          {route.kind === 'home' && (
            <Home
              notes={notes}
              loading={spaces === null}
              onOpen={navigate}
              onNew={() => newNotePrompt()}
              onDaily={() => void openDaily()}
            />
          )}
        </main>
      </div>
      {layout === 'phone' && !editing && (
        <nav class="bottombar" aria-label="quick actions">
          <button type="button" class={'bottombar-btn' + (drawer ? ' on' : '')} onClick={() => setDrawer((d) => !d)}>
            <Icon name="folder" size={20} />
            Notes
          </button>
          <button type="button" class="bottombar-btn" onClick={focusSearch}>
            <Icon name="search" size={20} />
            Search
          </button>
          <button type="button" class="bottombar-btn" onClick={() => void openDaily()}>
            <Icon name="calendar" size={20} />
            Today
          </button>
          <button type="button" class="bottombar-btn accent" onClick={() => newNotePrompt()}>
            <Icon name="plus" size={20} />
            New
          </button>
        </nav>
      )}
      {palette && <Palette spec={palette} onClose={() => setPalette(null)} />}
      {menu && <Menu spec={menu} onClose={() => setMenu(null)} />}
      {confirmSpec && <Confirm spec={confirmSpec} onClose={() => setConfirmSpec(null)} />}
      {toast && (
        <div class="toast" role="status">
          {toast}
        </div>
      )}
    </div>
  )
}

function openLabel(m: prefs.OpenMode): string {
  switch (m) {
    case 'read':
      return 'read'
    case 'edit':
      return 'edit'
    case 'split':
      return 'side by side'
  }
}

interface HomeProps {
  notes: Array<{ id: string; path: string; title: string }>
  loading: boolean
  onOpen: (id: string) => void
  onNew: () => void
  onDaily: () => void
}

// The home page: the two things people come here to do, then what they
// opened last. Shortcuts are in the account menu.
function Home({ notes, loading, onOpen, onNew, onDaily }: HomeProps) {
  const byId = useMemo(() => new Map(notes.map((n) => [n.id, n])), [notes])
  const recent = prefs.recents().map((id) => byId.get(id)).filter((n): n is HomeProps['notes'][number] => Boolean(n))

  return (
    <div class="home">
      <div class="home-mark" aria-hidden="true">
        <Icon name="slash" size={56} />
      </div>
      <h1 class="home-title">YANA/</h1>
      <p class="home-sub">Everything you expect. Nothing you don't.</p>
      <div class="home-actions">
        <button type="button" class="btn primary large" onClick={onNew}>
          <Icon name="plus" size={18} />
          New note
        </button>
        <button type="button" class="btn large" onClick={onDaily}>
          <Icon name="calendar" size={18} />
          Today
        </button>
      </div>
      {loading ? (
        <p class="muted">Loading the tree…</p>
      ) : notes.length === 0 ? (
        <p class="muted">No notes yet. Start one above, or drop a markdown file into the notes directory; it shows up on the next scan.</p>
      ) : recent.length > 0 ? (
        <section class="recents" aria-label="recently opened">
          <h2 class="section-title">Recent</h2>
          <ul class="recents-list">
            {recent.map((n) => (
              <li key={n.id}>
                <a class="recent" href={`/n/${n.id}`} onClick={(ev) => { ev.preventDefault(); onOpen(n.id) }}>
                  <span class="recent-title">{n.title}</span>
                  <span class="recent-path">{n.path}</span>
                </a>
              </li>
            ))}
          </ul>
        </section>
      ) : (
        <p class="muted">Pick a note from the sidebar. The ones you open show up here.</p>
      )}
    </div>
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
