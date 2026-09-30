// The search-box grammar, mirrored from the server (internal/search/
// query.go): terms separated by spaces, operators of the form name:value,
// a bare #tag, double-quoted exact phrases, and - in front to negate.
// Anything the grammar does not recognise is plain full text. The mirror
// exists so the box can highlight operators as chips and offer
// completions while typing, and so the switcher can filter by them —
// with the same meaning the server applies.

/** One parsed term. start/end index the original string. */
export interface OpTerm {
  raw: string
  start: number
  end: number
  negated: boolean
  /** '' for plain text; otherwise tag, path, space, is, has, author, before, after. */
  op: string
  value: string
  quoted: boolean
  /** The searchable text of a plain term. */
  text: string
}

const OP_NAMES = ['tag', 'path', 'space', 'is', 'has', 'author', 'before', 'after'] as const
export type OpName = (typeof OP_NAMES)[number]

const IS_VALUES = ['untagged', 'task', 'html']
const HAS_VALUES = ['image', 'attachment']

/** What the empty state lists: every operator with a plain-language hint. */
export const OPERATORS: Array<{ op: OpName; example: string; hint: string }> = [
  { op: 'tag', example: 'tag:home', hint: 'notes carrying a #tag' },
  { op: 'path', example: 'path:folder/', hint: 'notes under a folder' },
  { op: 'space', example: 'space:work', hint: 'one space' },
  { op: 'is', example: 'is:untagged', hint: 'untagged · task (an open box) · html' },
  { op: 'has', example: 'has:image', hint: 'image · attachment (a file in _assets)' },
  { op: 'author', example: 'author:claude', hint: 'last edit by that person or agent' },
  { op: 'before', example: 'before:2026-01-01', hint: 'modified before a day' },
  { op: 'after', example: 'after:2026-01-01', hint: 'modified since a day' },
]

function isSpace(ch: string): boolean {
  return ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r'
}

function knownOp(name: string): boolean {
  return (OP_NAMES as readonly string[]).includes(name)
}

function validDate(v: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(v)) return false
  const y = Number(v.slice(0, 4))
  const m = Number(v.slice(5, 7))
  const d = Number(v.slice(8, 10))
  return m >= 1 && m <= 12 && d >= 1 && d <= 31 && y > 0
}

/** Reads a quoted run starting at qs[start] (the opening quote). */
function quotedRun(q: string, start: number): { content: string; end: number } {
  for (let i = start + 1; i < q.length; i++) {
    if (q[i] === '"') return { content: q.slice(start + 1, i), end: i + 1 }
  }
  return { content: q.slice(start + 1), end: q.length }
}

function wordEnd(q: string, i: number): number {
  while (i < q.length && !isSpace(q[i] ?? '')) i++
  return i
}

/** A term that failed to be an operator, kept as plain text. */
function demoted(q: string, start: number, end: number, negated: boolean): OpTerm {
  return { raw: q.slice(start, end), start, end, negated, op: '', value: '', quoted: false, text: q.slice(start + (negated ? 1 : 0), end) }
}

function operatorTerm(q: string, start: number, valStart: number, rawEnd: number, negated: boolean, name: string, value: string, quoted: boolean): OpTerm {
  const base = { raw: q.slice(start, rawEnd), start, end: rawEnd, negated, quoted, text: '' }
  if (value === '') {
    // An operator with nothing after its colon yet: kept as an operator
    // so the completions can pick up what belongs there. The server
    // treats the same term as plain text.
    return { ...base, op: name, value: '' }
  }
  switch (name) {
    case 'is':
      if (!IS_VALUES.includes(value.toLowerCase())) return demoted(q, start, rawEnd, negated)
      return { ...base, op: name, value: value.toLowerCase() }
    case 'has':
      if (!HAS_VALUES.includes(value.toLowerCase())) return demoted(q, start, rawEnd, negated)
      return { ...base, op: name, value: value.toLowerCase() }
    case 'before':
    case 'after':
      if (!validDate(value)) return demoted(q, start, rawEnd, negated)
      return { ...base, op: name, value }
    case 'tag':
      return { ...base, op: name, value: value.toLowerCase() }
    default:
      return { ...base, op: name, value }
  }
}

/** Parses a query into terms. See the file comment for the grammar. */
export function parseQuery(q: string): OpTerm[] {
  const out: OpTerm[] = []
  let i = 0
  while (i < q.length) {
    while (i < q.length && isSpace(q[i] ?? '')) i++
    if (i >= q.length) break
    const start = i
    let negated = false
    if (q[i] === '-' && i + 1 < q.length && !isSpace(q[i + 1] ?? '')) {
      negated = true
      i++
    }
    if (i >= q.length) break
    if (q[i] === '"') {
      const { content, end } = quotedRun(q, i)
      out.push({ raw: q.slice(start, end), start, end, negated, quoted: true, op: '', value: '', text: content })
      i = end
      continue
    }
    if (q[i] === '#' && (i + 1 >= q.length || !isSpace(q[i + 1] ?? ''))) {
      const end = wordEnd(q, i + 1)
      out.push({ raw: q.slice(start, end), start, end, negated, op: 'tag', value: q.slice(i + 1, end).toLowerCase(), quoted: false, text: '' })
      i = end
      continue
    }
    const colon = q.indexOf(':', i)
    if (colon > i) {
      const name = q.slice(i, colon)
      if (/^[a-z]+$/.test(name) && knownOp(name)) {
        if (q[colon + 1] === '"') {
          const { content, end } = quotedRun(q, colon + 1)
          out.push(operatorTerm(q, start, colon + 1, end, negated, name, content, true))
          i = end
          continue
        }
        const end = wordEnd(q, colon + 1)
        out.push(operatorTerm(q, start, colon + 1, end, negated, name, q.slice(colon + 1, end), false))
        i = end
        continue
      }
    }
    const end = wordEnd(q, i)
    out.push({ raw: q.slice(start, end), start, end, negated, op: '', value: '', quoted: false, text: q.slice(i, end) })
    i = end
  }
  return out
}

// --- the switcher ---------------------------------------------------------

export interface NoteLike {
  path: string
  tags: string[]
  kind?: string
}

/** Whether a note satisfies the terms the tree can evaluate: tag, path,
 * space, is:untagged, is:html. The others need the index, so the
 * switcher leaves them to the search box. */
export function noteMatches(terms: OpTerm[], note: NoteLike): boolean {
  for (const t of terms) {
    if (!t.op) continue
    const lower = note.path.toLowerCase()
    let hit: boolean
    switch (t.op) {
      case 'tag':
        hit = note.tags.includes(t.value)
        break
      case 'path': {
        const v = t.value.replace(/^\/+|\/+$/g, '')
        hit = v !== '' && (lower === v || lower.startsWith(v + '/') || lower.startsWith(v + '.'))
        break
      }
      case 'space': {
        const first = note.path.split('/')[0] ?? ''
        hit = first.toLowerCase() === t.value.toLowerCase()
        break
      }
      case 'is':
        if (t.value === 'untagged') hit = note.tags.length === 0
        else if (t.value === 'html') hit = note.kind === 'html'
        else continue // task and the rest: not the switcher's to judge
        break
      default:
        continue
    }
    if (hit === t.negated) return false
  }
  return true
}

// --- completions ----------------------------------------------------------

/** What the completions need: the tags, folders and spaces that exist. */
export interface CompletionSource {
  tags: string[]
  folders: string[]
  spaces: string[]
}

/** The term the caret is finishing: the last one, when the query does
 * not end in a space. */
export function termInProgress(q: string): OpTerm | null {
  if (q === '' || isSpace(q[q.length - 1] ?? '')) return null
  const terms = parseQuery(q)
  return terms.length > 0 ? (terms[terms.length - 1] as OpTerm) : null
}

/** What the completion list offers for the query as typed: operator
 * names for an empty box, values for a tag:/path:/space:/is:/has: term. */
export function completionsFor(q: string, src: { tags: string[]; folders: string[]; spaces: string[] }): { title: string; values: string[] } | null {
  if (q.trim() === '') {
    return { title: 'Operators', values: OPERATORS.map((o) => o.example) }
  }
  const t = termInProgress(q)
  if (!t || !t.op || t.quoted) return null
  const filter = (list: string[]): string[] => {
    const v = t.value.toLowerCase()
    const starts: string[] = []
    const contains: string[] = []
    for (const x of list) {
      const lx = x.toLowerCase()
      if (lx === v) continue
      if (lx.startsWith(v)) starts.push(x)
      else if (v !== '' && lx.includes(v)) contains.push(x)
    }
    return [...starts, ...contains].slice(0, 8)
  }
  switch (t.op) {
    case 'tag':
      return { title: 'tags', values: filter(src.tags) }
    case 'path': {
      const v = t.value.replace(/^\/+|\/+$/g, '').toLowerCase()
      const list = src.folders.filter((f) => {
        if (f.toLowerCase() === v) return false
        return f.toLowerCase().startsWith(v) || (v !== '' && f.toLowerCase().includes(v))
      })
      return { title: 'folders', values: list.slice(0, 8).map((f) => f + '/') }
    }
    case 'space':
      return { title: 'spaces', values: filter(src.spaces) }
    case 'is':
      return { title: 'is:', values: filter(IS_VALUES) }
    case 'has':
      return { title: 'has:', values: filter(HAS_VALUES) }
    default:
      return null
  }
}

/** Replaces the in-progress term's value with a picked completion. */
export function applyCompletion(q: string, value: string): string {
  const t = termInProgress(q)
  if (!t || !t.op) return q
  const body = t.negated ? t.raw.slice(1) : t.raw
  const mark = body.startsWith('#') ? '#' : t.op + ':'
  return q.slice(0, t.start) + (t.negated ? '-' : '') + mark + value + q.slice(t.end)
}
