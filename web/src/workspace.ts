// The open tabs: which notes are open in the content pane, in what
// order, which one shows, and how each was left (read, edit or split,
// and how far down). A tab is a record, not a mounted page: only the
// active tab's note is on screen and holds a realtime session; the rest
// are an id and the state to come back to. At most one tab is a preview:
// browsing (a click in the tree, a search hit, a wikilink) replaces it
// instead of adding tabs, and editing it, a double-click, or Keep open
// makes it a normal tab. Pinned tabs sit leftmost. The state is kept per
// device through prefs, and a change is announced through subscribe.

import { isMac } from './hotkeys'
import type { OpenMode } from './prefs'
import * as prefs from './prefs'

export interface Tab {
  /** Stable for the life of the tab; history entries carry it. */
  key: string
  /** The note. */
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
  panes: Pane[]
  /** The pane the keys and the tree act on. */
  focus: number
}

/** How a note opens: in the preview tab, in a new tab brought to the
 * front, in a new tab behind the current one, or in place of the
 * current tab (the phone, which has no strip). */
export type OpenHow = 'preview' | 'tab' | 'background' | 'here'

const CLOSED_MAX = 20

/** How a click asks for a note: Mod-click or the middle button opens it
 * in a tab behind the current one. A plain click is undefined: the
 * caller's default, which is the preview tab. */
export function howFrom(ev: MouseEvent): OpenHow | undefined {
  if (ev.button === 1 || (isMac ? ev.metaKey : ev.ctrlKey)) return 'background'
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
  state = next
  save()
  for (const fn of listeners) fn()
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

  if (how === 'here') {
    const cur = p.tabs.find((t) => t.key === p.active)
    if (!cur) return open(id, 'preview', init, at)
    if (cur.id === id) return cur
    const next: Tab = { ...cur, id, mode: init.mode, scroll: 0, title: init.title ?? '' }
    commit(setPane(at, { ...p, tabs: p.tabs.filter((t) => t.id !== id || t.key === cur.key).map((t) => (t.key === cur.key ? next : t)) }))
    return next
  }

  const existing = p.tabs.find((t) => t.id === id)
  if (existing) {
    if (how === 'background') return existing
    const tab = how === 'tab' && existing.preview ? { ...existing, preview: false } : existing
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

/** Points an existing tab at another note: back and forward within a tab. */
export function retarget(key: string, id: string, mode: OpenMode, title: string): void {
  const at = find(key)
  if (!at) return
  const p = pane(at.pane)
  const tabs = p.tabs
    .filter((t) => t.id !== id || t.key === key)
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
    if (p.tabs.some((t) => t.id === last.tab.id)) continue
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
  if (dest !== at.pane) return
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
  const empty: Workspace = { panes: [{ tabs: [], active: null }], focus: 0 }
  const raw = prefs.tabState()
  if (!raw) return empty
  try {
    const v = JSON.parse(raw) as { panes?: unknown; focus?: unknown }
    if (!Array.isArray(v.panes)) return empty
    const panes: Pane[] = v.panes
      .map((p: { tabs?: unknown; active?: unknown }) => {
        const tabs = (Array.isArray(p?.tabs) ? p.tabs : []).filter(isTab).map((t) => ({ ...t, scroll: Number(t.scroll) || 0 }))
        const active = typeof p?.active === 'string' && tabs.some((t) => t.key === p.active) ? p.active : (tabs[0]?.key ?? null)
        return { tabs, active }
      })
      .filter((p, i) => i === 0 || p.tabs.length > 0)
      .slice(0, 1)
    if (panes.length === 0) return empty
    const focus = typeof v.focus === 'number' && v.focus >= 0 && v.focus < panes.length ? v.focus : 0
    return { panes, focus }
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
    /^[0-9A-Za-z]{26}$/.test(x.id) &&
    (x.mode === 'read' || x.mode === 'edit' || x.mode === 'split') &&
    typeof x.preview === 'boolean' &&
    typeof x.pinned === 'boolean' &&
    typeof x.title === 'string'
  )
}
