import { api, ApiError, type RegexHit, type SearchHit } from './api'
import { h, clear, debounce } from './dom'

export interface SearchView {
  // Returns true while results are being shown (the tree is hidden).
  active(): boolean
}

export function createSearch(
  input: HTMLInputElement,
  regexToggle: HTMLInputElement,
  results: HTMLElement,
  onOpen: (id: string) => void,
  onActiveChange: (active: boolean) => void,
): SearchView {
  let active = false
  let seq = 0

  function setActive(v: boolean): void {
    if (active === v) return
    active = v
    onActiveChange(v)
  }

  function renderFts(hits: SearchHit[]): void {
    clear(results)
    if (hits.length === 0) {
      results.append(h('p', { class: 'empty' }, 'No notes match.'))
      return
    }
    for (const hit of hits) {
      const snippet = h('div', { class: 'hit-snippet' })
      snippet.innerHTML = escapeExceptMark(hit.snippet)
      results.append(
        h(
          'a',
          {
            class: 'hit',
            href: `/n/${hit.note.id}`,
            onClick: (ev) => {
              ev.preventDefault()
              onOpen(hit.note.id)
            },
          },
          h('div', { class: 'hit-title' }, hit.note.title),
          h('div', { class: 'hit-path' }, hit.note.path),
          snippet,
        ),
      )
    }
  }

  function renderRegex(hits: RegexHit[]): void {
    clear(results)
    if (hits.length === 0) {
      results.append(h('p', { class: 'empty' }, 'No lines match.'))
      return
    }
    for (const hit of hits) {
      const row = h(
        'a',
        {
          class: 'hit hit-regex' + (hit.id ? '' : ' unindexed'),
          href: hit.id ? `/n/${hit.id}` : '#',
          onClick: (ev) => {
            ev.preventDefault()
            if (hit.id) onOpen(hit.id)
          },
        },
        h('div', { class: 'hit-path' }, `${hit.path}:${hit.line}`),
        h('pre', { class: 'hit-line' }, hit.text),
      )
      results.append(row)
    }
  }

  const run = debounce(async () => {
    const q = input.value.trim()
    const mine = ++seq
    if (q === '') {
      setActive(false)
      clear(results)
      return
    }
    setActive(true)
    clear(results)
    results.append(h('p', { class: 'empty' }, 'Searching…'))
    try {
      if (regexToggle.checked) {
        const res = await api.regex(q)
        if (mine === seq) renderRegex(res.hits)
      } else {
        const res = await api.search(q)
        if (mine === seq) renderFts(res.hits)
      }
    } catch (err) {
      if (mine !== seq) return
      clear(results)
      const msg = err instanceof ApiError ? err.message : 'Search failed.'
      results.append(h('p', { class: 'error' }, msg))
    }
  }, 150)

  input.addEventListener('input', run)
  regexToggle.addEventListener('change', run)
  input.addEventListener('keydown', (ev) => {
    if (ev.key === 'Escape') {
      input.value = ''
      run()
      input.blur()
    }
  })

  return { active: () => active }
}

// The FTS snippet is server-generated text with <mark> boundaries; escape
// everything else so a note cannot inject markup through its own body.
function escapeExceptMark(s: string): string {
  const esc = s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  return esc.replace(/&lt;mark&gt;/g, '<mark>').replace(/&lt;\/mark&gt;/g, '</mark>')
}
