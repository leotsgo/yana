// The app shell: top bar, sidebar (search, tree or results, nav), the open
// note or report, and the overlays (command palette, quick switcher,
// prompts, menus). Three layouts share this one tree: on phones the
// sidebar is a drawer and a bar runs along the bottom; on tablets the
// drawer stays but the top bar has room for actions; on desktops the
// sidebar is a column that can be collapsed. Global hotkeys live here
// too; see hotkeys.ts for the bindings. The everyday actions — new note
// without a path, quick capture into today's note, pins, tags, the tree
// actions behind a right-click or a long press — are wired here as well.
// Notes open in tabs (workspace.ts): the strip sits above the note on
// tablets and desktops, and the URL is always the focused pane's active
// tab. Pages (tasks, tags, settings, the trash, home) are tabs as well,
// one per kind. A desktop splits the content into two panes, each with a
// strip.

import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks'

import { api, ApiError, baseOf, dirOf, saveBlob, spaceOf, stem } from './api'
import type { MoveResult, Note, SpaceInfo, SpaceTree, Status, TreeNode } from './api'
import { ActivityPage, WhatsChanged } from './activity'
import * as auth from './auth'
import * as cache from './cache'
import { Confirm } from './confirm'
import type { ConfirmSpec } from './confirm'
import { isLocalConflict } from './conflict'
import { isEditable, isMac, keys, label, matches, tabDigit } from './hotkeys'
import { Icon } from './icons'
import type { IconName } from './icons'
import { useVisualViewport } from './keyboard'
import { coarsePointer, current as currentLayout, useLayout } from './layout'
import { renderUnresolvedReport } from './links'
import { Menu } from './menu'
import type { MenuItem, MenuSpec } from './menu'
import type { Completions, LinkTarget } from './editor'
import { NotePage } from './note'
import * as outbox from './outbox'
import { Palette } from './palette'
import type { PaletteItem, PaletteSpec } from './palette'
import { resolveDir } from './paths'
import * as prefs from './prefs'
import * as pwa from './pwa'
import { SearchPage, SearchResults } from './search'
import { SettingsPage, isSection } from './settings'
import type { Section } from './settings'
import { SharePage } from './share'
import { appendToNote, composeShareBlock, today } from './sharelib'
import { TagPage, TagsIndex } from './tags'
import { TasksPage } from './tasks'
import { TrashPage } from './trash'
import { TAB_DRAG, TabStrip } from './tabstrip'
import type { TabInfo } from './tabstrip'
import { Tree, flatten, folders } from './tree'
import type { FlatNote, TreeEdit, TreeTarget } from './tree'
import * as workspace from './workspace'
import { isPage, openProps } from './workspace'
import type { OpenHow, Tab } from './workspace'

type Route =
  | { kind: 'home' }
  | { kind: 'note'; id: string }
  | { kind: 'share' }
  | { kind: 'links' }
  | { kind: 'trash' }
  | { kind: 'tags' }
  | { kind: 'tag'; tag: string }
  | { kind: 'search' }
  | { kind: 'tasks'; space: string; tag: string; path: string }
  | { kind: 'activity'; space: string; path: string }
  | { kind: 'settings'; section: Section | null }

/** Where the address points: the window's own, or a page tab's path. */
function parseRoute(url = location.pathname + location.search): Route {
  const u = new URL(url, location.origin)
  const path = u.pathname
  const q = u.searchParams
  if (path === '/share') return { kind: 'share' }
  if (path === '/links') return { kind: 'links' }
  if (path === '/trash') return { kind: 'trash' }
  if (path === '/tags') return { kind: 'tags' }
  if (path === '/search') return { kind: 'search' }
  if (path === '/tasks') return { kind: 'tasks', space: q.get('space') ?? '', tag: q.get('tag') ?? '', path: q.get('path') ?? '' }
  if (path === '/activity') return { kind: 'activity', space: q.get('space') ?? '', path: q.get('path') ?? '' }
  if (path === '/settings') return { kind: 'settings', section: null }
  const tg = path.match(/^\/tags\/(.+)$/)
  if (tg && tg[1]) return { kind: 'tag', tag: decodeURIComponent(tg[1]).toLowerCase() }
  const st = path.match(/^\/settings\/([a-z]+)$/)
  if (st && st[1]) return { kind: 'settings', section: isSection(st[1]) ? st[1] : null }
  const m = path.match(/^\/n\/([0-9A-Za-z]{26})$/)
  return m && m[1] ? { kind: 'note', id: m[1] } : { kind: 'home' }
}

/** What a tab shows: a note, or the page at its path. */
function routeOf(id: string): Route {
  return isPage(id) ? parseRoute(id) : { kind: 'note', id }
}

/** The name and icon a page tab carries. */
function pageLabel(r: Route): { title: string; icon: IconName } {
  switch (r.kind) {
    case 'tasks':
      return { title: 'Tasks', icon: 'check-square' }
    case 'tags':
      return { title: 'Tags', icon: 'tag' }
    case 'tag':
      return { title: '#' + r.tag, icon: 'tag' }
    case 'links':
      return { title: 'Unresolved links', icon: 'unlink' }
    case 'trash':
      return { title: 'Trash', icon: 'trash' }
    case 'activity':
      return { title: 'What changed', icon: 'history' }
    case 'settings':
      return { title: 'Settings', icon: 'settings' }
    case 'share':
      return { title: 'Share', icon: 'share' }
    default:
      return { title: 'Home', icon: 'home' }
  }
}

/** The narrowest a pane gets beside another, and how near the middle
 * the divider snaps to halves. */
const PANE_MIN = 360
const PANE_SNAP = 40

/** What the address says at load. A note or a page comes to the front
 * of the strip, opened in a tab if it was not there; the bare root shows
 * the tab that was active last time, or the home screen. True for the
 * phone's search screen. */
function initialLoad(): boolean {
  const r = parseRoute()
  if (r.kind === 'search') return true
  if (r.kind === 'home') return false
  const id = r.kind === 'note' ? r.id : location.pathname + location.search
  const had = workspace.pane().tabs.find((t) => t.id === id)
  if (had) workspace.activate(had.key)
  else workspace.open(id, currentLayout() === 'phone' ? 'here' : 'tab', { mode: prefs.openMode() })
  return false
}

/** Track a subscribe/get module state in a component. */
function useExternal<T>(get: () => T, subscribe: (l: () => void) => () => void): T {
  const [value, setValue] = useState(get)
  useEffect(() => subscribe(() => setValue(get())), [get, subscribe])
  return value
}

interface Toast {
  msg: string
  /** One button beside the message: Undo, mostly. */
  action?: { label: string; run: () => void }
}

/** A note as the tree knows it, enough for the actions on one. */
interface NoteRef {
  id: string
  path: string
  title: string
}

export function App({ onSignOut }: { onSignOut: () => void }) {
  const layout = useLayout()
  // The open tabs, and the page shown instead of the active one, if any.
  // The route follows from the two.
  const ws = useExternal(workspace.get, workspace.subscribe)
  // The phone's search screen, the one view that is not a tab.
  const [phoneSearch, setPhoneSearch] = useState(initialLoad)
  const [spaces, setSpaces] = useState<SpaceTree[] | null>(null)
  const [spaceList, setSpaceList] = useState<SpaceInfo[] | null>(null)
  const [treeError, setTreeError] = useState<string | null>(null)
  const [staleTree, setStaleTree] = useState(false) // the tree is the last saved copy
  const [status, setStatus] = useState<Status | null>(null)
  const [query, setQuery] = useState('')
  const [regex, setRegex] = useState(false)
  const [palette, setPalette] = useState<PaletteSpec | null>(null)
  const [menu, setMenu] = useState<MenuSpec | null>(null)
  const [confirmSpec, setConfirmSpec] = useState<ConfirmSpec | null>(null)
  const [toast, setToast] = useState<Toast | null>(null)
  const [openPref, setOpenPref] = useState<prefs.OpenMode>(prefs.openMode)
  const [live, setLive] = useState(prefs.livePreview)
  const [editing, setEditing] = useState(false) // the phone is showing an editor
  const [collapsed, setCollapsed] = useState(prefs.sidebarCollapsed) // desktop column
  const [drawer, setDrawer] = useState(false) // phone and tablet
  // The note whose title should be focused: a new one, or one a person
  // double-clicked in the tree. The count makes a repeat on the open note count.
  // Keyed by the tab, so the same note in the other pane is left alone.
  const [fresh, setFresh] = useState<{ key: string; seq: number } | null>(null)
  const [treeEdit, setTreeEdit] = useState<TreeEdit | null>(null) // a folder input open in the tree
  const [themePref, setThemePref] = useState(prefs.theme)
  const [pinList, setPinList] = useState(prefs.pins)
  const [rev, setRev] = useState(0) // bumps to reopen the current note after a move
  // A search hit opens with its match scrolled into view, and a task
  // row with its box; the sequence remounts the page so a second one on
  // the same note scrolls too.
  const [hit, setHit] = useState<{ key: string; text: string | null; line: number | null; seq: number } | null>(null)
  const [openTasks, setOpenTasks] = useState<number | null>(null)
  const searchInput = useRef<HTMLInputElement>(null)
  // The focused pane's note, for the actions that act on "this note";
  // each pane's own is kept so a focus change can hand it over.
  const current = useRef<Note | null>(null)
  const paneNotes = useRef<Array<Note | null>>([null, null])
  const user = auth.user()
  const narrow = layout !== 'desktop'
  const sidebarShown = narrow ? drawer : !collapsed
  // Two panes only on a desktop; narrower, the second merges away.
  const split = ws.panes.length > 1 && layout === 'desktop'
  /** The tab a pane shows. */
  const tabIn = (i: number): Tab | null => {
    const p = ws.panes[i]
    return p ? (p.tabs.find((t) => t.key === p.active) ?? null) : null
  }
  const active = tabIn(ws.focus)
  const route: Route = phoneSearch ? { kind: 'search' } : active ? routeOf(active.id) : { kind: 'home' }
  const mode: prefs.OpenMode = active?.mode ?? openPref

  // --- navigation --------------------------------------------------------

  // What history does when the active tab changes next: a new entry for
  // opening a note, the same entry for switching tabs, nothing after
  // back or forward (the browser has moved already).
  const histMode = useRef<'push' | 'replace' | 'none'>('replace')
  const layoutNow = useRef(layout)
  layoutNow.current = layout
  // Titles by note id, from the tree: a tab shows one before its note loads.
  const titles = useRef(new Map<string, string>())

  const setMode = useCallback((m: prefs.OpenMode) => {
    const t = workspace.activeTab()
    if (t) workspace.setMode(t.key, m)
  }, [])

  // A note opens in the preview tab unless asked otherwise, in the
  // preferred mode; a new one opens in a tab of its own, in the editor.
  // A phone has no strip: the note takes the place of the one showing.
  // Null is the home screen, which is a tab of its own.
  const navigate = useCallback(
    (id: string | null, how: OpenHow = 'preview', opts: { mode?: prefs.OpenMode; hit?: { text: string | null; line: number | null } } = {}): Tab | null => {
      setDrawer(false)
      if (!id) {
        openPage('/')
        return null
      }
      const lay = layoutNow.current
      const h: OpenHow = lay === 'phone' ? 'here' : how === 'right' && lay !== 'desktop' ? 'tab' : how
      const tab = workspace.open(id, h, { mode: opts.mode ?? prefs.openMode(), title: titles.current.get(id) ?? '' })
      if (h === 'background' || h === 'right') return tab
      if (opts.mode) workspace.setMode(tab.key, opts.mode)
      histMode.current = 'push'
      const want = opts.hit
      setHit((prev) => (want ? { key: tab.key, text: want.text, line: want.line, seq: (prev?.seq ?? 0) + 1 } : null))
      return tab
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  )

  // A search result: read mode, scrolled to the match.
  const openHit = useCallback(
    (id: string, highlight: string | null, how?: OpenHow) => {
      navigate(id, how, highlight ? { mode: 'read', hit: { text: highlight, line: null } } : { mode: 'read' })
      if (how !== 'background') setQuery('')
    },
    [navigate],
  )

  // A task row: read mode, scrolled to its box.
  const openTask = useCallback(
    (id: string, line: number | null, how?: OpenHow) => navigate(id, how, { mode: 'read', hit: { text: null, line } }),
    [navigate],
  )

  // A page opens in its tab: the one of its kind, moved to this path,
  // or a new one beside the active tab. A phone swaps it in.
  function openPage(path: string, push = true): void {
    workspace.open(path, layoutNow.current === 'phone' ? 'here' : 'tab', { mode: prefs.openMode() })
    histMode.current = push ? 'push' : 'replace'
    setDrawer(false)
    setHit(null)
  }

  const openLinks = useCallback((push = true) => openPage('/links', push), [])
  const openTrash = useCallback((push = true) => openPage('/trash', push), [])
  const openTags = useCallback((push = true) => openPage('/tags', push), [])
  const openTag = useCallback((tag: string, push = true) => openPage(`/tags/${encodeURIComponent(tag)}`, push), [])
  // The activity feed: every space by default, one space (or a folder in
  // it) from the tree's context menus and the filters on the page.
  const openActivity = useCallback(
    (space = '', path = '', push = true) => {
      const q = new URLSearchParams()
      if (space) q.set('space', space)
      if (path) q.set('path', path)
      const qs = q.toString()
      openPage('/activity' + (qs ? '?' + qs : ''), push)
    },
    [],
  )
  // The tasks page: every space by default, one space (or a folder in
  // it) from a folder's context menu and the page's own filters.
  const openTasksPage = useCallback(
    (space = '', path = '', push = true) => {
      const q = new URLSearchParams()
      if (space) q.set('space', space)
      if (path) q.set('path', path)
      const qs = q.toString()
      openPage('/tasks' + (qs ? '?' + qs : ''), push)
    },
    [],
  )
  const openSettings = useCallback(
    (section: Section | null = null, push = true) => openPage(section ? `/settings/${section}` : '/settings', push),
    [],
  )

  // Back and forward: an entry made by a tab goes back to that tab, at
  // the note or page it showed then; one whose tab is closed opens again
  // (a note in the preview tab).
  useEffect(() => {
    const onPop = () => {
      const r = parseRoute()
      histMode.current = 'none'
      setHit(null)
      setPhoneSearch(r.kind === 'search')
      if (r.kind === 'search') return
      if (r.kind === 'home' && workspace.pane().tabs.length === 0) return
      const id = r.kind === 'note' ? r.id : location.pathname + location.search
      const key = (history.state as { tab?: string | null } | null)?.tab
      const at = key ? workspace.find(key) : null
      const title = titles.current.get(id) ?? ''
      if (at) workspace.retarget(at.tab.key, id, prefs.openMode(), title)
      else workspace.open(id, layoutNow.current === 'phone' ? 'here' : r.kind === 'note' ? 'preview' : 'tab', { mode: prefs.openMode(), title })
    }
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])

  // The address and the window title follow the focused pane's active tab.
  const activePath = active ? (isPage(active.id) ? active.id : `/n/${active.id}`) : '/'
  useEffect(() => {
    if (active && isPage(active.id)) document.title = `${pageLabel(routeOf(active.id)).title} — YANA/`
    else if (!active) document.title = 'YANA/'
    const how = histMode.current
    histMode.current = 'replace'
    if (how === 'none' || phoneSearch) return
    const state = { tab: active?.key ?? null }
    if (how === 'push' && location.pathname + location.search !== activePath) history.pushState(state, '', activePath)
    else history.replaceState(state, '', activePath)
  }, [activePath, active?.key, phoneSearch])

  // Below desktop width there is room for one pane: the right one's tabs
  // join the left strip. Widening again does not split it back.
  useEffect(() => {
    if (layout !== 'desktop' && ws.panes.length > 1) workspace.merge()
  }, [layout, ws.panes.length])

  // The drawer is a phone thing; a resize to desktop leaves it closed.
  useEffect(() => {
    if (!narrow) setDrawer(false)
  }, [narrow])

  // The search page is the phone's; wider, the box is in the sidebar.
  useEffect(() => {
    if (!phoneSearch || layout === 'phone') return
    history.replaceState({ tab: active?.key ?? null }, '', activePath)
    setPhoneSearch(false)
    window.setTimeout(() => searchInput.current?.focus(), 0)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [phoneSearch, layout])

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
        setPinList(prefs.pins())
      }),
    [],
  )

  // --- data --------------------------------------------------------------

  const net = useExternal(pwa.netState, pwa.subscribeNet)
  const box = useExternal(outbox.outboxState, outbox.subscribeOutbox)

  const loadTree = useCallback(async () => {
    try {
      const { spaces } = await api.tree()
      setSpaces(spaces)
      setTreeError(null)
      setStaleTree(false)
      void cache.putTree(spaces)
    } catch (err) {
      // Offline, the last saved tree stands in for the server's.
      if (cache.networkDown(err)) {
        const cached = await cache.getTree()
        if (cached) {
          setSpaces(cached)
          setStaleTree(true)
          return
        }
      }
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
      void cache.putStatus(s)
      if (!s.ready) window.setTimeout(() => { void loadStatus(); void loadTree() }, 2000)
    } catch (err) {
      if (cache.networkDown(err)) setStatus(await cache.getStatus())
      else setStatus(null)
    }
  }, [loadTree])

  // The open-task count follows the tree: it badges the home screen and
  // the sidebar, and a tick changes it.
  const loadTaskCount = useCallback(async () => {
    try {
      const { count } = await api.taskCount()
      setOpenTasks(count)
    } catch {
      // Offline or indexing; the badge is not worth a message.
    }
  }, [])

  useEffect(() => {
    void loadTree()
    void loadStatus()
    void loadTaskCount()
    void outbox.drain()
    void loadSpaces()
    // Files can change under us; keep the tree fresh without a websocket.
    const t = window.setInterval(() => { void loadTree(); void loadTaskCount() }, 30_000)
    const onVis = () => { if (document.visibilityState === 'visible') { void loadTree(); void loadTaskCount(); void outbox.drain() } }
    const onOnline = () => { void loadTree(); void outbox.drain() }
    document.addEventListener('visibilitychange', onVis)
    window.addEventListener('online', onOnline)
    return () => {
      window.clearInterval(t)
      document.removeEventListener('visibilitychange', onVis)
      window.removeEventListener('online', onOnline)
    }
  }, [loadTree, loadStatus, loadSpaces, loadTaskCount])

  const say = useCallback((msg: string, action?: Toast['action']) => setToast(action ? { msg, action } : { msg }), [])
  useEffect(() => {
    if (!toast) return
    // A toast with something to undo stays a little longer.
    const t = window.setTimeout(() => setToast(null), toast.action ? 8000 : 4500)
    return () => window.clearTimeout(t)
  }, [toast])

  // Replay results surface the same way everything else does.
  useEffect(() => {
    if (box.message) say(box.message)
  }, [box.message, say])

  // --- actions -----------------------------------------------------------

  const notes = useMemo(() => flatten(spaces ?? []), [spaces])
  const byId = useMemo(() => new Map(notes.map((n) => [n.id, n])), [notes])
  const dirs = useMemo(() => folders(spaces ?? []), [spaces])
  // A tab whose note the tree no longer has — deleted, trashed, or in a
  // space this account lost — is greyed, and closes when it is picked.
  // Only a tree fresh from the server says so.
  const treeReady = spaces !== null && !staleTree && treeError === null
  const gone = useCallback((id: string) => !isPage(id) && treeReady && !byId.has(id), [treeReady, byId])

  // A conflict copy that appears while its note is open says so. The
  // tree poll brings the news; ones this client parked itself stay
  // quiet, and the first load only primes the set.
  const conflictSeen = useRef<Set<string>>(new Set())
  const conflictsPrimed = useRef(false)
  useEffect(() => {
    const open = new Set<string>()
    for (const p of ws.panes) for (const t of p.tabs) if (!isPage(t.id)) open.add(t.id)
    const next = new Set<string>()
    const news: string[] = []
    for (const n of notes) {
      if (!n.conflict) continue
      next.add(n.id)
      if (
        conflictsPrimed.current &&
        !conflictSeen.current.has(n.id) &&
        n.conflictOf &&
        open.has(n.conflictOf) &&
        !isLocalConflict(n.path)
      ) {
        const owner = byId.get(n.conflictOf)
        news.push(`A conflict copy of ${owner?.title || 'a note you have open'} appeared.`)
      }
    }
    conflictSeen.current = next
    conflictsPrimed.current = true
    for (const m of news) say(m)
  }, [notes, byId, ws, say])

  // Tabs follow renames through the tree.
  useEffect(() => {
    titles.current = new Map(notes.map((n) => [n.id, n.title]))
    if (treeReady) workspace.setTitles(titles.current)
  }, [notes, treeReady])
  /** The folder paths in one space, or in every space. */
  const foldersIn = useCallback(
    (space: string | null) => dirs.filter((d) => d.depth > 0 && (space === null || d.path.startsWith(space + '/'))).map((d) => d.path),
    [dirs],
  )
  const hasDir = useCallback((path: string) => dirs.some((d) => d.path === path), [dirs])

  // What the editor offers after `[[` and `#`: the notes of the space
  // (a link resolves within its space) and every tag in use. A note
  // links by its file name; twins in one space link by their path.
  const completions = useMemo(() => {
    const bySpace = new Map<string, LinkTarget[]>()
    const tags = new Set<string>()
    const counts = new Map<string, number>()
    for (const n of notes) {
      const key = spaceOf(n.path) + '\0' + stem(n.name).toLowerCase()
      counts.set(key, (counts.get(key) ?? 0) + 1)
      for (const t of n.tags) tags.add(t)
    }
    for (const n of notes) {
      const space = spaceOf(n.path)
      const twins = (counts.get(space + '\0' + stem(n.name).toLowerCase()) ?? 0) > 1
      const inSpace = space ? n.path.slice(space.length + 1) : n.path
      const target = twins ? stem(inSpace) : stem(n.name)
      const list = bySpace.get(space) ?? []
      list.push({ target, title: n.title, path: n.path })
      bySpace.set(space, list)
    }
    const sortedTags = [...tags].sort((a, b) => a.localeCompare(b))
    return (space: string): Completions => ({ notes: bySpace.get(space) ?? [], tags: sortedTags })
  }, [notes])

  /** The starter note, opened at a section; made first when the space
   * has none (someone deleted it, or this server never seeded one). */
  function openGuide(section?: string): void {
    const sp = defaultSpace()
    const want = sp ? `${sp}/Start here.md` : 'Start here.md'
    const found = notes.find((n) => n.path === want) ?? notes.find((n) => n.name === 'Start here.md')
    if (found) {
      openHit(found.id, section ?? null)
      return
    }
    api
      .guide(sp)
      .then((res) => {
        if (!res.id) {
          say('The guide is written; it shows up on the next scan.')
          return
        }
        void loadTree()
        openHit(res.id, section ?? null)
      })
      .catch((err: unknown) => {
        say(err instanceof ApiError ? err.message : 'Could not write the guide.')
      })
  }

  /** The space new things go into: the open note's, else the preferred
   * one from settings, else the first one. */
  function defaultSpace(): string {
    if (current.current) return current.current.space
    const pref = prefs.defaultSpace()
    if (pref && spaces?.some((s) => s.name === pref)) return pref
    return spaces?.[0]?.name ?? ''
  }

  /** The folder a new note lands in when none is named: beside the open
   * note, else the top of the default space. */
  function defaultDir(): string {
    if (current.current) return dirOf(current.current.path)
    return defaultSpace()
  }

  /** The daily note's space: the preference, else the same as new notes. */
  function dailySpace(): string {
    const pref = prefs.dailySpace()
    if (pref && spaces?.some((s) => s.name === pref)) return pref
    return defaultSpace()
  }

  const createNote = useCallback(
    async (path: string, retryOnTaken = false) => {
      let p = path.trim().replace(/^\/+/, '')
      if (!/\.(md|markdown|html?)$/i.test(p)) p += '.md'
      const title = baseOf(p).replace(/\.(md|markdown|html?)$/i, '')
      const seed = `# ${title}\n\n`
      try {
        const res = await api.createNote(p, seed)
        const tab = navigate(res.id, 'tab', { mode: 'edit' })
        if (tab) setFresh((f) => ({ key: tab.key, seq: (f?.seq ?? 0) + 1 }))
        void loadTree()
      } catch (err) {
        if (cache.networkDown(err)) {
          // The id comes from the server, so offline the note is queued
          // and created when the server answers again.
          void outbox.enqueue({ kind: 'create', path: p, content: seed })
          say(`Offline. ${p} is created when the connection returns.`)
        } else if (retryOnTaken && err instanceof ApiError && (err.status === 409 || err.status === 400)) {
          // Something on disk the tree has not seen yet holds the name.
          const m = /^(.*?)(?: (\d+))?\.md$/.exec(p)
          const n = m && m[2] ? Number(m[2]) + 1 : 2
          if (n < 100 && m) void createNote(`${m[1]} ${n}.md`, true)
          else say(err.message)
        } else {
          say(err instanceof ApiError ? err.message : 'Could not create the note.')
        }
      }
    },
    [navigate, loadTree, say],
  )

  /** New note, no questions asked: an untitled note in the folder, the
   * title focused. The file is named for the title once there is one. */
  function newNote(dir?: string): void {
    const d = dir ?? defaultDir()
    const taken = new Set(notes.filter((n) => dirOf(n.path) === d).map((n) => baseOf(n.path).toLowerCase()))
    let name = 'Untitled'
    for (let i = 2; taken.has(name.toLowerCase() + '.md'); i++) name = `Untitled ${i}`
    void createNote(d ? `${d}/${name}` : name, true)
  }

  function newNotePrompt(dir?: string): void {
    const initial = dir !== undefined ? (dir ? dir + '/' : '') : defaultDir() ? defaultDir() + '/' : ''
    setPalette({
      mode: 'prompt',
      placeholder: 'Path for the new note',
      initial,
      hint: 'A name, or a path inside the tree like projects/kiln. Folders that do not exist yet are created.',
      onSubmit: (v) => void createNote(v),
    })
  }

  const openDaily = useCallback(async () => {
    const date = today()
    try {
      const res = await api.daily(dailySpace(), date)
      if (res.created) void loadTree()
      const tab = navigate(res.id, 'tab', res.created ? { mode: 'edit' } : {})
      if (tab && res.created) setFresh((f) => ({ key: tab.key, seq: (f?.seq ?? 0) + 1 }))
    } catch (err) {
      if (cache.networkDown(err)) {
        void outbox.enqueue({ kind: 'daily', space: dailySpace(), date })
        say("Offline. Today's note opens when the connection returns.")
      } else {
        say(err instanceof ApiError ? err.message : 'Could not open the daily note.')
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navigate, loadTree, say, spaces])

  // Quick capture: one line onto the end of today's note, without
  // opening it. The share target lands its line the same way.
  const capture = useCallback(
    async (text: string) => {
      const block = composeShareBlock('', text, '')
      const space = dailySpace()
      try {
        const res = await api.daily(space, today())
        if (res.created) void loadTree()
        const { offline, undo } = await appendToNote(res.id, block)
        say(offline ? 'Added on this device. It merges when the server is back.' : "Added to today's note.", {
          label: 'Undo',
          run: () => {
            undo()
              .then(() => say('Removed.'))
              .catch(() => say('Could not remove the line; it is still in the note.'))
          },
        })
      } catch (err) {
        if (cache.networkDown(err)) {
          outbox.enqueueShare(space, '', text, '')
          say("Offline. The line lands in today's note when the connection returns.")
        } else {
          say(err instanceof Error ? err.message : 'Could not add the line.')
        }
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [loadTree, say, spaces],
  )

  function capturePrompt(): void {
    setPalette({
      mode: 'prompt',
      placeholder: 'Capture a line',
      initial: '',
      hint: "Goes to the end of today's note. Enter to add.",
      onSubmit: (v) => void capture(v),
    })
  }

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
  // The new heading reaches the index once the write-back has run, a few
  // seconds after the file moves, so the tree is read again twice.
  const onMoved = useCallback(
    (res: MoveResult) => {
      void loadTree()
      window.setTimeout(() => void loadTree(), 1500)
      window.setTimeout(() => void loadTree(), 5000)
      movedToast(res)
    },
    [loadTree, movedToast],
  )

  function renamePrompt(note?: NoteRef): void {
    const n = note ?? current.current
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

  /** The folder picker: the current folder in the input, to edit by
   * hand or leave alone, and the tree of folders under it to pick from.
   * A path that is not there yet is made on Enter; a bare name lands in
   * the thing's own space. */
  function folderPicker(placeholder: string, here: string, exclude: (path: string) => boolean, pick: (dir: string) => void): void {
    const items: PaletteItem[] = dirs
      .filter((d) => d.path === here || !exclude(d.path))
      .map((d) => ({
        id: d.path || '/',
        label: d.path === '' ? '/' : d.depth === 0 ? d.path + '/' : baseOf(d.path),
        path: d.path,
        depth: d.depth,
        here: d.path === here,
        run: () => pick(d.path),
      }))
    const spaceNames = new Set((spaces ?? []).map((sp) => sp.name))
    const space = here.split('/')[0] ?? ''
    // Typed from the root, unless the first part is not a space: then it
    // sits inside the thing's own space.
    const resolve = (q: string) => {
      const first = q.split('/')[0] ?? ''
      return resolveDir(spaceNames.has(first) || !space ? '' : space, q)
    }
    setPalette({
      mode: 'list',
      match: 'path',
      placeholder,
      initial: here,
      items,
      limit: 400,
      createHint: 'new folder',
      createLabel: (q) => `Make ${resolve(q) || q}/`,
      hint: 'Edit the path, or pick a folder below. A path that is not there yet is made.',
      onCreate: (q) => {
        const dir = resolve(q)
        if (!dir.includes('/')) {
          say(dir ? `${dir}/ is a space. A folder lives inside one, like ${dir}/archive.` : 'A folder lives inside a space.')
          return
        }
        if (dir === here) return
        if (exclude(dir)) {
          say('A folder cannot move into itself.')
          return
        }
        pick(dir)
      },
    })
  }

  function moveNotePicker(note?: NoteRef): void {
    const n = note ?? current.current
    if (!n) return
    const from = dirOf(n.path)
    folderPicker(`Move ${n.title || baseOf(n.path)} to`, from, (d) => d === from, (dir) => {
      void moveNote(n.id, n.path, dir ? `${dir}/${baseOf(n.path)}` : baseOf(n.path))
    })
  }

  /** Open a note with its title selected: rename it from the tree. A
   * double-click keeps the preview tab the first click opened. */
  function openTitle(id: string): void {
    const tab = navigate(id, 'tab', { mode: 'edit' })
    if (tab) setFresh((f) => ({ key: tab.key, seq: (f?.seq ?? 0) + 1 }))
  }

  // Deleting shows the note's inbound links first: whoever points at it
  // is about to hold an unresolved link until it comes back.
  const deleteNotePrompt = useCallback(
    (note?: NoteRef) => {
      const n = note ?? current.current
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
                prefs.forgetPin({ kind: 'note', id: n.id })
                workspace.closeNote(n.id)
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
    },
    [loadTree, say],
  )

  // --- folders -----------------------------------------------------------

  const createDir = useCallback(
    (path: string) => {
      api
        .createDir(path)
        .then(() => {
          void loadTree()
          say(`Made ${path}/.`)
        })
        .catch((err: unknown) => say(err instanceof ApiError ? err.message : 'Could not make the folder.'))
    },
    [loadTree, say],
  )

  // A new folder is an input row in the tree where it will sit. On a
  // phone, or with the sidebar away, the prompt asks instead.
  function newFolderPrompt(parent: string): void {
    if (!coarsePointer && sidebarShown) {
      setTreeEdit({ kind: 'new-dir', parent })
      return
    }
    setPalette({
      mode: 'prompt',
      placeholder: 'Name for the new folder',
      initial: '',
      hint: `A folder inside ${parent || 'the root'}. It is a real directory on disk.`,
      onSubmit: (v) => {
        const name = v.trim().replace(/^\/+|\/+$/g, '')
        if (!name) return
        createDir(parent ? `${parent}/${name}` : name)
      },
    })
  }

  const dirMovedToast = (from: string, res: { path: string; moved: number; rewritten: number; broken: number }) => {
    const n = res.rewritten
    const what = `${res.moved} note${res.moved === 1 ? '' : 's'}`
    say(
      n > 0
        ? `Moved ${what} to ${res.path}/. ${n} link${n === 1 ? '' : 's'} updated${res.broken ? `, ${res.broken} left unresolved` : ''}.`
        : `Moved ${what} to ${res.path}/.`,
    )
    prefs.repinDir(from, res.path)
    prefs.moveFolderState(from, res.path)
  }

  const moveDir = useCallback(
    async (from: string, to: string) => {
      try {
        const res = await api.moveDir(from, to)
        dirMovedToast(from, res)
        void loadTree()
        // A note open inside the folder has a new path now.
        if (current.current && current.current.path.startsWith(from + '/')) {
          current.current = null
          setRev((r) => r + 1)
        }
      } catch (err) {
        say(err instanceof ApiError ? err.message : `Could not move ${from}.`)
        void loadTree()
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [loadTree, say],
  )

  function renameDir(node: TreeNode, name: string): void {
    const parent = dirOf(node.path)
    if (!name || name === node.name) return
    void moveDir(node.path, parent ? `${parent}/${name}` : name)
  }

  // Renaming a folder happens in its row; the prompt is the phone's way.
  function renameDirPrompt(node: TreeNode): void {
    if (!coarsePointer && sidebarShown) {
      setTreeEdit({ kind: 'rename-dir', path: node.path })
      return
    }
    setPalette({
      mode: 'prompt',
      placeholder: 'New name for the folder',
      initial: node.name,
      select: [0, node.name.length],
      hint: 'Every note inside moves with it; wikilinks that point at them are rewritten.',
      onSubmit: (v) => renameDir(node, v.trim().replace(/^\/+|\/+$/g, '')),
    })
  }

  function moveDirPicker(node: TreeNode): void {
    const parent = dirOf(node.path)
    folderPicker(
      `Move ${node.name}/ to`,
      parent,
      (d) => d === parent || d === node.path || d.startsWith(node.path + '/') || d === '',
      (dir) => void moveDir(node.path, `${dir}/${node.name}`),
    )
  }

  function deleteDirPrompt(node: TreeNode): void {
    const inside = countNotes(node)
    setConfirmSpec({
      title: inside > 0 ? `Delete ${node.name}/ and the ${inside} note${inside === 1 ? '' : 's'} inside?` : `Delete the empty folder ${node.name}/?`,
      body:
        inside > 0
          ? 'Each note moves to the trash and can be restored for 30 days. Links pointing at them go unresolved until then.'
          : undefined,
      confirmLabel: 'Delete',
      danger: true,
      onConfirm: () => {
        api
          .deleteDir(node.path)
          .then((res) => {
            prefs.repinDir(node.path, null)
            for (const n of notes) if (n.path.startsWith(node.path + '/')) workspace.closeNote(n.id)
            void loadTree()
            say(
              res.deleted > 0
                ? `Deleted ${node.name}/. ${res.deleted} note${res.deleted === 1 ? ' is' : 's are'} in the trash.`
                : `Deleted ${node.name}/.`,
            )
          })
          .catch((err: unknown) => {
            say(err instanceof ApiError ? err.message : `Could not delete ${node.name}/.`)
            void loadTree()
          })
      },
    })
  }

  // --- pins --------------------------------------------------------------

  function pinNote(n: NoteRef): void {
    const on = prefs.togglePin({ kind: 'note', id: n.id })
    say(on ? `Pinned ${n.title || baseOf(n.path)}.` : `Unpinned ${n.title || baseOf(n.path)}.`)
  }

  function pinDir(node: TreeNode): void {
    const on = prefs.togglePin({ kind: 'dir', path: node.path })
    say(on ? `Pinned ${node.name}/.` : `Unpinned ${node.name}/.`)
  }

  // --- tree actions ------------------------------------------------------

  // The context menu for a row: right-click or ⋯ on a desktop, a long
  // press on a phone. Everything here is reachable elsewhere too; this is
  // the short way.
  function treeContext(target: TreeTarget, anchor: HTMLElement, at?: { x: number; y: number }): void {
    let items: Array<MenuItem | 'sep'>
    let title: string
    let subtitle: string | undefined
    if (target.kind === 'note') {
      const n = target.node
      const ref: NoteRef = { id: n.id ?? '', path: n.path, title: n.title || n.name }
      const pinned = prefs.isPinned({ kind: 'note', id: ref.id })
      title = ref.title
      subtitle = n.path
      items = [
        { id: 'open', label: 'Open', icon: 'book-open', run: () => navigate(ref.id) },
        ...(layout === 'phone' ? [] : [{ id: 'open-tab', label: 'Open in new tab', icon: 'layers' as const, run: () => navigate(ref.id, 'background') }]),
        ...(layout === 'desktop' ? [{ id: 'open-right', label: 'Open to the right', icon: 'columns' as const, run: () => navigate(ref.id, 'right') }] : []),
        { id: 'pin', label: pinned ? 'Unpin' : 'Pin to the top', icon: pinned ? 'pin-off' : 'pin', run: () => pinNote(ref) },
        'sep',
        { id: 'move', label: 'Move to a folder', icon: 'move', run: () => moveNotePicker(ref) },
        { id: 'rename', label: 'Rename or move by path', icon: 'pencil', run: () => renamePrompt(ref) },
        'sep',
        { id: 'delete', label: 'Delete', icon: 'trash', detail: 'to the trash', danger: true, run: () => deleteNotePrompt(ref) },
      ]
    } else if (target.kind === 'dir') {
      const n = target.node
      const pinned = prefs.isPinned({ kind: 'dir', path: n.path })
      title = n.name + '/'
      subtitle = n.path
      items = [
        { id: 'new', label: 'New note here', icon: 'file-plus', run: () => newNote(n.path) },
        { id: 'folder', label: 'New folder inside', icon: 'folder-plus', run: () => newFolderPrompt(n.path) },
        { id: 'pin', label: pinned ? 'Unpin' : 'Pin to the top', icon: pinned ? 'pin-off' : 'pin', run: () => pinDir(n) },
        { id: 'activity', label: 'Activity here', icon: 'history', run: () => openActivity(spaceOf(n.path), n.path.slice(spaceOf(n.path).length + 1)) },
        { id: 'tasks', label: 'Tasks here', icon: 'check-square', run: () => openTasksPage(spaceOf(n.path), n.path.slice(spaceOf(n.path).length + 1)) },
        'sep',
        { id: 'rename', label: 'Rename', icon: 'pencil', run: () => renameDirPrompt(n) },
        { id: 'move', label: 'Move to a folder', icon: 'move', run: () => moveDirPicker(n) },
        'sep',
        { id: 'delete', label: 'Delete', icon: 'trash', detail: 'notes to the trash', danger: true, run: () => deleteDirPrompt(n) },
      ]
    } else {
      title = target.name ? target.name + '/' : '/'
      const inside = foldersIn(target.name)
      items = [
        { id: 'new', label: 'New note here', icon: 'file-plus', run: () => newNote(target.name) },
        ...(target.name
          ? [
              { id: 'folder', label: 'New folder', icon: 'folder-plus' as const, run: () => newFolderPrompt(target.name) },
              { id: 'activity', label: 'Activity here', icon: 'history' as const, run: () => openActivity(target.name) },
              { id: 'tasks', label: 'Tasks here', icon: 'check-square' as const, run: () => openTasksPage(target.name) },
            ]
          : []),
        ...(inside.length > 0
          ? [
              { id: 'collapse', label: 'Collapse folders', icon: 'chevrons-down-up' as const, run: () => prefs.setFoldersOpen(inside, false) },
              { id: 'expand', label: 'Expand folders', icon: 'chevrons-up-down' as const, run: () => prefs.setFoldersOpen(inside, true) },
            ]
          : []),
      ]
    }
    const spec: MenuSpec = { anchor, items, label: 'tree actions', title }
    if (subtitle) spec.subtitle = subtitle
    if (at) spec.at = at
    setMenu(spec)
  }

  // Mod+E: the editor beside the render on a wide screen; on a phone,
  // in and out of the editor.
  const toggleSplit = useCallback(() => {
    const t = workspace.activeTab()
    if (!t || isPage(t.id)) return
    const m = t.mode
    workspace.setMode(t.key, layout === 'phone' ? (m === 'edit' ? 'read' : 'edit') : m === 'split' ? 'edit' : 'split')
  }, [layout])

  // --- tabs --------------------------------------------------------------

  /** Brings a tab to the front, unless its note is gone: then the tab
   * closes and says so. */
  const activateTab = useCallback(
    (key: string) => {
      const at = workspace.find(key)
      if (!at) return
      if (gone(at.tab.id)) {
        workspace.closeNote(at.tab.id)
        say(`${at.tab.title || 'That note'} is no longer there. A deleted note waits in the trash.`)
        return
      }
      histMode.current = 'replace'
      workspace.activate(key)
      setHit(null)
    },
    [gone, say],
  )

  const closeTab = useCallback((key: string) => {
    histMode.current = 'replace'
    workspace.close(key)
  }, [])

  /** Mod+W: the active tab closes; a pinned one stays. False when there
   * is nothing to close, so the browser does its own. */
  function closeActiveTab(): boolean {
    if (layout === 'phone') return false
    const t = workspace.activeTab()
    if (!t) return false
    if (t.pinned) {
      say('A pinned tab closes from its menu.')
      return true
    }
    closeTab(t.key)
    return true
  }

  function stepTab(dir: 1 | -1): void {
    const t = workspace.step(dir)
    if (t) activateTab(t.key)
  }

  function jumpTab(n: number): void {
    const t = workspace.nth(n)
    if (t) activateTab(t.key)
  }

  function reopenTab(): void {
    if (workspace.reopen()) histMode.current = 'push'
  }

  // --- panes -------------------------------------------------------------

  /** Focuses a pane; the note actions follow it at once. */
  function focusPane(i: number): void {
    if (i === workspace.get().focus) return
    current.current = paneNotes.current[i] ?? null
    workspace.focusPane(i)
  }

  // Where the caret was in each pane, so a pane focused from the keyboard
  // picks up where it was left: the editor, the title, a field.
  const lastFocus = useRef<Array<HTMLElement | null>>([null, null])

  /** Focuses a pane from the keyboard and puts the caret back. */
  function focusPaneKeys(i: number): void {
    if (ws.panes.length < 2) return
    focusPane(i)
    requestAnimationFrame(() => {
      const el = lastFocus.current[i]
      const pane = contentRef.current?.querySelectorAll<HTMLElement>(':scope > .pane')[i]
      const editor = pane?.querySelector<HTMLElement>('.cm-content')
      if (el?.isConnected && pane?.contains(el)) el.focus({ preventScroll: true })
      else if (editor) editor.focus({ preventScroll: true })
      // Reading: nothing to type into, and the other pane's caret must not keep the keys.
      else if (document.activeElement instanceof HTMLElement && !pane?.contains(document.activeElement)) document.activeElement.blur()
    })
  }

  /** Mod+\: the note to the right, or the right pane closed. The copy on
   * the right opens in the other mode: read beside edit. */
  function toggleSplitPane(): void {
    if (layout !== 'desktop') return
    if (ws.panes.length > 1) {
      histMode.current = 'replace'
      workspace.closePane(1)
      return
    }
    const t = workspace.activeTab()
    if (!t) return
    navigate(t.id, 'right', { mode: t.mode === 'read' ? 'edit' : 'read' })
  }

  function tabInfo(t: Tab): TabInfo {
    if (isPage(t.id)) return { ...pageLabel(routeOf(t.id)), gone: false }
    const n = byId.get(t.id)
    return { title: n?.title ?? t.title, kind: n?.kind, public: n?.public, gone: gone(t.id) }
  }

  function tabMenu(t: Tab, anchor: HTMLElement, at?: { x: number; y: number }): void {
    const where = workspace.find(t.key)
    if (!where) return
    const tabs = workspace.pane(where.pane).tabs
    const idx = tabs.findIndex((x) => x.key === t.key)
    const two = ws.panes.length > 1
    const other = where.pane === 0 ? 1 : 0
    const paneItems: Array<MenuItem | 'sep'> =
      layout === 'desktop'
        ? [
            'sep',
            { id: 'open-right', label: two ? 'Open in the other pane' : 'Open to the right', icon: 'columns', run: () => { focusPane(where.pane); navigate(t.id, 'right', { mode: t.mode }) } },
            {
              id: 'move-pane',
              label: two ? 'Move to the other pane' : 'Move to the right',
              icon: 'move',
              disabled: !two && tabs.length < 2,
              run: () => {
                histMode.current = 'replace'
                workspace.moveToPane(t.key, two ? other : 1)
              },
            },
          ]
        : []
    const items: Array<MenuItem | 'sep'> = [
      { id: 'close', label: 'Close', icon: 'x', detail: t.pinned ? undefined : label(keys.closeTabAlt), run: () => closeTab(t.key) },
      {
        id: 'others',
        label: 'Close others',
        disabled: tabs.every((x) => x.key === t.key || x.pinned),
        run: () => {
          histMode.current = 'replace'
          workspace.closeOthers(t.key)
        },
      },
      {
        id: 'right',
        label: 'Close to the right',
        disabled: !tabs.slice(idx + 1).some((x) => !x.pinned),
        run: () => {
          histMode.current = 'replace'
          workspace.closeRight(t.key)
        },
      },
      'sep',
      ...(t.preview ? [{ id: 'keep', label: 'Keep open', icon: 'check' as const, run: () => workspace.keep(t.key) }] : []),
      { id: 'pin', label: t.pinned ? 'Unpin tab' : 'Pin tab', icon: t.pinned ? ('pin-off' as const) : ('pin' as const), run: () => workspace.togglePin(t.key) },
      ...paneItems,
    ]
    const spec: MenuSpec = { anchor, items, label: 'tab actions', title: tabInfo(t).title || 'Untitled' }
    if (at) spec.at = at
    setMenu(spec)
  }

  // Right-click on a wikilink in a rendered note.
  const linkMenu = useCallback((id: string, anchor: HTMLElement, at: { x: number; y: number }) => {
    const title = titles.current.get(id)
    const spec: MenuSpec = {
      anchor,
      at,
      label: 'link actions',
      items: [
        { id: 'open', label: 'Open', icon: 'book-open', run: () => navigate(id) },
        { id: 'open-tab', label: 'Open in new tab', icon: 'layers', run: () => navigate(id, 'background') },
        ...(layoutNow.current === 'desktop' ? [{ id: 'open-right', label: 'Open to the right', icon: 'columns' as const, run: () => navigate(id, 'right') }] : []),
      ],
    }
    if (title) spec.title = title
    setMenu(spec)
  }, [navigate])

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

  // Search: the sidebar box on a desktop and a tablet, a page of its
  // own on a phone.
  function focusSearch(): void {
    if (layout === 'phone') {
      if (location.pathname !== '/search') history.pushState({ tab: null }, '', '/search')
      setPhoneSearch(true)
      setDrawer(false)
      return
    }
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
    const items = notes.map((n) => ({
      id: n.id,
      label: n.title,
      detail: n.tags.length > 0 ? `${n.path}  ${n.tags.map((t) => '#' + t).join(' ')}` : n.path,
      run: () => navigate(n.id),
    }))
    items.sort((a, b) => Number(recent.has(b.id)) - Number(recent.has(a.id)))
    setPalette({
      mode: 'list',
      placeholder: 'Open a note, or type #tag',
      items,
      onCreate: (q) => {
        const sp = defaultSpace()
        void createNote(q.includes('/') || !sp ? q : `${sp}/${q}`)
      },
    })
  }

  // Exports are downloads the token has to travel with, so they run
  // through the API wrapper rather than a plain navigation.
  function exportNote(note?: Note): void {
    const n = note ?? current.current
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

  // Help: how to do the everyday things, each opening the guide at its
  // section, then every shortcut in one list. The home screen does not
  // carry them.
  function showHelp(): void {
    const how: Array<[string, string, string]> = [
      ['Make a link to another note', 'type [[ or use the Link button', 'Links between notes'],
      ['Put a picture or file in a note', 'drag, paste, or the Attach button', 'Pictures and files'],
      ['Make a task list and tick it', '- [ ] on a line; tick it while reading', 'Tasks'],
      ['See every open task in one place', 'the tasks page, with filters', 'Tasks'],
      ['Tag a note', '#word anywhere in it', 'Tags'],
      ['Write something down fast', 'Capture, into today\'s note', 'Today and capture'],
      ['Find a note again', 'search, the switcher, tags, recents', 'Finding things'],
      ['Move a note or make a folder', 'drag in the sidebar, or Move in the menu', 'Folders and moving'],
      ['See what changed, or bring a note back', 'Details, and the trash', 'History'],
      ['Share a space with someone', 'People and Spaces in settings', 'Sharing a space'],
      ['Let an agent read and write notes', 'files, or MCP with a key', 'Agents'],
    ]
    const rows: Array<[string, string]> = [
      ['New note', label(keys.newNote)],
      ['Capture a line into today\'s note', label(keys.capture)],
      ["Today's note", label(keys.daily)],
      ['Open the tasks page', label(keys.tasks)],
      ['Open a note by name or #tag', label(keys.switcher)],
      ['Command palette', label(keys.palette)],
      ['Search', label(keys.search) + ' or /'],
      ['Edit the open note', 'E'],
      ['Back to reading', 'Esc'],
      ['Editor and preview side by side', label(keys.split)],
      ['Open a note in a new tab', `${isMac ? '⌘' : 'Ctrl'}-click or middle-click`],
      ['Next and previous tab', `${isMac ? '⌃Tab / ⌃⇧Tab' : 'Ctrl+Tab / Ctrl+Shift+Tab'}, or ${label(keys.nextTabAlt)} / ${label(keys.prevTabAlt)}`],
      ['Go to tab 1 to 8, or the last', `${isMac ? '⌥' : 'Alt+'}1 … ${isMac ? '⌥' : 'Alt+'}9, or ${isMac ? '⌘' : 'Ctrl+'}1 … 9`],
      ['Close the tab', `${label(keys.closeTab)} or ${label(keys.closeTabAlt)}`],
      ['Reopen the tab closed last', `${label(keys.reopenTab)} or ${label(keys.reopenTabAlt)}`],
      ['Open the note to the right, or close the right pane', label(keys.splitPane)],
      ['Open a note in the other pane', `${isMac ? '⌘⌥' : 'Ctrl+Alt+'}click`],
      ['Move focus to the other pane', `${label(keys.paneSwap)}, or ${label(keys.paneLeft)} / ${label(keys.paneRight)}`],
      ['Keep a preview tab open', 'double-click it, or edit the note'],
      ['Name a new note, then write', 'Enter in the title'],
      ['Link to a note while writing', '[[ then a name'],
      ['Undo and redo in the editor', `${label({ key: 'z', mod: true })} / ${label({ key: 'z', mod: true, shift: true })}`],
      ['Find in the open note', label({ key: 'f', mod: true })],
      ['Actions for a note or folder in the tree, or a tab', 'Right-click, or hold on a touch screen'],
      ['Close a dialog', 'Esc'],
    ]
    const items: PaletteItem[] = [
      { id: 'guide', label: 'Open the guide', detail: 'a note that shows everything by doing it', run: () => openGuide() },
      { id: 'what-changed', label: 'See what changed lately', detail: 'the activity page: who changed which notes, when', run: () => openActivity() },
      { id: 'tasks-page', label: 'Open the tasks page', detail: 'every open box across your spaces', run: () => openTasksPage() },
      ...how.map(([what, detail, section]) => ({ id: 'how:' + section, label: what, detail, hint: 'in the guide', run: () => openGuide(section) })),
      ...rows.map(([what, key]) => ({ id: what, label: what, hint: key, run: () => undefined })),
    ]
    setPalette({ mode: 'list', placeholder: 'Help: how do I…', items, limit: 40 })
  }

  function openPalette(): void {
    const items: PaletteItem[] = [
      { id: 'new', label: 'New note', hint: label(keys.newNote), run: () => newNote() },
      { id: 'capture', label: 'Capture a line', detail: "into today's note, without opening it", hint: label(keys.capture), run: capturePrompt },
      { id: 'daily', label: "Today's note", hint: label(keys.daily), run: () => void openDaily() },
      { id: 'tasks', label: 'Tasks', detail: 'every open box across your spaces', hint: label(keys.tasks), run: () => openTasksPage() },
      { id: 'open', label: 'Open a note', hint: label(keys.switcher), run: openSwitcher },
      { id: 'search', label: 'Search notes', hint: label(keys.search), run: focusSearch },
      { id: 'activity', label: 'What changed', detail: 'who changed which notes, when', run: () => openActivity() },
      { id: 'new-path', label: 'New note at a path', detail: 'name the file and folder yourself', run: () => newNotePrompt() },
      { id: 'new-folder', label: 'New folder', detail: `in ${defaultDir() || 'the root'}`, run: () => newFolderPrompt(defaultDir()) },
    ]
    if (!narrow) {
      items.push({ id: 'sidebar', label: collapsed ? 'Show the sidebar' : 'Hide the sidebar', run: toggleSidebar })
    }
    if (dirs.some((d) => d.depth > 0)) {
      items.push({ id: 'collapse-all', label: 'Collapse every folder', detail: 'in the sidebar', run: () => prefs.setFoldersOpen(foldersIn(null), false) })
      items.push({ id: 'expand-all', label: 'Expand every folder', detail: 'in the sidebar', run: () => prefs.setFoldersOpen(foldersIn(null), true) })
    }
    if (current.current?.kind === 'md') {
      const shown = layout === 'phone' && mode === 'split' ? 'read' : mode
      if (shown !== 'read') items.push({ id: 'read', label: 'Read this note', hint: 'Esc', run: () => setMode('read') })
      if (shown !== 'edit') items.push({ id: 'edit', label: 'Edit this note', hint: 'E', run: () => setMode('edit') })
      if (layout !== 'phone' && shown !== 'split') {
        items.push({ id: 'split', label: 'Editor and preview side by side', hint: label(keys.split), run: () => setMode('split') })
      }
    }
    const tab = layout === 'phone' ? null : workspace.activeTab()
    if (tab) {
      if (tab.preview) items.push({ id: 'keep-tab', label: 'Keep this tab open', detail: 'the preview tab becomes a normal one', run: () => workspace.keep(tab.key) })
      items.push({ id: 'close-tab', label: 'Close this tab', hint: label(keys.closeTabAlt), run: () => closeTab(tab.key) })
      items.push({ id: 'pin-tab', label: tab.pinned ? 'Unpin this tab' : 'Pin this tab', detail: 'pinned tabs sit leftmost', run: () => workspace.togglePin(tab.key) })
    }
    if (layout !== 'phone') items.push({ id: 'reopen-tab', label: 'Reopen closed tab', hint: label(keys.reopenTabAlt), run: reopenTab })
    if (layout === 'desktop' && ws.panes.length > 1) {
      items.push({ id: 'close-pane', label: 'Close the right pane', detail: 'its tabs can be reopened', hint: label(keys.splitPane), run: toggleSplitPane })
    } else if (layout === 'desktop' && tab) {
      items.push({ id: 'split-pane', label: 'Open this note to the right', detail: 'a second pane beside this one', hint: label(keys.splitPane), run: toggleSplitPane })
    }
    if (current.current) {
      const n = current.current
      const pinned = prefs.isPinned({ kind: 'note', id: n.id })
      items.push({ id: 'pin', label: pinned ? 'Unpin this note' : 'Pin this note', detail: pinned ? 'from the top of the sidebar' : 'to the top of the sidebar', run: () => pinNote(n) })
      if (n.role !== 'viewer') {
        items.push({ id: 'move', label: 'Move this note to a folder', detail: dirOf(n.path) || '/', run: () => moveNotePicker() })
        items.push({ id: 'rename', label: 'Rename or move this note by path', detail: n.path, run: () => renamePrompt() })
        items.push({ id: 'delete', label: 'Delete this note', detail: 'moves it to the trash', run: () => deleteNotePrompt() })
      }
    }
    items.push({ id: 'tags', label: 'Tags', detail: 'every #tag and the notes carrying it', run: () => openTags() })
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
    if (net.canInstall) {
      items.push({ id: 'install', label: 'Install app', detail: 'add to the home screen', run: () => void pwa.promptInstall() })
    }
    items.push({ id: 'theme', label: 'Theme', detail: themePref, run: () => setThemeNext() })
    items.push({ id: 'open-in', label: 'Open notes in', detail: openLabel(openPref), run: setOpenPrefNext })
    items.push({ id: 'live', label: 'Hide markdown syntax while editing', detail: live ? 'on' : 'off', run: toggleLive })
    items.push({ id: 'settings-appearance', label: 'Appearance', detail: 'text size, line width, density', run: () => openSettings('appearance') })
    items.push({ id: 'keys', label: 'Help and keyboard shortcuts', detail: 'how to link, add pictures, tick tasks, share', run: showHelp })
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
        { id: 'keys', label: 'Help and shortcuts', icon: 'keyboard', run: showHelp },
        ...(net.canInstall
          ? ['sep' as const, { id: 'install', label: 'Install app', icon: 'share' as const, run: () => void pwa.promptInstall() }]
          : []),
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

  const focusSide = focusPaneKeys
  const actions = useRef({ openPalette, openSwitcher, newNote, openDaily, focusSearch, toggleSplit, capturePrompt, openTasksPage, closeActiveTab, stepTab, jumpTab, reopenTab, toggleSplitPane, focusSide })
  actions.current = { openPalette, openSwitcher, newNote, openDaily, focusSearch, toggleSplit, capturePrompt, openTasksPage, closeActiveTab, stepTab, jumpTab, reopenTab, toggleSplitPane, focusSide }
  const tabKeys = layout !== 'phone'
  const paneKeys = layout === 'desktop'

  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      const a = actions.current
      if (ev.key === 'Escape' && palette) {
        setPalette(null)
        return
      }
      let handled = true
      const digit = tabKeys ? tabDigit(ev) : 0
      if (matches(ev, keys.palette)) a.openPalette()
      else if (matches(ev, keys.switcher)) a.openSwitcher()
      else if (matches(ev, keys.newNote)) a.newNote()
      else if (matches(ev, keys.daily)) void a.openDaily()
      else if (matches(ev, keys.capture)) a.capturePrompt()
      else if (matches(ev, keys.tasks)) a.openTasksPage()
      else if (matches(ev, keys.search)) a.focusSearch()
      else if (matches(ev, keys.split)) a.toggleSplit()
      else if (paneKeys && matches(ev, keys.splitPane)) a.toggleSplitPane()
      else if (paneKeys && matches(ev, keys.paneLeft)) a.focusSide(0)
      else if (paneKeys && matches(ev, keys.paneRight)) a.focusSide(1)
      else if (paneKeys && matches(ev, keys.paneSwap)) a.focusSide(workspace.get().focus === 0 ? 1 : 0)
      else if (tabKeys && ev.ctrlKey && !ev.altKey && !ev.metaKey && ev.key === 'Tab') a.stepTab(ev.shiftKey ? -1 : 1)
      else if (tabKeys && matches(ev, keys.nextTabAlt)) a.stepTab(1)
      else if (tabKeys && matches(ev, keys.prevTabAlt)) a.stepTab(-1)
      else if (digit) a.jumpTab(digit)
      else if (tabKeys && (matches(ev, keys.closeTab) || matches(ev, keys.closeTabAlt))) handled = a.closeActiveTab()
      else if (tabKeys && (matches(ev, keys.reopenTab) || matches(ev, keys.reopenTabAlt))) a.reopenTab()
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
  }, [palette, tabKeys, paneKeys])

  // --- render ------------------------------------------------------------

  // A new note's title is asked for once: leaving its tab (or its pane)
  // ends that, so coming back does not select the title again.
  useEffect(() => {
    if (fresh && active?.key !== fresh.key) setFresh(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active?.key, active?.id])

  // --- panes: render ------------------------------------------------------

  const contentRef = useRef<HTMLElement>(null)
  const [dividerLive, setDividerLive] = useState<number | null>(null) // while dragging
  const [edge, setEdge] = useState(false) // a tab is held over the right edge

  // The divider drags between the panes, each at least 360px wide, and
  // snaps to halves within 40px of the middle.
  function dividerDown(ev: PointerEvent): void {
    const el = contentRef.current
    if (!el || ev.button !== 0) return
    ev.preventDefault()
    const bar = ev.currentTarget as HTMLElement
    bar.setPointerCapture(ev.pointerId)
    const rect = el.getBoundingClientRect()
    let f = ws.divider
    const move = (e: PointerEvent) => {
      let x = Math.min(Math.max(e.clientX - rect.left, PANE_MIN), rect.width - PANE_MIN)
      if (Math.abs(x - rect.width / 2) <= PANE_SNAP) x = rect.width / 2
      f = x / rect.width
      setDividerLive(f)
    }
    const up = () => {
      bar.removeEventListener('pointermove', move)
      bar.removeEventListener('pointerup', up)
      bar.removeEventListener('pointercancel', up)
      setDividerLive(null)
      workspace.setDivider(f)
    }
    bar.addEventListener('pointermove', move)
    bar.addEventListener('pointerup', up)
    bar.addEventListener('pointercancel', up)
  }

  // Each pane reports its note; the focused one's is "this note".
  const onNotes = useMemo(
    () =>
      [0, 1].map((i) => (n: Note | null) => {
        paneNotes.current[i] = n
        if (i === workspace.get().focus) current.current = n
        if (!n) return
        prefs.touchRecent(n.id)
        workspace.setTitles(new Map([[n.id, n.title]]))
      }),
    [],
  )
  useEffect(() => {
    current.current = paneNotes.current[ws.focus] ?? null
  }, [ws.focus])

  function paneView(i: number) {
    const p = ws.panes[i] ?? { tabs: [], active: null }
    const tab = tabIn(i)
    const f = dividerLive ?? ws.divider
    const style = split
      ? i === 0
        ? `flex: 0 0 clamp(${PANE_MIN}px, calc(${(f * 100).toFixed(3)}% - 3px), calc(100% - ${PANE_MIN + 6}px))`
        : 'flex: 1 1 0'
      : undefined
    return (
      <div
        key={'pane' + i}
        class={'pane' + (split && i === ws.focus ? ' pane-focused' : '')}
        style={style}
        onPointerDownCapture={() => focusPane(i)}
        onFocusIn={(ev) => {
          lastFocus.current[i] = ev.target as HTMLElement
        }}
      >
        {layout !== 'phone' && p.tabs.length > 0 && (
          <TabStrip
            tabs={p.tabs}
            active={tab?.key ?? null}
            info={tabInfo}
            onActivate={activateTab}
            onClose={closeTab}
            onKeep={(k) => workspace.keep(k)}
            onMenu={tabMenu}
            onMove={(k, at) => {
              histMode.current = 'replace'
              workspace.move(k, at, i)
            }}
            onDropNote={(id, title, at) => {
              const t = workspace.open(id, 'background', { mode: prefs.openMode(), title }, i)
              workspace.move(t.key, at, i)
              activateTab(t.key)
            }}
          />
        )}
        {tab && (isPage(tab.id) ? renderPage(tab.id) : renderNote(tab, i))}
        {!tab && homeView()}
      </div>
    )
  }

  function renderNote(tab: Tab, i: number) {
    const mine = () => paneNotes.current[i] ?? undefined
    return (
      <NotePage
        key={`${tab.key}:${tab.id}:${rev}:${hit?.key === tab.key ? hit.seq : 0}`}
        id={tab.id}
        layout={layout}
        focused={i === ws.focus}
        mode={tab.mode}
        onMode={(m) => workspace.setMode(tab.key, m)}
        live={live}
        onEditing={setEditing}
        onOpen={navigate}
        onLinkMenu={layout === 'phone' ? undefined : linkMenu}
        onEdited={() => {
          // Typing in a preview tab keeps it.
          if (workspace.find(tab.key)?.tab.preview) workspace.keep(tab.key)
        }}
        scroll={tab.scroll}
        onScroll={(y) => workspace.setScroll(tab.key, y)}
        onNote={onNotes[i] ?? onNotes[0]!}
        onToast={say}
        onMenu={setMenu}
        fresh={fresh?.key === tab.key}
        freshSeq={fresh?.seq ?? 0}
        onDelete={() => deleteNotePrompt(mine())}
        onRename={() => renamePrompt(mine())}
        onMove={() => moveNotePicker(mine())}
        hasDir={hasDir}
        onExport={() => exportNote(mine())}
        onShared={() => void loadTree()}
        onMoved={onMoved}
        onTag={openTag}
        pinned={pinList.some((p) => p.kind === 'note' && p.id === tab.id)}
        onPin={() => {
          const n = mine()
          if (n) pinNote(n)
        }}
        highlight={hit?.key === tab.key ? hit.text : null}
        taskLine={hit?.key === tab.key ? hit.line : null}
        lookup={completions}
      />
    )
  }

  function homeView() {
    return (
      <Home
        notes={notes}
        pins={pinList}
        spaces={spaces ?? []}
        loading={spaces === null}
        openTasks={openTasks}
        conflicts={status?.conflicts ?? 0}
        onOpen={navigate}
        onNew={() => newNote()}
        onCapture={capturePrompt}
        onDaily={() => void openDaily()}
        onTasks={() => openTasksPage()}
        onGuide={() => openGuide()}
        onActivity={() => openActivity()}
        onConflicts={() => openSettings('data')}
        onInstall={net.canInstall ? () => void pwa.promptInstall() : null}
      />
    )
  }

  /** A page tab: everything that is not a note, by its path. */
  function renderPage(path: string) {
    const r = routeOf(path)
    if (r.kind === 'home') return homeView()
    return (
      <>
        {r.kind === 'links' && <LinksReport onOpen={navigate} />}
        {r.kind === 'trash' && (
          <TrashPage onOpen={navigate} onToast={(m) => say(m)} confirm={setConfirmSpec} onChanged={() => void loadTree()} />
        )}
        {r.kind === 'tags' && <TagsIndex onTag={openTag} />}
        {r.kind === 'tag' && <TagPage tag={r.tag} onOpen={navigate} onAll={() => openTags()} />}
        {r.kind === 'tasks' && (
          <TasksPage
            key={r.space + ':' + r.tag + ':' + r.path}
            space={r.space}
            tag={r.tag}
            path={r.path}
            spaces={spaceList}
            notes={notes}
            onOpen={openTask}
            onNavigate={(space, tag, path) => {
              const q = new URLSearchParams()
              if (space) q.set('space', space)
              if (tag) q.set('tag', tag)
              if (path) q.set('path', path)
              const qs = q.toString()
              openPage('/tasks' + (qs ? '?' + qs : ''))
            }}
            onToast={say}
          />
        )}
        {r.kind === 'activity' && (
          <ActivityPage
            key={r.space + ':' + r.path}
            space={r.space}
            path={r.path}
            spaces={spaceList}
            onOpen={navigate}
            onNavigate={openActivity}
            onToast={say}
            onChanged={() => { void loadTree(); void loadSpaces() }}
          />
        )}
        {r.kind === 'share' && (
          <SharePage notes={notes} dailySpace={dailySpace()} onOpen={navigate} onToast={(m) => say(m)} search={new URL(path, location.origin).search} />
        )}
        {r.kind === 'settings' && (
          <SettingsPage
            section={r.section}
            layout={layout}
            status={status}
            spaces={spaceList}
            notes={notes}
            onSection={(s) => openSettings(s)}
            onToast={(m) => say(m)}
            confirm={setConfirmSpec}
            onChanged={() => { void loadTree(); void loadSpaces() }}
            onOpen={navigate}
            onOpenTrash={() => openTrash()}
            onSignOut={onSignOut}
            onStatus={() => void loadStatus()}
          />
        )}
      </>
    )
  }

  const searching = query.trim() !== ''
  const selected = route.kind === 'note' ? route.id : null

  // While the phone keyboard is up the shell is sized to what is left
  // above it, so the formatting bar and the caret stay in view.
  const phoneEditing = layout === 'phone' && editing
  const viewport = useVisualViewport(phoneEditing)
  const shellClass = ['shell', layout, sidebarShown ? 'sidebar-open' : 'sidebar-closed', viewport ? 'kb' : ''].join(' ')
  const shellStyle = viewport ? `height:${viewport.height}px;top:${viewport.top}px` : undefined

  // One chip for the offline and queued state, whichever applies.
  const queuedLabel = box.pending > 0 ? `${box.pending} queued` : ''
  const netLabel = net.offline
    ? queuedLabel
      ? `Offline · ${queuedLabel}`
      : 'Offline'
    : box.sending
      ? 'Sending…'
      : queuedLabel
  const netTitle = net.offline
    ? 'Changes are kept on this device and sent when the connection returns.'
    : 'Queued changes replay in order when the connection returns.'

  // The phone's search page takes the whole screen, bars included.
  if (layout === 'phone' && route.kind === 'search') {
    return (
      <div class={shellClass}>
        <SearchPage status={status} onOpen={openHit} onClose={() => history.back()} />
        {toast && <ToastView toast={toast} onClose={() => setToast(null)} />}
      </div>
    )
  }

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
          <img class="mark" src="/icon-192.png" alt="" width={20} height={20} />
          YANA/
        </a>
        {netLabel && (
          <span class={'net-chip' + (net.offline ? ' offline' : '')} title={netTitle}>
            <span class="sync-dot" />
            {netLabel}
          </span>
        )}
        <span class="spacer" />
        {layout !== 'phone' && (
          <>
            <button type="button" class="btn" title={`New note (${label(keys.newNote)})`} onClick={() => newNote()}>
              <Icon name="plus" />
              New
            </button>
            <button type="button" class="btn" title={`Capture a line into today's note (${label(keys.capture)})`} onClick={capturePrompt}>
              <Icon name="capture" />
              Capture
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
      {net.updateReady && (
        <div class="update-bar" role="status">
          <span>A new version is ready.</span>
          <button type="button" class="btn small" onClick={() => pwa.reloadForUpdate()}>
            Reload
          </button>
        </div>
      )}
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
              onFocus={() => { if (layout === 'phone') focusSearch() }}
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
              <SearchResults query={query} regex={regex} onOpen={openHit} />
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
                  <>
                    {staleTree && <p class="tree-offline">Offline. This is the last saved tree.</p>}
                    <Tree
                      spaces={spaces}
                      selected={selected}
                      pins={pinList}
                      onOpen={navigate}
                      onOpenTitle={openTitle}
                      onMove={(id, from, dir) => void moveNote(id, from, dir ? `${dir}/${baseOf(from)}` : baseOf(from))}
                      onMoveDir={(path, dir) => void moveDir(path, `${dir}/${baseOf(path)}`)}
                      onNew={newNote}
                      onNewFolder={newFolderPrompt}
                      onRenameFolder={renameDirPrompt}
                      edit={treeEdit}
                      onCreateDir={createDir}
                      onRenameDir={renameDir}
                      onEditDone={() => setTreeEdit(null)}
                      onContext={treeContext}
                    />
                  </>
                )}
              </nav>
            )}
          </div>
          {!searching && (
            <nav class="sidebar-nav" aria-label="more">
              <a class={'sidebar-link' + (route.kind === 'tasks' ? ' selected' : '')} href="/tasks" onClick={(ev) => { ev.preventDefault(); openTasksPage() }}>
                <Icon name="check-square" />
                Tasks
                {openTasks !== null && openTasks > 0 && <span class="sidebar-count">{openTasks}</span>}
              </a>
              <a class={'sidebar-link' + (route.kind === 'tags' || route.kind === 'tag' ? ' selected' : '')} href="/tags" onClick={(ev) => { ev.preventDefault(); openTags() }}>
                <Icon name="tag" />
                Tags
              </a>
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
        <main
          class={'content' + (split ? ' panes-split' : '')}
          ref={contentRef}
          onDragOver={(ev) => {
            // A tab held near the right edge makes the second pane.
            const el = contentRef.current
            if (layout !== 'desktop' || ws.panes.length > 1 || !el || !ev.dataTransfer?.types.includes(TAB_DRAG)) return
            const near = ev.clientX > el.getBoundingClientRect().right - 120
            if (near !== edge) setEdge(near)
            if (near) {
              ev.preventDefault()
              ev.dataTransfer.dropEffect = 'move'
            }
          }}
          onDragLeave={(ev) => {
            if (!(ev.currentTarget as HTMLElement).contains(ev.relatedTarget as Node | null)) setEdge(false)
          }}
          onDrop={(ev) => {
            if (!edge) return
            setEdge(false)
            const raw = ev.dataTransfer?.getData(TAB_DRAG)
            if (!raw) return
            ev.preventDefault()
            try {
              const { key } = JSON.parse(raw) as { key: string }
              histMode.current = 'replace'
              workspace.moveToPane(key, 1)
            } catch {
              // not ours
            }
          }}
        >
          {paneView(0)}
          {split && (
            <div
              class="pane-divider"
              role="separator"
              aria-orientation="vertical"
              aria-label="Resize the panes"
              title="Drag to resize; double-click for halves"
              onPointerDown={dividerDown}
              onDblClick={() => workspace.setDivider(0.5)}
            />
          )}
          {split && paneView(1)}
          {edge && <div class="pane-drop-edge" aria-hidden="true" />}
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
          <button type="button" class="bottombar-btn accent" onClick={capturePrompt}>
            <Icon name="capture" size={20} />
            Capture
          </button>
          <button type="button" class="bottombar-btn" onClick={() => void openDaily()}>
            <Icon name="calendar" size={20} />
            Today
          </button>
          <button type="button" class={'bottombar-btn' + (route.kind === 'tasks' ? ' on' : '')} onClick={() => openTasksPage()}>
            <Icon name="check-square" size={20} />
            Tasks
            {openTasks !== null && openTasks > 0 && <span class="bottombar-badge">{openTasks > 99 ? '99+' : openTasks}</span>}
          </button>
          <button type="button" class="bottombar-btn" onClick={() => newNote()}>
            <Icon name="plus" size={20} />
            New
          </button>
        </nav>
      )}
      {palette && <Palette spec={palette} onClose={() => setPalette(null)} />}
      {menu && <Menu spec={menu} onClose={() => setMenu(null)} />}
      {confirmSpec && <Confirm spec={confirmSpec} onClose={() => setConfirmSpec(null)} />}
      {toast && <ToastView toast={toast} onClose={() => setToast(null)} />}
    </div>
  )
}

function ToastView({ toast, onClose }: { toast: Toast; onClose: () => void }) {
  return (
    <div class="toast" role="status">
      <span class="toast-msg">{toast.msg}</span>
      {toast.action && (
        <button
          type="button"
          class="toast-action"
          onClick={() => {
            onClose()
            toast.action?.run()
          }}
        >
          {toast.action.label}
        </button>
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

/** Notes under a directory node, at any depth. */
function countNotes(n: TreeNode): number {
  let c = 0
  for (const ch of n.children ?? []) c += ch.type === 'note' ? 1 : countNotes(ch)
  return c
}

interface HomeProps {
  notes: FlatNote[]
  pins: prefs.Pin[]
  spaces: SpaceTree[]
  loading: boolean
  /** Open tasks across the account's spaces, when the count is in. */
  openTasks: number | null
  /** Conflict copies waiting in the caller's spaces. */
  conflicts: number
  onOpen: (id: string, how?: OpenHow) => void
  onNew: () => void
  onCapture: () => void
  onDaily: () => void
  /** The tasks page. */
  onTasks: () => void
  /** The starter note: opened, or made first. */
  onGuide: () => void
  /** The activity feed. */
  onActivity: () => void
  /** The Data page, where the conflicts are listed. */
  onConflicts: () => void
  /** Offered when the browser made an install prompt available. */
  onInstall: (() => void) | null
}

// The home page: the three things people come here to do, each with a
// line saying what it is, then what they pinned and what they opened
// last. Shortcuts are in the account menu and the palette.
function Home({ notes, pins, spaces, loading, openTasks, conflicts, onOpen, onNew, onCapture, onDaily, onTasks, onGuide, onActivity, onConflicts, onInstall }: HomeProps) {
  const byId = useMemo(() => new Map(notes.map((n) => [n.id, n])), [notes])
  const recent = prefs.recents().map((id) => byId.get(id)).filter((n): n is FlatNote => Boolean(n))
  const pinned = pins
    .map((p) => {
      if (p.kind === 'note') {
        const n = byId.get(p.id)
        return n ? { key: 'n' + n.id, title: n.title, path: n.path, run: () => onOpen(n.id) } : null
      }
      const exists = folders(spaces).some((d) => d.path === p.path)
      return exists ? { key: 'd' + p.path, title: baseOf(p.path) + '/', path: p.path, run: null } : null
    })
    .filter((x): x is NonNullable<typeof x> => x !== null)
  const guide = notes.find((n) => n.name === 'Start here.md')

  return (
    <div class="home">
      <img class="home-mark" src="/icon-192.png" alt="" width={96} height={96} />
      <h1 class="home-title">YANA/</h1>
      <p class="home-sub">Everything you expect. Nothing you don't.</p>
      <div class="home-actions">
        <button type="button" class="home-action primary" onClick={onNew}>
          <Icon name="plus" size={18} />
          <span class="home-action-text">
            <span class="home-action-title">New note</span>
            <span class="home-action-sub">A blank page; name it and write.</span>
          </span>
        </button>
        <button type="button" class="home-action" onClick={onCapture}>
          <Icon name="capture" size={18} />
          <span class="home-action-text">
            <span class="home-action-title">Capture</span>
            <span class="home-action-sub">One line into today's note, without opening it.</span>
          </span>
        </button>
        <button type="button" class="home-action" onClick={onDaily}>
          <Icon name="calendar" size={18} />
          <span class="home-action-text">
            <span class="home-action-title">Today</span>
            <span class="home-action-sub">Today's note, made if it is not there yet.</span>
          </span>
        </button>
        <button type="button" class="home-action" onClick={onTasks}>
          <Icon name="check-square" size={18} />
          <span class="home-action-text">
            <span class="home-action-title">
              Tasks{openTasks !== null && openTasks > 0 ? <span class="home-action-count">{openTasks}</span> : null}
            </span>
            <span class="home-action-sub">
              {openTasks === null ? 'Every open box across your spaces.' : openTasks === 0 ? 'Nothing open. Write `- [ ]` on a line to make one.' : `${openTasks} open across your spaces.`}
            </span>
          </span>
        </button>
        {onInstall && (
          <button type="button" class="home-action" onClick={onInstall}>
            <Icon name="share" size={18} />
            <span class="home-action-text">
              <span class="home-action-title">Install app</span>
              <span class="home-action-sub">On the home screen, and it works offline.</span>
            </span>
          </button>
        )}
      </div>
      <p class="home-guide">
        <a href={guide ? `/n/${guide.id}` : '#guide'} onClick={(ev) => { ev.preventDefault(); onGuide() }}>
          <Icon name="info" size={14} />
          {guide ? 'Start here: how links, pictures, tasks and tags work' : 'New here? Open the guide'}
        </a>
      </p>
      {conflicts > 0 && (
        <p class="home-conflicts">
          <a href="/settings/data" onClick={(ev) => { ev.preventDefault(); onConflicts() }}>
            <Icon name="alert" size={14} />
            {conflicts} {conflicts === 1 ? 'conflict copy waits' : 'conflict copies wait'} — two writes met the same
            path
          </a>
        </p>
      )}
      <WhatsChanged spaceNames={spaces.map((s) => s.name)} ready={!loading} onOpen={onActivity} />
      {loading ? (
        <p class="muted">Loading the tree…</p>
      ) : notes.length === 0 ? (
        <p class="muted">No notes yet. Start one above, or drop a markdown file into the notes directory; it shows up on the next scan. The guide is a good first note.</p>
      ) : (
        <>
          {pinned.length > 0 && (
            <section class="recents" aria-label="pinned">
              <h2 class="section-title">Pinned</h2>
              <ul class="recents-list">
                {pinned.map((p) => (
                  <li key={p.key}>
                    {p.run ? (
                      <a class="recent" href={`/n/${p.key.slice(1)}`} onClick={(ev) => { ev.preventDefault(); p.run() }}>
                        <span class="recent-title">{p.title}</span>
                        <span class="recent-path">{p.path}</span>
                      </a>
                    ) : (
                      <span class="recent static">
                        <span class="recent-title">
                          <Icon name="folder" size={14} /> {p.title}
                        </span>
                        <span class="recent-path">{p.path}</span>
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </section>
          )}
          {recent.length > 0 ? (
            <section class="recents" aria-label="recently opened">
              <h2 class="section-title">Recent</h2>
              <ul class="recents-list">
                {recent.map((n) => (
                  <li key={n.id}>
                    <a class="recent" href={`/n/${n.id}`} {...openProps((how) => onOpen(n.id, how))}>
                      <span class="recent-title">{n.title}</span>
                      <span class="recent-path">{n.path}</span>
                    </a>
                  </li>
                ))}
              </ul>
            </section>
          ) : (
            pinned.length === 0 && <p class="muted">Pick a note from the sidebar. The ones you open show up here.</p>
          )}
        </>
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
