// The open tabs: which notes are open in the content pane, in what
// order, which one shows, and how each was left (read, edit or split,
// and how far down). A tab is a record, not a mounted page: only the
// active tab's note is on screen and holds a realtime session; the rest
// are an id and the state to come back to. A page (tasks, tags,
// settings...) is a tab too, one per kind: its id is the page's path,
// and opening that kind again moves the tab to the new path. At most one tab is a preview:
// browsing (a click in the tree, a search hit, a wikilink) replaces it
// instead of adding tabs, and editing it, a double-click, or Keep open
// makes it a normal tab. Pinned tabs sit leftmost. On a desktop the
// content area splits into two panes side by side, each with its own
// strip; the focused one is where keys and the tree act, and a pane
// whose last tab closes goes away. The state is kept per device through
// prefs, and a change is announced through subscribe.

import { isMac } from './hotkeys'
import type { OpenMode } from './prefs'
import * as prefs from './prefs'

export interface Tab {
  /** Stable for the life of the tab; history entries carry it. */
  key: string
  /** The note's id, or a page's path (it starts with a slash). */
  id: string
  mode: OpenMode
  /** Where the note was scrolled to when it was last left, in pixels. */
  scroll: number
  preview: boolean
  pinned: boolean
  /** The last known title, shown until the tree has the note. */
  title: string
}

export interface Pane {
  tabs: Tab[]
  /** The key of the tab on screen; null when none is. */
  active: string | null
}

export interface Workspace {
  /** One pane, or two side by side. */
  panes: Pane[]
  /** The pane the keys and the tree act on. */
  focus: number
  /** The left pane's share of the width while split, 0 to 1. */
  divider: number
}

/** How a note opens: in the preview tab, in a new tab brought to the
 * front, in a new tab behind the current one, in place of the current
 * tab (the phone, which has no strip), or in the other pane. */
export type OpenHow = 'preview' | 'tab' | 'background' | 'here' | 'right'

const CLOSED_MAX = 20

/** True for a page tab: its id is a path. */
export function isPage(id: string): boolean {
  return id.startsWith('/')
}

/** A page's kind: the first part of its path (tasks, tags, settings),
 * home for the root. */
export function pageKind(id: string): string {
  return id.slice(1).split(/[/?#]/)[0] || 'home'
}

/** Two ids for the same tab: the same note, or pages of one kind. */
function same(a: string, b: string): boolean {
  return a === b || (isPage(a) && isPage(b) && pageKind(a) === pageKind(b))
}

/** How a click asks for a note: Mod-click or the middle button opens it
 * in a tab behind the current one. A plain click is undefined: the
 * caller's default, which is the preview tab. */
export function howFrom(ev: MouseEvent): OpenHow | undefined {
  const mod = isMac ? ev.metaKey : ev.ctrlKey
  if (mod && ev.altKey) return 'right'
  if (ev.button === 1 || mod) return 'background'
  return undefined
}

/** Click and middle-click handlers for a link to a note. */
export function openProps(open: (how?: OpenHow) => void) {
  return {
    onClick: (ev: MouseEvent) => {
      ev.preventDefault()
      open(howFrom(ev))
    },
    onAuxClick: (ev: MouseEvent) => {
      if (ev.button !== 1) return
      ev.preventDefault()
      open('background')
    },
  }
}

/** The same for a link built outside Preact. */
export function wireOpen(a: HTMLElement, open: (how?: OpenHow) => void): void {
  const p = openProps(open)
  a.addEventListener('click', p.onClick)
  a.addEventListener('auxclick', p.onAuxClick)
}

let state: Workspace = load()
const listeners = new Set<() => void>()
/** Recently closed tabs, newest last, for reopening. Kept in memory. */
const closed: Array<{ tab: Tab; pane: number; index: number }> = []

export function get(): Workspace {
  return state
}

/** Runs fn after the tabs change; returns the unsubscribe. */
export function subscribe(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

function commit(next: Workspace): void {
  state = tidy(next)
  save()
  for (const fn of listeners) fn()
}

/** A pane with no tabs goes, unless it is the only one; focus follows. */
function tidy(ws: Workspace): Workspace {
  if (ws.panes.length < 2 || ws.panes.every((p) => p.tabs.length > 0)) return ws
  const keep = ws.panes.map((p, i) => ({ p, i })).filter(({ p }) => p.tabs.length > 0)
  if (keep.length === 0) return { ...ws, panes: [{ tabs: [], active: null }], focus: 0 }
  const focus = Math.max(0, keep.findIndex(({ i }) => i === ws.focus))
  return { ...ws, panes: keep.map(({ p }) => p), focus }
}

function newKey(): string {
  return Math.random().toString(36).slice(2, 10) + Date.now().toString(36).slice(-4)
}

/** A pane; the focused one by default. */
export function pane(i = state.focus): Pane {
  return state.panes[i] ?? { tabs: [], active: null }
}

export function activeTab(i = state.focus): Tab | null {
  const p = pane(i)
  return p.tabs.find((t) => t.key === p.active) ?? null
}

/** The tab with this key, and the pane it is in. */
export function find(key: string): { tab: Tab; pane: number } | null {
  for (let i = 0; i < state.panes.length; i++) {
    const tab = state.panes[i]?.tabs.find((t) => t.key === key)
    if (tab) return { tab, pane: i }
  }
  return null
}

function setPane(i: number, p: Pane, ws = state): Workspace {
  const panes = ws.panes.slice()
  panes[i] = p
  return { ...ws, panes }
}

function update(key: string, fn: (t: Tab) => Tab): void {
  const at = find(key)
  if (!at) return
  const p = pane(at.pane)
  commit(setPane(at.pane, { ...p, tabs: p.tabs.map((t) => (t.key === key ? fn(t) : t)) }))
}

/** Where a new tab goes: after the active one, and never among the pinned. */
function insertAt(p: Pane): number {
  const pinned = p.tabs.filter((t) => t.pinned).length
  const i = p.tabs.findIndex((t) => t.key === p.active)
  return Math.max(pinned, i + 1)
}

/** Opens a note in the focused pane and returns its tab. A note that
 * already has a tab there is brought to the front instead (or, opened
 * in the background, left where it is). */
export function open(id: string, how: OpenHow, init: { mode: OpenMode; title?: string }, at = state.focus): Tab {
  const p = pane(at)
  const fresh = (preview: boolean): Tab => ({ key: newKey(), id, mode: init.mode, scroll: 0, preview, pinned: false, title: init.title ?? '' })

  if (how === 'right') return openRight(id, init)

  if (how === 'here') {
    const cur = p.tabs.find((t) => t.key === p.active)
    if (!cur) return open(id, 'preview', init, at)
    if (cur.id === id) return cur
    const next: Tab = { ...cur, id, mode: init.mode, scroll: 0, title: init.title ?? '' }
    commit(setPane(at, { ...p, tabs: p.tabs.filter((t) => !same(t.id, id) || t.key === cur.key).map((t) => (t.key === cur.key ? next : t)) }))
    return next
  }

  const existing = p.tabs.find((t) => same(t.id, id))
  if (existing) {
    if (how === 'background') return existing
    // A page of the same kind moves to the new path (a filter, a section).
    let tab = existing.id === id ? existing : { ...existing, id, scroll: 0 }
    if (how === 'tab' && tab.preview) tab = { ...tab, preview: false }
    commit(setPane(at, { tabs: p.tabs.map((t) => (t.key === tab.key ? tab : t)), active: tab.key }))
    return tab
  }

  if (how === 'preview') {
    const prev = p.tabs.find((t) => t.preview)
    if (prev) {
      const tab: Tab = { ...prev, id, mode: init.mode, scroll: 0, title: init.title ?? '' }
      commit(setPane(at, { tabs: p.tabs.map((t) => (t.key === prev.key ? tab : t)), active: tab.key }))
      return tab
    }
  }

  const tab = fresh(how === 'preview')
  const tabs = p.tabs.slice()
  if (how === 'background') tabs.push(tab)
  else tabs.splice(insertAt(p), 0, tab)
  commit(setPane(at, { tabs, active: how === 'background' ? p.active : tab.key }))
  return tab
}

/** Opens a note in the pane beside the focused one, making that pane
 * when there is one pane. With two, the note takes the place of the
 * other pane's active tab (or brings its own tab there to the front).
 * Focus stays where it was, so the next one lands on the same side. */
function openRight(id: string, init: { mode: OpenMode; title?: string }): Tab {
  const tab: Tab = { key: newKey(), id, mode: init.mode, scroll: 0, preview: false, pinned: false, title: init.title ?? '' }
  if (state.panes.length < 2) {
    commit({ ...state, panes: [pane(0), { tabs: [tab], active: tab.key }] })
    return tab
  }
  const other = state.focus === 0 ? 1 : 0
  const p = pane(other)
  const had = p.tabs.find((t) => same(t.id, id))
  if (had) {
    const tab = { ...had, id }
    commit(setPane(other, { tabs: p.tabs.map((t) => (t.key === had.key ? tab : t)), active: had.key }))
    return tab
  }
  const cur = p.tabs.find((t) => t.key === p.active)
  if (!cur || cur.pinned) {
    commit(setPane(other, { tabs: [...p.tabs, tab], active: tab.key }))
    return tab
  }
  const next: Tab = { ...cur, id, mode: init.mode, scroll: 0, preview: false, title: init.title ?? '' }
  commit(setPane(other, { ...p, tabs: p.tabs.map((t) => (t.key === cur.key ? next : t)) }))
  return next
}

/** Moves a tab into another pane at an index (the end by default), and
 * focuses it there. Pane 1 when there is one pane makes the split. A
 * note already open in the target pane is brought to the front instead. */
export function moveToPane(key: string, to: number, index?: number): void {
  const at = find(key)
  if (!at || at.pane === to) return
  let ws = without(at.pane, (t) => t.key === key, false)
  if (to >= ws.panes.length) ws = { ...ws, panes: [...ws.panes, { tabs: [], active: null }] }
  const dest = ws.panes[to] as Pane
  const had = dest.tabs.find((t) => same(t.id, at.tab.id))
  if (had) {
    commit({ ...setPane(to, { ...dest, active: had.key }, ws), focus: to })
    return
  }
  const tab: Tab = { ...at.tab, pinned: false }
  const tabs = dest.tabs.slice()
  const pinned = tabs.filter((t) => t.pinned).length
  tabs.splice(Math.max(pinned, Math.min(index ?? tabs.length, tabs.length)), 0, tab)
  // The source pane may have gone; the target's index moves with it.
  const next = tidy({ ...setPane(to, { tabs, active: tab.key }, ws), focus: to })
  commit({ ...next, focus: next.panes.findIndex((p) => p.tabs.some((t) => t.key === key)) })
}

/** Closes a pane: its tabs go on the reopen list. */
export function closePane(i: number): void {
  if (state.panes.length < 2) return
  commit(without(i, () => true, true))
}

/** Folds the right pane's tabs into the left strip and closes the split:
 * the window got too narrow for two. The focused pane's tab stays active. */
export function merge(): void {
  if (state.panes.length < 2) return
  const [left, right] = state.panes as [Pane, Pane]
  const tabs = [...left.tabs, ...right.tabs.filter((t) => !left.tabs.some((l) => same(l.id, t.id))).map((t) => ({ ...t, pinned: false }))]
  const was = (state.focus === 1 ? right : left).tabs.find((t) => t.key === (state.focus === 1 ? right : left).active)
  const active = tabs.find((t) => t.key === was?.key)?.key ?? tabs.find((t) => t.id === was?.id)?.key ?? left.active
  commit({ ...state, panes: [{ tabs, active }], focus: 0 })
}

export function focusPane(i: number): void {
  if (i === state.focus || i < 0 || i >= state.panes.length) return
  commit({ ...state, focus: i })
}

export function setDivider(f: number): void {
  const v = Math.min(0.9, Math.max(0.1, f))
  if (v === state.divider) return
  commit({ ...state, divider: v })
}

/** Points an existing tab at another note: back and forward within a tab. */
export function retarget(key: string, id: string, mode: OpenMode, title: string): void {
  const at = find(key)
  if (!at) return
  const p = pane(at.pane)
  const tabs = p.tabs
    .filter((t) => !same(t.id, id) || t.key === key)
    .map((t) => (t.key === key ? (t.id === id ? t : { ...t, id, mode, scroll: 0, title }) : t))
  commit({ ...setPane(at.pane, { tabs, active: key }), focus: at.pane })
}

export function activate(key: string): void {
  const at = find(key)
  if (!at) return
  const p = pane(at.pane)
  if (p.active === key && state.focus === at.pane) return
  commit({ ...setPane(at.pane, { ...p, active: key }), focus: at.pane })
}

/** Drops tabs from a pane, choosing the next active tab the way a
 * browser does: the one to the right, else the one to the left. */
function without(i: number, drop: (t: Tab) => boolean, remember: boolean, ws = state): Workspace {
  const p = ws.panes[i] ?? { tabs: [], active: null }
  const idx = p.tabs.findIndex((t) => t.key === p.active)
  const tabs = p.tabs.filter((t) => !drop(t))
  if (remember) {
    p.tabs.forEach((t, index) => {
      if (drop(t)) closed.push({ tab: t, pane: i, index })
    })
    while (closed.length > CLOSED_MAX) closed.shift()
  }
  let active = p.active
  if (!tabs.some((t) => t.key === active)) {
    const after = p.tabs.slice(idx + 1).find((t) => !drop(t))
    const before = p.tabs.slice(0, Math.max(0, idx)).reverse().find((t) => !drop(t))
    active = (after ?? before)?.key ?? null
  }
  return setPane(i, { tabs, active }, ws)
}

export function close(key: string): void {
  const at = find(key)
  if (!at) return
  commit(without(at.pane, (t) => t.key === key, true))
}

/** Closes every tab showing a note: it was deleted, or is out of reach. */
export function closeNote(id: string): void {
  let next = state
  for (let i = 0; i < state.panes.length; i++) next = without(i, (t) => t.id === id, false, next)
  commit(next)
}

export function closeOthers(key: string): void {
  const at = find(key)
  if (!at) return
  const next = without(at.pane, (t) => t.key !== key && !t.pinned, true)
  commit(setPane(at.pane, { ...(next.panes[at.pane] as Pane), active: key }, next))
}

export function closeRight(key: string): void {
  const at = find(key)
  if (!at) return
  const idx = pane(at.pane).tabs.findIndex((t) => t.key === key)
  const right = new Set(pane(at.pane).tabs.slice(idx + 1).filter((t) => !t.pinned).map((t) => t.key))
  commit(without(at.pane, (t) => right.has(t.key), true))
}

/** Brings back the last closed tab, where it was. */
export function reopen(): Tab | null {
  for (;;) {
    const last = closed.pop()
    if (!last) return null
    const i = Math.min(last.pane, state.panes.length - 1)
    const p = pane(i)
    if (p.tabs.some((t) => same(t.id, last.tab.id))) continue
    const tab: Tab = { ...last.tab, preview: false }
    const tabs = p.tabs.slice()
    const pinned = tabs.filter((t) => t.pinned).length
    tabs.splice(tab.pinned ? Math.min(last.index, pinned) : Math.max(pinned, Math.min(last.index, tabs.length)), 0, tab)
    commit({ ...setPane(i, { tabs, active: tab.key }), focus: i })
    return tab
  }
}

/** Makes the preview tab a normal one. */
export function keep(key: string): void {
  const at = find(key)
  if (!at || !at.tab.preview) return
  update(key, (t) => ({ ...t, preview: false }))
}

/** Pins or unpins a tab; a pinned tab moves to the end of the pinned
 * group, an unpinned one to the start of the rest. */
export function togglePin(key: string): void {
  const at = find(key)
  if (!at) return
  const p = pane(at.pane)
  const tab: Tab = { ...at.tab, pinned: !at.tab.pinned, preview: false }
  const rest = p.tabs.filter((t) => t.key !== key)
  const pinned = rest.filter((t) => t.pinned).length
  rest.splice(pinned, 0, tab)
  commit(setPane(at.pane, { ...p, tabs: rest }))
}

/** Moves a tab to a position in a pane's strip, kept inside its group
 * (pinned or not). */
export function move(key: string, to: number, toPane?: number): void {
  const at = find(key)
  if (!at) return
  const dest = toPane ?? at.pane
  if (dest !== at.pane) {
    moveToPane(key, dest, to)
    return
  }
  const p = pane(at.pane)
  const from = p.tabs.findIndex((t) => t.key === key)
  const tabs = p.tabs.filter((t) => t.key !== key)
  const pinned = tabs.filter((t) => t.pinned).length
  let i = to > from ? to - 1 : to
  i = at.tab.pinned ? Math.min(Math.max(0, i), pinned) : Math.max(pinned, Math.min(i, tabs.length))
  tabs.splice(i, 0, at.tab)
  commit(setPane(at.pane, { ...p, tabs }))
}

/** The next or previous tab in the focused strip, wrapping. */
export function step(dir: 1 | -1): Tab | null {
  const p = pane()
  if (p.tabs.length === 0) return null
  const idx = p.tabs.findIndex((t) => t.key === p.active)
  return p.tabs[(idx + dir + p.tabs.length) % p.tabs.length] ?? null
}

/** Tab n (1-8) in the focused strip; 9 is the last one. */
export function nth(n: number): Tab | null {
  const p = pane()
  return (n === 9 ? p.tabs[p.tabs.length - 1] : p.tabs[n - 1]) ?? null
}

export function setMode(key: string, mode: OpenMode): void {
  const at = find(key)
  if (!at || at.tab.mode === mode) return
  update(key, (t) => ({ ...t, mode }))
}

/** Where a tab's note is scrolled to. Nothing on screen depends on it,
 * so it is stored without announcing a change. */
export function setScroll(key: string, y: number): void {
  const at = find(key)
  if (!at) return
  at.tab.scroll = Math.max(0, Math.round(y))
  save()
}

/** Titles from the tree, so a renamed note's tab follows it. */
export function setTitles(titles: Map<string, string>): void {
  let changed = false
  const panes = state.panes.map((p) => ({
    ...p,
    tabs: p.tabs.map((t) => {
      const title = titles.get(t.id)
      if (title === undefined || title === t.title) return t
      changed = true
      return { ...t, title }
    }),
  }))
  if (changed) commit({ ...state, panes })
}

// --- storage ---------------------------------------------------------------

let saveTimer: number | undefined

function save(): void {
  window.clearTimeout(saveTimer)
  saveTimer = window.setTimeout(() => prefs.setTabState(JSON.stringify(state)), 150)
}

// Leaving the page must not lose the last scroll position.
window.addEventListener('pagehide', () => {
  window.clearTimeout(saveTimer)
  prefs.setTabState(JSON.stringify(state))
})

function load(): Workspace {
  const empty: Workspace = { panes: [{ tabs: [], active: null }], focus: 0, divider: 0.5 }
  const raw = prefs.tabState()
  if (!raw) return empty
  try {
    const v = JSON.parse(raw) as { panes?: unknown; focus?: unknown; divider?: unknown }
    if (!Array.isArray(v.panes)) return empty
    const panes: Pane[] = v.panes
      .map((p: { tabs?: unknown; active?: unknown }) => {
        const tabs = (Array.isArray(p?.tabs) ? p.tabs : []).filter(isTab).map((t) => ({ ...t, scroll: Number(t.scroll) || 0 }))
        const active = typeof p?.active === 'string' && tabs.some((t) => t.key === p.active) ? p.active : (tabs[0]?.key ?? null)
        return { tabs, active }
      })
      .filter((p, i) => i === 0 || p.tabs.length > 0)
      .slice(0, 2)
    if (panes.length === 0) return empty
    const focus = typeof v.focus === 'number' && v.focus >= 0 && v.focus < panes.length ? v.focus : 0
    const divider = typeof v.divider === 'number' && v.divider >= 0.1 && v.divider <= 0.9 ? v.divider : 0.5
    return { panes, focus, divider }
  } catch {
    return empty
  }
}

function isTab(t: unknown): t is Tab {
  if (typeof t !== 'object' || t === null) return false
  const x = t as Partial<Tab>
  return (
    typeof x.key === 'string' &&
    typeof x.id === 'string' &&
    (/^[0-9A-Za-z]{26}$/.test(x.id) || isPage(x.id)) &&
    (x.mode === 'read' || x.mode === 'edit' || x.mode === 'split') &&
    typeof x.preview === 'boolean' &&
    typeof x.pinned === 'boolean' &&
    typeof x.title === 'string'
  )
}
