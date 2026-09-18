// Subsequence matching for the quick switcher and the palette: every
// query character must appear in order; runs of consecutive matches and
// matches at word or path boundaries score higher. Case-insensitive.

export interface FuzzyMatch {
  score: number
  positions: number[]
}

export function fuzzy(query: string, text: string): FuzzyMatch | null {
  const q = query.toLowerCase()
  const t = text.toLowerCase()
  if (q === '') return { score: 0, positions: [] }
  // The boundary look-ahead can skip past the only run that works
  // ("picture" against "put a picture in a note" jumps to the i of
  // "in"), so a plain left-to-right pass is the fallback.
  return scan(q, t, true) ?? scan(q, t, false)
}

function scan(q: string, t: string, lookAhead: boolean): FuzzyMatch | null {
  const positions: number[] = []
  let score = 0
  let ti = 0
  let prev = -2
  for (let qi = 0; qi < q.length; qi++) {
    const ch = q[qi]
    const idx = t.indexOf(ch as string, ti)
    if (idx < 0) return null
    // Prefer a later occurrence that sits on a boundary when the next
    // one is mid-word; a cheap look-ahead keeps "jou" matching journal/
    // over j-o-u scattered across a long path.
    let at = idx
    if (lookAhead && !isBoundary(t, idx)) {
      const next = t.indexOf(ch as string, idx + 1)
      if (next >= 0 && isBoundary(t, next) && next - idx < 24) at = next
    }
    positions.push(at)
    score += 1
    if (at === prev + 1) score += 3
    if (isBoundary(t, at)) score += 2
    prev = at
    ti = at + 1
  }
  // Shorter targets win ties; early matches win too.
  score -= (positions[0] ?? 0) * 0.05 + t.length * 0.01
  return { score, positions }
}

function isBoundary(t: string, i: number): boolean {
  if (i === 0) return true
  const p = t[i - 1] as string
  return p === '/' || p === ' ' || p === '-' || p === '_' || p === '.'
}
