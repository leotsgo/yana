// The static site's search runtime, bundled into every site export.
// It pairs with search-index.js, which the exporter writes holding the
// site's notes as JSON on window.YANA_EXPORT_INDEX. Both load as classic
// scripts, which is what makes search work from file:// where fetch
// does not.

import MiniSearch from 'minisearch'

interface IndexDoc {
  id: string
  t: string
  p: string
  h: string
  b: string
}

interface ExportIndex {
  space: string
  notes: IndexDoc[]
}

declare global {
  interface Window {
    YANA_EXPORT_INDEX?: ExportIndex
  }
}

function boot(): void {
  const input = document.getElementById('yana-search') as HTMLInputElement | null
  const results = document.getElementById('yana-results')
  const index = window.YANA_EXPORT_INDEX
  if (!input || !results || !index || index.notes.length === 0) return

  const ms = new MiniSearch<IndexDoc>({
    fields: ['t', 'p', 'b'],
    storeFields: ['t', 'p', 'h'],
    searchOptions: { prefix: true, fuzzy: 0.2, boost: { t: 4, p: 2 } },
  })
  ms.addAll(index.notes)

  const empty = results.innerHTML
  let seq = 0
  const run = (): void => {
    const q = input.value.trim()
    const mine = ++seq
    if (q === '') {
      results.innerHTML = empty
      return
    }
    const hits = ms.search(q).slice(0, 30)
    if (mine !== seq) return
    results.replaceChildren()
    if (hits.length === 0) {
      const p = document.createElement('p')
      p.className = 'x-muted'
      p.textContent = `No notes match "${q}".`
      results.append(p)
      return
    }
    for (const hit of hits) {
      const a = document.createElement('a')
      a.className = 'x-result'
      a.href = hit.h
      const t = document.createElement('span')
      t.className = 't'
      t.textContent = hit.t
      const p = document.createElement('span')
      p.className = 'p'
      p.textContent = hit.p
      a.append(t, p)
      results.append(a)
    }
  }
  input.addEventListener('input', run)
  input.focus()
}

if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', boot)
else boot()
