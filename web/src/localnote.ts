// Notes made on this device while offline: the id they carry, how they
// show in the tree until the server has them, and what happens to their
// text when today's note turned out to exist already. Pure functions, so
// the outbox and the shell share them and the tests run them in Node.

import type { SpaceTree, TreeNode } from './api'

const B32 = '0123456789ABCDEFGHJKMNPQRSTVWXYZ' // Crockford, as ULIDs spell it

/** A ULID, the shape the server's scanner gives every note: 10
 * characters of millisecond time, 16 of randomness, upper case. */
export function newID(now = Date.now(), rand: Uint8Array = crypto.getRandomValues(new Uint8Array(16))): string {
  let t = ''
  for (let i = 0, n = now; i < 10; i++, n = Math.floor(n / 32)) t = B32[n % 32] + t
  let r = ''
  for (let i = 0; i < 16; i++) r += B32[(rand[i] ?? 0) & 31]
  return t + r
}

/** A local note: its id and where it will be made. */
export interface LocalNote {
  id: string
  path: string
}

/** The tree with the notes made offline in it, marked local, folders
 * made for them as needed. One the tree already has (the server made it)
 * is left out. The input is not changed. */
export function withLocal(spaces: SpaceTree[], locals: LocalNote[]): SpaceTree[] {
  if (locals.length === 0) return spaces
  const known = new Set<string>()
  const walk = (n: TreeNode) => {
    if (n.id) known.add(n.id)
    n.children?.forEach(walk)
  }
  spaces.forEach((s) => s.children.forEach(walk))
  let out = spaces
  for (const l of locals) {
    if (known.has(l.id)) continue
    const parts = l.path.split('/')
    const space = parts.length > 1 ? parts.shift()! : ''
    const name = parts.pop() ?? l.path
    const note: TreeNode = { type: 'note', name, path: l.path, id: l.id, title: name.replace(/\.(md|markdown)$/i, ''), kind: 'md', local: true }
    const into = (children: TreeNode[], dirs: string[], at: string): TreeNode[] => {
      const [d, ...rest] = dirs
      if (d === undefined) return [...children, note]
      const p = at ? `${at}/${d}` : d
      const i = children.findIndex((c) => c.type === 'dir' && c.name === d)
      const dir: TreeNode = i >= 0 ? children[i]! : { type: 'dir', name: d, path: p, children: [] }
      const next = { ...dir, children: into(dir.children ?? [], rest, p) }
      return i >= 0 ? children.map((c, j) => (j === i ? next : c)) : [...children, next]
    }
    const si = out.findIndex((s) => s.name === space)
    const sp = si >= 0 ? out[si]! : { name: space, notes: 0, children: [] }
    const nextSpace = { ...sp, notes: sp.notes + 1, children: into(sp.children, parts, space) }
    out = si >= 0 ? out.map((s, j) => (j === si ? nextSpace : s)) : [...out, nextSpace]
  }
  return out
}

/** The body a daily note made offline starts with. */
export function dailySeed(date: string): string {
  return `# ${date}\n\n`
}

/** What goes onto the end of today's note when another device made it
 * while this one was offline: the text written here, under a rule, the
 * seed heading left out. Empty when nothing was written beyond it. */
export function offlineBlock(text: string, seed: string): string {
  let rest = text
  if (rest.startsWith(seed.trimEnd())) rest = rest.slice(seed.trimEnd().length)
  rest = rest.trim()
  return rest ? `\n---\n\n${rest}\n` : ''
}

/** The path with " n" before its extension: the next name to try when
 * the one picked is taken. */
export function numbered(path: string, n: number): string {
  const m = /^(.*?)(?: \d+)?(\.[^./]+)$/.exec(path)
  return m ? `${m[1]} ${n}${m[2]}` : `${path} ${n}`
}
