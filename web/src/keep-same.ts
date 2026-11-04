// Structural sharing for JSON values. No imports; the test runner loads it
// directly.

const keyOf = (x: unknown, i: number): unknown => {
  if (x && typeof x === 'object' && !Array.isArray(x)) {
    const o = x as { path?: unknown; name?: unknown }
    if (typeof o.path === 'string') return o.path
    if (typeof o.name === 'string') return o.name
  }
  return i
}

/** `next` with every part equal to the same part of `prev` swapped for
 * prev's own object, so a memo keyed on identity sees it unchanged; prev
 * itself when nothing changed. Array items match by `path` or `name` when
 * they have one (an insert does not shift the rest), else by position. */
export function keepSame<T>(prev: unknown, next: T): T {
  if (prev === next || typeof prev !== 'object' || typeof next !== 'object' || prev === null || next === null) return next
  if (Array.isArray(next)) {
    if (!Array.isArray(prev)) return next
    const by = new Map<unknown, unknown>()
    prev.forEach((p, i) => by.set(keyOf(p, i), p))
    let same = prev.length === next.length
    const out = next.map((n, i) => {
      const v = keepSame(by.get(keyOf(n, i)), n)
      if (v !== prev[i]) same = false
      return v
    })
    return (same ? prev : out) as T
  }
  if (Array.isArray(prev)) return next
  const p = prev as Record<string, unknown>, n = next as Record<string, unknown>
  const keys = Object.keys(n)
  let same = keys.length === Object.keys(p).length
  const out: Record<string, unknown> = {}
  for (const k of keys) {
    out[k] = keepSame(p[k], n[k])
    if (out[k] !== p[k]) same = false
  }
  return (same ? p : out) as T
}
