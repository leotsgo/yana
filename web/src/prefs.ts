// Per-browser preferences: theme, text size and measure, editor layout,
// sidebar state, what the tree has open, default spaces, the display name,
// recently opened notes, pinned notes and folders, the open tabs, recent
// searches. Everything lives in localStorage under one prefix and is read
// through here so the shell, the editor and the settings pages agree on
// the keys. Storage can be unavailable (private windows, blocked site
// data); every access is guarded and falls back to the default. A change
// made anywhere is announced through onChange so an open shell follows
// the settings page without a reload.

import type { ColorBy } from './graph/model'
import { DEFAULT_FORCES, FORCE_RANGES } from './graph/protocol'
import type { Forces } from './graph/protocol'
import { BUILTIN, TOKENS, YANA_DARK, YANA_LIGHT, parseTheme, themeVars } from './themes'
import type { Mode, ThemeDef } from './themes'

export type Theme = 'light' | 'dark' | 'system'
/** How a note opens: rendered, as source, or both side by side. */
export type OpenMode = 'read' | 'edit' | 'split'
/** Text size of the note body, read and edit alike. */
export type TextSize = 'small' | 'normal' | 'large'
/** The measure: how wide a line of the note may run. */
export type LineWidth = 'narrow' | 'normal' | 'wide'
/** Row height in the sidebar tree. */
export type Density = 'comfortable' | 'compact'

const PREFIX = 'yana.'
const RECENTS_MAX = 12
const FOLDERS_MAX = 8
const QUERIES_MAX = 8

const listeners = new Set<() => void>()

/** Runs fn after any preference changes; returns the unsubscribe. */
export function onChange(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

function read(key: string): string | null {
  try {
    return localStorage.getItem(PREFIX + key)
  } catch {
    return null
  }
}

function write(key: string, value: string | null): void {
  try {
    if (value === null) localStorage.removeItem(PREFIX + key)
    else localStorage.setItem(PREFIX + key, value)
  } catch {
    // Nothing to do; the preference just does not persist.
  }
  for (const fn of listeners) fn()
}

// --- theme ---------------------------------------------------------------

export function theme(): Theme {
  const t = read('theme')
  return t === 'light' || t === 'dark' ? t : 'system'
}

export function setTheme(t: Theme): void {
  write('theme', t === 'system' ? null : t)
  applyTheme()
}

const darkQuery = window.matchMedia('(prefers-color-scheme: dark)')

/** Every theme on offer: the built-ins, then the ones added here. */
export function themes(): ThemeDef[] {
  return [...BUILTIN, ...customThemes()]
}

/** The theme used in one mode; Yana's own unless another was picked. */
export function themeFor(mode: Mode): ThemeDef {
  const id = read('theme.' + mode)
  return themes().find((d) => d.id === id && d.mode === mode) ?? (mode === 'dark' ? YANA_DARK : YANA_LIGHT)
}

export function setThemeFor(mode: Mode, id: string): void {
  write('theme.' + mode, id === (mode === 'dark' ? YANA_DARK : YANA_LIGHT).id ? null : id)
  applyTheme()
}

/** The owner's themes, as last fetched from .yana/themes.json. */
export function customThemes(): ThemeDef[] {
  try {
    const v = JSON.parse(read('themes') ?? '[]') as ThemeDef[]
    return Array.isArray(v) ? v : []
  } catch {
    return []
  }
}

/**
 * Keeps the server's theme list, each one checked as a pasted theme is,
 * so a hand-edited file can only ever put hex colours on the page.
 */
export function setCustomThemes(list: unknown[]): void {
  const defs = list.map((x) => parseTheme(JSON.stringify(x))).filter((d): d is ThemeDef => typeof d !== 'string')
  write('themes', defs.length ? JSON.stringify(defs) : null)
  applyTheme()
}

/** CSS text of a theme's tokens, for the root's inline style. */
function cssText(def: ThemeDef): string {
  return Object.entries(themeVars(def)).map(([k, v]) => `--${k}:${v}`).join(';')
}

/**
 * Resolves the preference against the system and stamps the root: the
 * mode as data-theme, the theme's colours as inline custom properties.
 * Both modes' colours are kept under yana.palette.* so the script in
 * index.html paints the right ones before the app boots.
 */
export function applyTheme(): void {
  const t = theme()
  const dark = t === 'dark' || (t === 'system' && darkQuery.matches)
  const def = themeFor(dark ? 'dark' : 'light')
  const root = document.documentElement
  for (const k of [...TOKENS, 'hover']) root.style.removeProperty('--' + k)
  const css = cssText(def)
  if (css) root.style.cssText += ';' + css
  try {
    for (const m of ['light', 'dark'] as const) {
      const c = cssText(themeFor(m))
      if (c) localStorage.setItem(PREFIX + 'palette.' + m, c)
      else localStorage.removeItem(PREFIX + 'palette.' + m)
    }
  } catch {
    // The pre-paint script then shows Yana's colours until the app boots.
  }
  root.dataset['palette'] = def.id
  root.dataset['theme'] = dark ? 'dark' : 'light'
  const meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
  if (meta) meta.content = themeVars(def)['bg-2'] ?? (dark ? '#1c1a17' : '#f3efe7')
}

darkQuery.addEventListener('change', () => {
  if (theme() === 'system') applyTheme()
})

// --- text and chrome -----------------------------------------------------

export function textSize(): TextSize {
  const v = read('size')
  return v === 'small' || v === 'large' ? v : 'normal'
}

export function setTextSize(v: TextSize): void {
  write('size', v === 'normal' ? null : v)
  applyAppearance()
}

export function lineWidth(): LineWidth {
  const v = read('measure')
  return v === 'narrow' || v === 'wide' ? v : 'normal'
}

export function setLineWidth(v: LineWidth): void {
  write('measure', v === 'normal' ? null : v)
  applyAppearance()
}

/** Whether the note's column sits in the middle of the page (the default)
 * or against its left edge. */
export function centered(): boolean {
  return read('align') !== 'left'
}

export function setCentered(on: boolean): void {
  write('align', on ? null : 'left')
  applyAppearance()
}

export function density(): Density {
  return read('density') === 'compact' ? 'compact' : 'comfortable'
}

export function setDensity(v: Density): void {
  write('density', v === 'comfortable' ? null : v)
  applyAppearance()
}

/** Stamps the root with the text and chrome preferences; the stylesheet
 * reads them as attributes so every page follows without a rerender. */
export function applyAppearance(): void {
  const root = document.documentElement
  const stamp = (name: string, value: string, fallback: string) => {
    if (value === fallback) delete root.dataset[name]
    else root.dataset[name] = value
  }
  stamp('size', textSize(), 'normal')
  stamp('measure', lineWidth(), 'normal')
  stamp('align', centered() ? 'center' : 'left', 'center')
  stamp('density', density(), 'comfortable')
}

// --- identity ------------------------------------------------------------

/** The name shown beside this person's cursor, and the author of their
 * edits when the server runs without accounts. Empty means the account's
 * username (or, without accounts, a generated name). */
export function displayName(): string {
  return (read('name') ?? '').trim()
}

export function setDisplayName(name: string): void {
  write('name', name.trim().slice(0, 40) || null)
}

// --- spaces --------------------------------------------------------------

/** The space new notes land in when no note is open; empty picks the
 * open note's space, then the first one. */
export function defaultSpace(): string {
  return read('space') ?? ''
}

export function setDefaultSpace(space: string): void {
  write('space', space || null)
}

/** The space the daily note lives in; empty follows defaultSpace. */
export function dailySpace(): string {
  return read('daily') ?? ''
}

export function setDailySpace(space: string): void {
  write('daily', space || null)
}

// --- layout --------------------------------------------------------------

/** The mode a note opens in. Read is the default; split only applies
 * on wide screens and falls back to read on a phone. */
export function openMode(): OpenMode {
  const m = read('open')
  return m === 'edit' || m === 'split' ? m : 'read'
}

export function setOpenMode(m: OpenMode): void {
  write('open', m === 'read' ? null : m)
}

/** Whether the editor hides markdown syntax on the lines the caret is not on. */
export function livePreview(): boolean {
  return read('live') === '1'
}

export function setLivePreview(on: boolean): void {
  write('live', on ? '1' : null)
}

export function sidebarCollapsed(): boolean {
  return read('sidebar') === '0'
}

export function setSidebarCollapsed(c: boolean): void {
  write('sidebar', c ? '0' : null)
}

/** Whether a note opened from anywhere but the tree opens the folders
 * above it there and scrolls to its row. On unless turned off. */
export function revealInTree(): boolean {
  return read('tree.reveal') !== '0'
}

export function setRevealInTree(on: boolean): void {
  write('tree.reveal', on ? null : '0')
}

// --- recents -------------------------------------------------------------

export function recents(): string[] {
  const raw = read('recents')
  if (!raw) return []
  try {
    const v = JSON.parse(raw) as unknown
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
  } catch {
    return []
  }
}

export function touchRecent(id: string): void {
  const list = [id, ...recents().filter((x) => x !== id)].slice(0, RECENTS_MAX)
  write('recents', JSON.stringify(list))
}

export function forgetRecent(id: string): void {
  write('recents', JSON.stringify(recents().filter((x) => x !== id)))
}

// --- pins ----------------------------------------------------------------

/** A pinned note (by id) or folder (by path). Pins are a preference of
 * this browser, not a file in the tree: the tree is the folders and the
 * notes, nothing else (invariant 2). */
export type Pin = { kind: 'note'; id: string } | { kind: 'dir'; path: string }

export function pins(): Pin[] {
  const raw = read('pins')
  if (!raw) return []
  try {
    const v = JSON.parse(raw) as unknown
    if (!Array.isArray(v)) return []
    return v.filter(
      (x): x is Pin =>
        typeof x === 'object' &&
        x !== null &&
        ((x as Pin).kind === 'note' ? typeof (x as { id?: unknown }).id === 'string' : (x as Pin).kind === 'dir' && typeof (x as { path?: unknown }).path === 'string'),
    )
  } catch {
    return []
  }
}

function samePin(a: Pin, b: Pin): boolean {
  return a.kind === b.kind && (a.kind === 'note' ? a.id === (b as { id: string }).id : a.path === (b as { path: string }).path)
}

export function isPinned(pin: Pin): boolean {
  return pins().some((p) => samePin(p, pin))
}

/** Adds the pin at the top, or removes it when it is already there. */
export function togglePin(pin: Pin): boolean {
  const rest = pins().filter((p) => !samePin(p, pin))
  const on = rest.length === pins().length
  write('pins', JSON.stringify(on ? [pin, ...rest] : rest))
  return on
}

/** A moved or deleted folder takes its pin along, or with it. */
export function repinDir(from: string, to: string | null): void {
  const next = pins().flatMap((p) => {
    if (p.kind !== 'dir' || (p.path !== from && !p.path.startsWith(from + '/'))) return [p]
    if (to === null) return []
    return [{ kind: 'dir' as const, path: to + p.path.slice(from.length) }]
  })
  write('pins', JSON.stringify(next))
}

export function forgetPin(pin: Pin): void {
  write('pins', JSON.stringify(pins().filter((p) => !samePin(p, pin))))
}

// --- tree state ----------------------------------------------------------

// What the tree has open. Folders start closed and spaces start open, so
// the store holds the folders a person opened and the spaces they closed:
// an empty store is the default, and a folder that goes away leaves at
// most a stale key. Kept in memory as well so the tree still toggles in a
// browser that blocks storage.

/** The pinned section, in the closed set beside the space names. */
export const PINNED_SECTION = '\0pinned'

let openDirs: Set<string> | null = null
let closedSet: Set<string> | null = null

function readSet(key: string): Set<string> {
  const raw = read(key)
  if (!raw) return new Set()
  try {
    const v = JSON.parse(raw) as unknown
    return new Set(Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [])
  } catch {
    return new Set()
  }
}

/** The folders a person opened, by path. */
export function openFolders(): ReadonlySet<string> {
  return (openDirs ??= readSet('tree.open'))
}

/** The spaces a person closed, by name (and PINNED_SECTION). */
export function closedSpaces(): ReadonlySet<string> {
  return (closedSet ??= readSet('tree.closed'))
}

export function isFolderOpen(path: string): boolean {
  return openFolders().has(path)
}

export function isSpaceOpen(name: string): boolean {
  return !closedSpaces().has(name)
}

export function setFoldersOpen(paths: Iterable<string>, open: boolean): void {
  const set = new Set(openFolders())
  for (const p of paths) {
    if (open) set.add(p)
    else set.delete(p)
  }
  openDirs = set
  write('tree.open', JSON.stringify([...set]))
}

export function setFolderOpen(path: string, open: boolean): void {
  setFoldersOpen([path], open)
}

export function setSpaceOpen(name: string, open: boolean): void {
  const set = new Set(closedSpaces())
  if (open) set.delete(name)
  else set.add(name)
  closedSet = set
  write('tree.closed', JSON.stringify([...set]))
}

/** A renamed or moved folder keeps its open state, and its children
 * theirs; the recent folders and the last folder follow it too. */
export function moveFolderState(from: string, to: string): void {
  const move = (p: string) => (p === from || p.startsWith(from + '/') ? to + p.slice(from.length) : p)
  const set = new Set<string>()
  for (const p of openFolders()) set.add(move(p))
  openDirs = set
  write('tree.open', JSON.stringify([...set]))
  const recent = recentFolders()
  const moved = [...new Set(recent.map(move))]
  if (moved.some((p, i) => p !== recent[i]) || moved.length !== recent.length) write('folders.recent', JSON.stringify(moved))
  const last = lastFolder()
  if (last && move(last) !== last) write('folders.last', move(last))
}

// --- per-folder sort -----------------------------------------------------

export type FolderSort = 'title-asc' | 'title-desc' | 'created-desc' | 'created-asc' | 'modified-desc' | 'modified-asc'

let sortDirs: Map<string, FolderSort> | null = null

function readSorts(): Map<string, FolderSort> {
  if (sortDirs) return sortDirs
  const raw = read('tree.sort')
  const map = new Map<string, FolderSort>()
  if (raw) {
    try {
      const v = JSON.parse(raw) as unknown
      if (v && typeof v === 'object') {
        for (const [k, mode] of Object.entries(v as Record<string, unknown>)) {
          if (typeof mode === 'string') map.set(k, mode as FolderSort)
        }
      }
    } catch {
      // A malformed entry is the same as no preference.
    }
  }
  sortDirs = map
  return map
}

/** The sort mode one folder is viewed with; 'title-asc' when unset. */
export function folderSort(path: string): FolderSort {
  return readSorts().get(path) ?? 'title-asc'
}

export function setFolderSort(path: string, mode: FolderSort): void {
  const map = readSorts()
  if (mode === 'title-asc') map.delete(path)
  else map.set(path, mode)
  sortDirs = map
  write('tree.sort', map.size ? JSON.stringify(Object.fromEntries(map)) : null)
}

/** Drops keys the live tree no longer has. Only writes when something went. */
export function pruneTreeState(folders: Iterable<string>, spaces: Iterable<string>): void {
  const f = new Set(folders)
  const s = new Set(spaces)
  s.add(PINNED_SECTION)
  const recent = recentFolders()
  const kept = recent.filter((p) => f.has(p))
  if (kept.length !== recent.length) write('folders.recent', JSON.stringify(kept))
  const last = lastFolder()
  if (last && !f.has(last)) write('folders.last', null)
  const open = [...openFolders()].filter((p) => f.has(p))
  const closed = [...closedSpaces()].filter((n) => s.has(n))
  if (open.length === openFolders().size && closed.length === closedSpaces().size) return
  openDirs = new Set(open)
  closedSet = new Set(closed)
  write('tree.open', JSON.stringify(open))
  write('tree.closed', JSON.stringify(closed))
}

// --- new notes -------------------------------------------------------------

/** Where the new-note picker starts: beside the open note, else the last
 * folder a note was made in; or always in the last folder. */
export type NewNoteStart = 'beside' | 'last'

export function newNoteStart(): NewNoteStart {
  return read('newnote.start') === 'last' ? 'last' : 'beside'
}

export function setNewNoteStart(v: NewNoteStart): void {
  write('newnote.start', v === 'beside' ? null : v)
}

/** Whether the picker suggests the next name in a folder whose notes
 * follow a pattern (dated, or numbered). On unless turned off. */
export function suggestNames(): boolean {
  return read('newnote.suggest') !== '0'
}

export function setSuggestNames(on: boolean): void {
  write('newnote.suggest', on ? null : '0')
}

/** The last folders a note was made in or opened from on this device,
 * newest first, each once. The pickers show them at the top. */
export function recentFolders(): string[] {
  const raw = read('folders.recent')
  if (!raw) return []
  try {
    const v = JSON.parse(raw) as unknown
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
  } catch {
    return []
  }
}

export function touchFolder(path: string): void {
  if (!path) return
  const list = [path, ...recentFolders().filter((x) => x !== path)].slice(0, FOLDERS_MAX)
  write('folders.recent', JSON.stringify(list))
}

/** The folder a note was last made in on this device; empty when none. */
export function lastFolder(): string {
  return read('folders.last') ?? ''
}

export function setLastFolder(path: string): void {
  if (path && path !== lastFolder()) write('folders.last', path)
}

// --- activity ---------------------------------------------------------------

/** When this browser last looked at the activity feed, in epoch
 * milliseconds; null when it never has. The feed and the home screen
 * mark what changed since then. */
export function activitySeen(): number | null {
  const v = read('activity.seen')
  if (!v) return null
  const n = Number(v)
  return Number.isFinite(n) && n > 0 ? n : null
}

export function touchActivitySeen(): void {
  write('activity.seen', String(Date.now()))
}

// --- tabs ------------------------------------------------------------------

/** The open tabs, as workspace.ts writes them. Stored without announcing
 * a change: the tabs have their own listeners, and a scroll position is
 * written often. */
export function tabState(): string | null {
  return read('tabs')
}

export function setTabState(v: string): void {
  try {
    localStorage.setItem(PREFIX + 'tabs', v)
  } catch {
    // The tabs just do not come back after a reload.
  }
}

// --- recent searches ------------------------------------------------------

export function recentQueries(): string[] {
  const raw = read('queries')
  if (!raw) return []
  try {
    const v = JSON.parse(raw) as unknown
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
  } catch {
    return []
  }
}

export function touchQuery(q: string): void {
  const v = q.trim()
  if (!v) return
  const list = [v, ...recentQueries().filter((x) => x !== v)].slice(0, QUERIES_MAX)
  write('queries', JSON.stringify(list))
}

export function forgetQuery(q: string): void {
  write('queries', JSON.stringify(recentQueries().filter((x) => x !== q)))
}

// --- saved searches ---------------------------------------------------------

/** A search kept under a name, pinned to the sidebar. A preference of
 * this browser, like pins: the tree holds notes and folders, nothing
 * else. */
export interface SavedSearch {
  name: string
  query: string
}

const SAVED_MAX = 20

export function savedSearches(): SavedSearch[] {
  const raw = read('searches.saved')
  if (!raw) return []
  try {
    const v = JSON.parse(raw) as unknown
    if (!Array.isArray(v)) return []
    return v.filter(
      (x): x is SavedSearch =>
        typeof x === 'object' && x !== null && typeof (x as SavedSearch).name === 'string' && typeof (x as SavedSearch).query === 'string',
    )
  } catch {
    return []
  }
}

/** Saves a query under a name; the same name is overwritten. */
export function saveSearch(name: string, query: string): void {
  const n = name.trim().slice(0, 60)
  const q = query.trim()
  if (!n || !q) return
  const rest = savedSearches().filter((s) => s.name !== n)
  write('searches.saved', JSON.stringify([{ name: n, query: q }, ...rest].slice(0, SAVED_MAX)))
}

export function forgetSearch(name: string): void {
  write('searches.saved', JSON.stringify(savedSearches().filter((s) => s.name !== name)))
}

/** Whether this exact query is already saved under a name. */
export function isSearchSaved(query: string): boolean {
  const q = query.trim()
  return q !== '' && savedSearches().some((s) => s.query === q)
}

// --- drawing views -------------------------------------------------------

/** Where a drawing's canvas was left: scroll offsets, zoom and the tool in hand. */
export type DrawingView = { scrollX: number; scrollY: number; zoom: number; tool?: string; locked?: boolean }

/** The view a drawing was left at in this browser, or null for a first visit. */
export function drawingView(id: string): DrawingView | null {
  try {
    const v = JSON.parse(read('drawing.view.' + id) ?? 'null') as DrawingView | null
    return v && [v.scrollX, v.scrollY, v.zoom].every(Number.isFinite) ? v : null
  } catch {
    return null
  }
}

// ponytail: one key per drawing ever opened, never pruned; fine for a
// personal vault, prune by age if drawings ever number in the thousands.
/** Remembers the view; written quietly, scrolling must not wake the shell. */
export function setDrawingView(id: string, v: DrawingView): void {
  try {
    localStorage.setItem(PREFIX + 'drawing.view.' + id, JSON.stringify(v))
  } catch {
    // Nothing to do; the view just does not persist.
  }
}

// --- graph view ----------------------------------------------------------

/** The graph view's filters, forces and panel, one JSON value per browser. */
export interface GraphPrefs {
  search: string
  orphans: boolean
  ghosts: boolean
  tags: boolean
  colorBy: ColorBy
  forces: Forces
  depth: number
  localOpen: boolean
  /** The filters panel; unset until toggled, open by default off the phone. */
  panel?: boolean
}

export function graphPrefs(): GraphPrefs {
  let v: Partial<Record<keyof GraphPrefs, unknown>> = {}
  try {
    const raw: unknown = JSON.parse(read('graph') ?? '{}')
    if (raw && typeof raw === 'object') v = raw
  } catch {
    // Unreadable: the defaults.
  }
  const bool = (x: unknown, d: boolean) => (typeof x === 'boolean' ? x : d)
  const f = (v.forces && typeof v.forces === 'object' ? v.forces : {}) as Record<string, unknown>
  const forces = { ...DEFAULT_FORCES }
  for (const k of Object.keys(FORCE_RANGES) as (keyof Forces)[]) {
    const [min, max] = FORCE_RANGES[k]
    const x = f[k]
    if (typeof x === 'number' && Number.isFinite(x)) forces[k] = Math.min(max, Math.max(min, x))
  }
  const depth = v.depth
  return {
    search: typeof v.search === 'string' ? v.search : '',
    orphans: bool(v.orphans, true),
    ghosts: bool(v.ghosts, false),
    tags: bool(v.tags, false),
    colorBy: v.colorBy === 'space' || v.colorBy === 'folder' ? v.colorBy : 'none',
    forces,
    depth: typeof depth === 'number' && Number.isInteger(depth) && depth >= 1 && depth <= 3 ? depth : 1,
    localOpen: bool(v.localOpen, false),
    panel: typeof v.panel === 'boolean' ? v.panel : undefined,
  }
}

/** Merges into the graph prefs; written quietly, a slider must not wake the shell. */
export function setGraphPrefs(p: Partial<GraphPrefs>): void {
  try {
    localStorage.setItem(PREFIX + 'graph', JSON.stringify({ ...graphPrefs(), ...p }))
  } catch {
    // Nothing to do; the graph just forgets.
  }
}
