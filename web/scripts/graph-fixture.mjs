// Writes a synthetic vault for tuning the graph view:
//   node web/scripts/graph-fixture.mjs <dir> [count=5700]
// <dir> is the notes root; the notes land in the space <dir>/zk/ as
// "Note 0001.md"…, the same every run (seeded). Clusters of ~60 notes with
// 1–4 links each (~5 % leaving the cluster), ~8 % orphans, 5 hubs linking
// ~200 notes each, ~3 % links to [[missing-N]], tags #t0…#t9 on ~20 %.
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'

const [dir, countArg = '5700'] = process.argv.slice(2)
const count = Number(countArg)
if (!dir || !Number.isInteger(count) || count < 1) {
  console.error('usage: graph-fixture.mjs <dir> [count=5700]')
  process.exit(2)
}

/** mulberry32: a small seeded PRNG, so every run writes the same vault. */
function mulberry32(a) {
  return () => {
    a = (a + 0x6d2b79f5) | 0
    let t = Math.imul(a ^ (a >>> 15), 1 | a)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}
const rand = mulberry32(42)
const int = (n) => Math.floor(rand() * n)

const CLUSTER = 60
const width = Math.max(4, String(count).length)
const name = (i) => `Note ${String(i + 1).padStart(width, '0')}`

const orphan = Array.from({ length: count }, () => rand() < 0.08)
const linked = [...orphan.keys()].filter((i) => !orphan[i])
const hubs = new Set()
while (hubs.size < Math.min(5, linked.length)) hubs.add(linked[int(linked.length)])

/** A random linked note: same cluster as `i` most of the time. */
function target(i) {
  const base = Math.floor(i / CLUSTER) * CLUSTER
  for (let tries = 0; tries < 20; tries++) {
    const j = rand() < 0.05 ? int(count) : base + int(Math.min(CLUSTER, count - base))
    if (j !== i && !orphan[j]) return j
  }
  return null
}

mkdirSync(join(dir, 'zk'), { recursive: true })
for (let i = 0; i < count; i++) {
  const lines = [`# ${name(i)}`, '', `Synthetic note ${i + 1} for tuning the graph view.`]
  if (!orphan[i]) {
    const out = new Set()
    const n = hubs.has(i) ? 200 : 1 + int(4)
    for (let k = 0; k < n * 2 && out.size < n; k++) {
      if (rand() < 0.03) { out.add(`missing-${int(100)}`); continue }
      const j = hubs.has(i) ? linked[int(linked.length)] : target(i)
      if (j !== null && j !== i) out.add(name(j))
    }
    lines.push('', [...out].map((t) => `[[${t}]]`).join(' '))
    if (rand() < 0.2) lines.push('', `#t${int(10)}`)
  }
  writeFileSync(join(dir, 'zk', `${name(i)}.md`), lines.join('\n') + '\n')
}
console.log(`wrote ${count} notes to ${join(dir, 'zk')}`)
