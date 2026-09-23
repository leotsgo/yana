// Suggested names for the new-note picker (exploratory). A folder whose
// notes follow a clear pattern gets the next name in it: today's date in
// a folder of dated notes, the next number in a run of numbered ones.
// Anything less clear gets nothing. Kept apart so it comes out cleanly.

const DATED = /^\d{4}-\d{2}-\d{2}$/
const NUMBERED = /^(.*?)(\d+)$/

/** The next name for a folder, given the names (without extension) of
 * the notes in it and today's date; null when there is no clear pattern. */
export function suggestName(names: string[], today: string): string | null {
  if (names.length === 0) return null
  // At least half of them named by date: today.
  const dated = names.filter((n) => DATED.test(n)).length
  if (dated * 2 >= names.length) return today
  // The largest run of names that end in a number, sharing what comes
  // before it, at least two of them and half the folder: the next number.
  const runs = new Map<string, number[]>()
  for (const n of names) {
    const m = NUMBERED.exec(n)
    if (!m || !m[1]?.trim()) continue
    const key = m[1].toLowerCase()
    runs.set(key, [...(runs.get(key) ?? []), Number(m[2])])
  }
  let best: { prefix: string; nums: number[] } | null = null
  for (const [prefix, nums] of runs) if (!best || nums.length > best.nums.length) best = { prefix, nums }
  if (!best || best.nums.length < 2 || best.nums.length * 2 < names.length) return null
  const { prefix, nums } = best
  const max = Math.max(...nums)
  // Spelled as the highest one is, zero padding and all.
  const top = names.find((n) => {
    const m = NUMBERED.exec(n)
    return m && m[1]?.toLowerCase() === prefix && Number(m[2]) === max
  })
  const m = top ? NUMBERED.exec(top) : null
  if (!m) return null
  const digits = m[2] ?? ''
  const next = String(max + 1)
  return (m[1] ?? '') + (digits.startsWith('0') ? next.padStart(digits.length, '0') : next)
}
