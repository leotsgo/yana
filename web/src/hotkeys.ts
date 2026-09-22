// One place for the global keys, so the palette can list them and the
// keydown handler can match them. "Mod" is Command on a Mac and Control
// elsewhere; the browser-reserved combinations (Mod+N, Mod+T) are avoided
// on purpose since they cannot be intercepted. The tab keys use the
// usual Mod+W and Mod+Shift+T, which a browser tab keeps for itself but
// the installed app receives, so each has an Alt twin that always works.

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
  newNote: { key: 't', alt: true } as Hotkey,
  newNoteAlt: { key: 'n', alt: true } as Hotkey,
  daily: { key: 'd', alt: true } as Hotkey,
  capture: { key: 'c', alt: true } as Hotkey,
  tasks: { key: 'k', alt: true } as Hotkey,
  search: { key: 'f', mod: true, shift: true } as Hotkey,
  split: { key: 'e', mod: true } as Hotkey,
  closeTab: { key: 'w', mod: true } as Hotkey,
  closeTabAlt: { key: 'w', alt: true } as Hotkey,
  reopenTab: { key: 't', mod: true, shift: true } as Hotkey,
  reopenTabAlt: { key: 't', alt: true, shift: true } as Hotkey,
  nextTabAlt: { key: ']', alt: true } as Hotkey,
  prevTabAlt: { key: '[', alt: true } as Hotkey,
}

/** The physical key, for the keys Alt or Shift change the character of. */
function physical(ev: KeyboardEvent): string {
  const c = ev.code
  if (c.startsWith('Key')) return c.slice(3).toLowerCase()
  if (c.startsWith('Digit')) return c.slice(5)
  if (c === 'BracketRight') return ']'
  if (c === 'BracketLeft') return '['
  if (c === 'Backslash') return '\\'
  return ev.key.toLowerCase()
}

export function matches(ev: KeyboardEvent, hk: Hotkey): boolean {
  const mod = isMac ? ev.metaKey : ev.ctrlKey
  const other = isMac ? ev.ctrlKey : ev.metaKey
  if (other) return false
  if (Boolean(hk.mod) !== mod) return false
  if (Boolean(hk.shift) !== ev.shiftKey) return false
  if (Boolean(hk.alt) !== ev.altKey) return false
  // Alt changes ev.key on a Mac (⌥n = "˜"); compare on the physical key.
  const want = hk.key.toLowerCase()
  return physical(ev) === want || ev.key.toLowerCase() === want
}

export function label(hk: Hotkey): string {
  const parts: string[] = []
  if (hk.mod) parts.push(isMac ? '⌘' : 'Ctrl')
  if (hk.alt) parts.push(isMac ? '⌥' : 'Alt')
  if (hk.shift) parts.push(isMac ? '⇧' : 'Shift')
  parts.push(hk.key.length === 1 ? hk.key.toUpperCase() : (ARROWS[hk.key] ?? hk.key))
  return parts.join(isMac ? '' : '+')
}

const ARROWS: Record<string, string> = { ArrowLeft: '←', ArrowRight: '→', ArrowUp: '↑', ArrowDown: '↓' }

/** Mod+1 to Mod+9, or Alt+1 to Alt+9 (a browser tab keeps Mod+digit
 * for itself): the digit, or 0 for anything else. */
export function tabDigit(ev: KeyboardEvent): number {
  const mod = isMac ? ev.metaKey : ev.ctrlKey
  const other = isMac ? ev.ctrlKey : ev.metaKey
  if (ev.shiftKey || other || mod === ev.altKey) return 0
  const m = /^Digit([1-9])$/.exec(ev.code)
  return m ? Number(m[1]) : 0
}

export function isEditable(el: Element | null): boolean {
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (el as HTMLElement).isContentEditable
}
