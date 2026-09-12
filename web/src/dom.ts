// Small element helpers so the rest of the client reads as structure, not
// as a wall of createElement calls.

type Attrs = Record<string, string | boolean | ((ev: Event) => void) | undefined>
type Child = Node | string | null | undefined | false

export function h<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  attrs: Attrs = {},
  ...children: Child[]
): HTMLElementTagNameMap[K] {
  const el = document.createElement(tag)
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === false) continue
    if (k.startsWith('on') && typeof v === 'function') {
      el.addEventListener(k.slice(2).toLowerCase(), v)
    } else if (k === 'class') {
      el.className = String(v)
    } else if (v === true) {
      el.setAttribute(k, '')
    } else {
      el.setAttribute(k, String(v))
    }
  }
  for (const c of children) {
    if (c === null || c === undefined || c === false) continue
    el.append(typeof c === 'string' ? document.createTextNode(c) : c)
  }
  return el
}

export function clear(el: Element): void {
  while (el.firstChild) el.removeChild(el.firstChild)
}

export function debounce<A extends unknown[]>(fn: (...args: A) => void, ms: number): (...args: A) => void {
  let t: number | undefined
  return (...args: A) => {
    window.clearTimeout(t)
    t = window.setTimeout(() => fn(...args), ms)
  }
}

export function fmtDate(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}
