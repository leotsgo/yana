// The app shell: top bar, sidebar (search, tree or results, nav), the open
// note or report, and the overlays (command palette, quick switcher,
// prompts, menus). Three layouts share this one tree: on phones the
// sidebar is a drawer and a bar runs along the bottom; on tablets the
// drawer stays but the top bar has room for actions; on desktops the
// sidebar is a column that can be collapsed. Global hotkeys live here
// too; see hotkeys.ts for the bindings. The everyday actions — new note
// without a path, quick capture into today's note, pins, tags, the tree
// actions behind a right-click or a long press — are wired here as well.

import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks'

import { api, ApiError, baseOf, dirOf, saveBlob } from './api'
import type { MoveResult, Note, SpaceInfo, SpaceTree, Status, TreeNode } from './api'
import * as auth from './auth'
import * as cache from './cache'
import { Confirm } from './confirm'
import type { ConfirmSpec } from './confirm'
import { isEditable, keys, label, matches } from './hotkeys'
import { Icon } from './icons'
import { useVisualViewport } from './keyboard'
import { coarsePointer, useLayout } from './layout'
import { renderUnresolvedReport } from './links'
import { Menu } from './menu'
import type { MenuItem, MenuSpec } from './menu'
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
import { TrashPage } from './trash'
import { Tree, flatten, folders } from './tree'
import type { FlatNote, TreeEdit, TreeTarget } from './tree'

type Route =
  | { kind: 'home' }
  | { kind: 'note'; id: string }
  | { kind: 'share' }
  | { kind: 'links' }
  | { kind: 'trash' }
  | { kind: 'tags' }
  | { kind: 'tag'; tag: string }
  | { kind: 'search' }
  | { kind: 'settings'; section: Section | null }

function parseRoute(): Route {
  if (location.pathname === '/share') return { kind: 'share' }
  if (location.pathname === '/links') return { kind: 'links' }
  if (location.pathname === '/trash') return { kind: 'trash' }
  if (location.pathname === '/tags') return { kind: 'tags' }
  if (location.pathname === '/search') return { kind: 'search' }
  if (location.pathname === '/settings') return { kind: 'settings', section: null }
  const tg = location.pathname.match(/^\/tags\/(.+)$/)
  if (tg && tg[1]) return { kind: 'tag', tag: decodeURIComponent(tg[1]).toLowerCase() }
  const st = location.pathname.match(/^\/settings\/([a-z]+)$/)
  if (st && st[1]) return { kind: 'settings', section: isSection(st[1]) ? st[1] : null }
  const m = location.pathname.match(/^\/n\/([0-9A-Za-z]{26})$/)
  return m && m[1] ? { kind: 'note', id: m[1] } : { kind: 'home' }
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
  const [route, setRoute] = useState<Route>(parseRoute)
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
  const [mode, setMode] = useState<prefs.OpenMode>(prefs.openMode)
  const [openPref, setOpenPref] = useState<prefs.OpenMode>(prefs.openMode)
  const [live, setLive] = useState(prefs.livePreview)
  const [editing, setEditing] = useState(false) // the phone is showing an editor
  const [collapsed, setCollapsed] = useState(prefs.sidebarCollapsed) // desktop column
  const [drawer, setDrawer] = useState(false) // phone and tablet
  // The note whose title should be focused: a new one, or one a person
  // double-clicked in the tree. The count makes a repeat on the open note count.
  const [fresh, setFresh] = useState<{ id: string; seq: number } | null>(null)
  const [treeEdit, setTreeEdit] = useState<TreeEdit | null>(null) // a folder input open in the tree
  const [themePref, setThemePref] = useState(prefs.theme)
  const [pinList, setPinList] = useState(prefs.pins)
  const [rev, setRev] = useState(0) // bumps to reopen the current note after a move
  // A search hit opens with its match scrolled into view; the sequence
  // remounts the page so a second hit in the same note scrolls too.
  const [hit, setHit] = useState<{ text: string; seq: number } | null>(null)
  const searchInput = useRef<HTMLInputElement>(null)
  const current = useRef<Note | null>(null)
  const user = auth.user()
  const narrow = layout !== 'desktop'
  const sidebarShown = narrow ? drawer : !collapsed

  // --- navigation --------------------------------------------------------

  // Every note opens in the preferred mode; a new one opens in the editor.
  const navigate = useCallback((id: string | null, push = true, edit = false) => {
    const path = id ? `/n/${id}` : '/'
    if (push && location.pathname !== path) history.pushState(null, '', path)
    setRoute(id ? { kind: 'note', id } : { kind: 'home' })
    setMode(edit ? 'edit' : prefs.openMode())
    setHit(null)
    setDrawer(false)
    if (!id) document.title = 'YANA/'
  }, [])

  // A search result: read mode, scrolled to the match.
  const openHit = useCallback((id: string, highlight: string | null) => {
    const path = `/n/${id}`
    if (location.pathname !== path) history.pushState(null, '', path)
    setRoute({ kind: 'note', id })
    setMode('read')
    setHit((h) => (highlight ? { text: highlight, seq: (h?.seq ?? 0) + 1 } : null))
    setDrawer(false)
    setQuery('')
  }, [])

  const openPage = useCallback((r: Route, path: string, title: string, push = true) => {
    if (push && location.pathname !== path) history.pushState(null, '', path)
    setRoute(r)
    setDrawer(false)
    document.title = `${title} — YANA/`
  }, [])

  const openLinks = useCallback((push = true) => openPage({ kind: 'links' }, '/links', 'Unresolved links', push), [openPage])
  const openTrash = useCallback((push = true) => openPage({ kind: 'trash' }, '/trash', 'Trash', push), [openPage])
  const openTags = useCallback((push = true) => openPage({ kind: 'tags' }, '/tags', 'Tags', push), [openPage])
  const openTag = useCallback(
    (tag: string, push = true) => openPage({ kind: 'tag', tag }, `/tags/${encodeURIComponent(tag)}`, `#${tag}`, push),
    [openPage],
  )
  const openSettings = useCallback(
    (section: Section | null = null, push = true) =>
      openPage({ kind: 'settings', section }, section ? `/settings/${section}` : '/settings', 'Settings', push),
    [openPage],
  )

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

  // The search page is the phone's; wider, the box is in the sidebar.
  useEffect(() => {
    if (route.kind !== 'search' || layout === 'phone') return
    history.replaceState(null, '', '/')
    setRoute({ kind: 'home' })
    window.setTimeout(() => searchInput.current?.focus(), 0)
  }, [route.kind, layout])

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

  useEffect(() => {
    void loadTree()
    void loadStatus()
    void outbox.drain()
    void loadSpaces()
    // Files can change under us; keep the tree fresh without a websocket.
    const t = window.setInterval(() => { void loadTree() }, 30_000)
    const onVis = () => { if (document.visibilityState === 'visible') { void loadTree(); void outbox.drain() } }
    const onOnline = () => { void loadTree(); void outbox.drain() }
    document.addEventListener('visibilitychange', onVis)
    window.addEventListener('online', onOnline)
    return () => {
      window.clearInterval(t)
      document.removeEventListener('visibilitychange', onVis)
      window.removeEventListener('online', onOnline)
    }
  }, [loadTree, loadStatus, loadSpaces])

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
  const dirs = useMemo(() => folders(spaces ?? []), [spaces])
  const hasDir = useCallback((path: string) => dirs.some((d) => d.path === path), [dirs])

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
        setFresh((f) => ({ id: res.id, seq: (f?.seq ?? 0) + 1 }))
        navigate(res.id, true, true)
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
      if (res.created) {
        setFresh((f) => ({ id: res.id, seq: (f?.seq ?? 0) + 1 }))
        void loadTree()
      }
      navigate(res.id, true, res.created)
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

  /** Open a note with its title selected: rename it from the tree. */
  function openTitle(id: string): void {
    setFresh((f) => ({ id, seq: (f?.seq ?? 0) + 1 }))
    navigate(id, true, true)
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
    },
    [navigate, loadTree, say, route],
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
            if (current.current && current.current.path.startsWith(node.path + '/')) navigate(null)
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
        'sep',
        { id: 'rename', label: 'Rename', icon: 'pencil', run: () => renameDirPrompt(n) },
        { id: 'move', label: 'Move to a folder', icon: 'move', run: () => moveDirPicker(n) },
        'sep',
        { id: 'delete', label: 'Delete', icon: 'trash', detail: 'notes to the trash', danger: true, run: () => deleteDirPrompt(n) },
      ]
    } else {
      title = target.name ? target.name + '/' : '/'
      items = [
        { id: 'new', label: 'New note here', icon: 'file-plus', run: () => newNote(target.name) },
        ...(target.name ? [{ id: 'folder', label: 'New folder', icon: 'folder-plus' as const, run: () => newFolderPrompt(target.name) }] : []),
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

  // Search: the sidebar box on a desktop and a tablet, a page of its
  // own on a phone.
  function focusSearch(): void {
    if (layout === 'phone') {
      openPage({ kind: 'search' }, '/search', 'Search')
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

  // Every shortcut in one list; the home screen does not carry them.
  function showShortcuts(): void {
    const rows: Array<[string, string]> = [
      ['New note', label(keys.newNote)],
      ['Capture a line into today\'s note', label(keys.capture)],
      ["Today's note", label(keys.daily)],
      ['Open a note by name or #tag', label(keys.switcher)],
      ['Command palette', label(keys.palette)],
      ['Search', label(keys.search) + ' or /'],
      ['Edit the open note', 'E'],
      ['Back to reading', 'Esc'],
      ['Editor and preview side by side', label(keys.split)],
      ['Name a new note, then write', 'Enter in the title'],
      ['Undo and redo in the editor', `${label({ key: 'z', mod: true })} / ${label({ key: 'z', mod: true, shift: true })}`],
      ['Find in the open note', label({ key: 'f', mod: true })],
      ['Actions for a note or folder in the tree', 'Right-click, or hold on a phone'],
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
      { id: 'new', label: 'New note', hint: label(keys.newNote), run: () => newNote() },
      { id: 'capture', label: 'Capture a line', detail: "into today's note, without opening it", hint: label(keys.capture), run: capturePrompt },
      { id: 'daily', label: "Today's note", hint: label(keys.daily), run: () => void openDaily() },
      { id: 'open', label: 'Open a note', hint: label(keys.switcher), run: openSwitcher },
      { id: 'search', label: 'Search notes', hint: label(keys.search), run: focusSearch },
      { id: 'new-path', label: 'New note at a path', detail: 'name the file and folder yourself', run: () => newNotePrompt() },
      { id: 'new-folder', label: 'New folder', detail: `in ${defaultDir() || 'the root'}`, run: () => newFolderPrompt(defaultDir()) },
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

  const actions = useRef({ openPalette, openSwitcher, newNote, openDaily, focusSearch, toggleSplit, capturePrompt })
  actions.current = { openPalette, openSwitcher, newNote, openDaily, focusSearch, toggleSplit, capturePrompt }

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
      else if (matches(ev, keys.newNote)) a.newNote()
      else if (matches(ev, keys.daily)) void a.openDaily()
      else if (matches(ev, keys.capture)) a.capturePrompt()
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
  const currentPinned = route.kind === 'note' && pinList.some((p) => p.kind === 'note' && p.id === route.id)

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
        <main class="content">
          {route.kind === 'note' && (
            <NotePage
              key={`${route.id}:${rev}:${hit?.seq ?? 0}`}
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
              fresh={fresh?.id === route.id}
              freshSeq={fresh?.seq ?? 0}
              onDelete={() => deleteNotePrompt()}
              onRename={() => renamePrompt()}
              onMove={() => moveNotePicker()}
              hasDir={hasDir}
              onExport={exportNote}
              onMoved={onMoved}
              onTag={openTag}
              pinned={currentPinned}
              onPin={() => { if (current.current) pinNote(current.current) }}
              highlight={hit?.text ?? null}
            />
          )}
          {route.kind === 'links' && <LinksReport onOpen={navigate} />}
          {route.kind === 'trash' && (
            <TrashPage onOpen={navigate} onToast={(m) => say(m)} confirm={setConfirmSpec} onChanged={() => void loadTree()} />
          )}
          {route.kind === 'tags' && <TagsIndex onTag={openTag} />}
          {route.kind === 'tag' && <TagPage tag={route.tag} onOpen={navigate} onAll={() => openTags()} />}
          {route.kind === 'share' && (
            <SharePage notes={notes} dailySpace={dailySpace()} onOpen={navigate} onToast={(m) => say(m)} />
          )}
          {route.kind === 'settings' && (
            <SettingsPage
              section={route.section}
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
          {route.kind === 'home' && (
            <Home
              notes={notes}
              pins={pinList}
              spaces={spaces ?? []}
              loading={spaces === null}
              onOpen={navigate}
              onNew={() => newNote()}
              onCapture={capturePrompt}
              onDaily={() => void openDaily()}
              onInstall={net.canInstall ? () => void pwa.promptInstall() : null}
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
          <button type="button" class="bottombar-btn accent" onClick={capturePrompt}>
            <Icon name="capture" size={20} />
            Capture
          </button>
          <button type="button" class="bottombar-btn" onClick={() => void openDaily()}>
            <Icon name="calendar" size={20} />
            Today
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
  onOpen: (id: string) => void
  onNew: () => void
  onCapture: () => void
  onDaily: () => void
  /** Offered when the browser made an install prompt available. */
  onInstall: (() => void) | null
}

// The home page: the three things people come here to do, then what
// they pinned and what they opened last. Shortcuts are in the account
// menu and the palette.
function Home({ notes, pins, spaces, loading, onOpen, onNew, onCapture, onDaily, onInstall }: HomeProps) {
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

  return (
    <div class="home">
      <img class="home-mark" src="/icon-192.png" alt="" width={96} height={96} />
      <h1 class="home-title">YANA/</h1>
      <p class="home-sub">Everything you expect. Nothing you don't.</p>
      <div class="home-actions">
        <button type="button" class="btn primary large" onClick={onNew}>
          <Icon name="plus" size={18} />
          New note
        </button>
        <button type="button" class="btn large" onClick={onCapture}>
          <Icon name="capture" size={18} />
          Capture
        </button>
        <button type="button" class="btn large" onClick={onDaily}>
          <Icon name="calendar" size={18} />
          Today
        </button>
        {onInstall && (
          <button type="button" class="btn large" onClick={onInstall}>
            <Icon name="share" size={18} />
            Install app
          </button>
        )}
      </div>
      {loading ? (
        <p class="muted">Loading the tree…</p>
      ) : notes.length === 0 ? (
        <p class="muted">No notes yet. Start one above, or drop a markdown file into the notes directory; it shows up on the next scan.</p>
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
                    <a class="recent" href={`/n/${n.id}`} onClick={(ev) => { ev.preventDefault(); onOpen(n.id) }}>
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
