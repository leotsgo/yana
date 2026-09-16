// Per-browser preferences: theme, editor layout, sidebar state, recently
// opened notes. Everything lives in localStorage under one prefix and is
// read through here so the shell, the editor and later the settings pages
// agree on the keys. Storage can be unavailable (private windows, blocked
// site data); every access is guarded and falls back to the default.

export type Theme = 'light' | 'dark' | 'system'
export type OpenMode = 'edit' | 'split'

const PREFIX = 'yana.'
const RECENTS_MAX = 12

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

// --- layout --------------------------------------------------------------

/** Whether the editor opens beside its preview on wide screens. */
export function openMode(): OpenMode {
  return read('preview') === '0' ? 'edit' : 'split'
}

export function setOpenMode(m: OpenMode): void {
  write('preview', m === 'split' ? '1' : '0')
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
