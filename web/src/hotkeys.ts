// One place for the global keys, so the palette can list them and the
// keydown handler can match them. "Mod" is Command on a Mac and Control
// elsewhere; the browser-reserved combinations (Mod+N, Mod+T, Mod+W) are
// avoided on purpose since they cannot be intercepted.

export const isMac = /Mac|iPhone|iPad/.test(navigator.platform)

export interface Hotkey {
  key: string
  mod?: boolean
  shift?: boolean
  alt?: boolean
}

export const keys = {
  palette: { key: 'k', mod: true } as Hotkey,
  switcher: { key: 'p', mod: true } as Hotkey,
  newNote: { key: 'n', alt: true } as Hotkey,
  daily: { key: 'd', alt: true } as Hotkey,
  capture: { key: 'c', alt: true } as Hotkey,
  search: { key: 'f', mod: true, shift: true } as Hotkey,
  split: { key: 'e', mod: true } as Hotkey,
}

export function matches(ev: KeyboardEvent, hk: Hotkey): boolean {
  const mod = isMac ? ev.metaKey : ev.ctrlKey
  const other = isMac ? ev.ctrlKey : ev.metaKey
  if (other) return false
  if (Boolean(hk.mod) !== mod) return false
  if (Boolean(hk.shift) !== ev.shiftKey) return false
  if (Boolean(hk.alt) !== ev.altKey) return false
  // Alt changes ev.key on a Mac (⌥n = "˜"); compare on the physical key.
  const code = ev.code.startsWith('Key') ? ev.code.slice(3).toLowerCase() : ev.key.toLowerCase()
  return code === hk.key || ev.key.toLowerCase() === hk.key
}

export function label(hk: Hotkey): string {
  const parts: string[] = []
  if (hk.mod) parts.push(isMac ? '⌘' : 'Ctrl')
  if (hk.alt) parts.push(isMac ? '⌥' : 'Alt')
  if (hk.shift) parts.push(isMac ? '⇧' : 'Shift')
  parts.push(hk.key.length === 1 ? hk.key.toUpperCase() : hk.key)
  return parts.join(isMac ? '' : '+')
}

export function isEditable(el: Element | null): boolean {
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (el as HTMLElement).isContentEditable
}
