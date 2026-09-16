// Per-browser preferences: theme, text size and measure, editor layout,
// sidebar state, default spaces, the display name, recently opened
// notes. Everything lives in localStorage under one prefix and is read
// through here so the shell, the editor and the settings pages agree on
// the keys. Storage can be unavailable (private windows, blocked site
// data); every access is guarded and falls back to the default. A change
// made anywhere is announced through onChange so an open shell follows
// the settings page without a reload.

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

/** Resolves the preference against the system and stamps the root. */
export function applyTheme(): void {
  const t = theme()
  const dark = t === 'dark' || (t === 'system' && darkQuery.matches)
  document.documentElement.dataset['theme'] = dark ? 'dark' : 'light'
  const meta = document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')
  if (meta) meta.content = dark ? '#1c1a17' : '#f3efe7'
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
