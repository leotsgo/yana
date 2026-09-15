// Search results in the sidebar: full text through the index, or regex
// through ripgrep. The query lives in the top bar; this renders what it
// finds.

import { useEffect, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { RegexHit, SearchHit } from './api'

type Result =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'fts'; hits: SearchHit[] }
  | { kind: 'regex'; hits: RegexHit[] }
  | { kind: 'error'; message: string }

export function SearchResults({ query, regex, onOpen }: { query: string; regex: boolean; onOpen: (id: string) => void }) {
  const [result, setResult] = useState<Result>({ kind: 'idle' })

  useEffect(() => {
    const q = query.trim()
    if (q === '') {
      setResult({ kind: 'idle' })
      return
    }
    let live = true
    setResult({ kind: 'loading' })
    const t = window.setTimeout(() => {
      const run = regex
        ? api.regex(q).then((r) => ({ kind: 'regex', hits: r.hits }) as Result)
        : api.search(q).then((r) => ({ kind: 'fts', hits: r.hits }) as Result)
      run
        .then((r) => { if (live) setResult(r) })
        .catch((err: unknown) => {
          if (live) setResult({ kind: 'error', message: err instanceof ApiError ? err.message : 'Search failed.' })
        })
    }, 150)
    return () => {
      live = false
      window.clearTimeout(t)
    }
  }, [query, regex])

  switch (result.kind) {
    case 'idle':
      return null
    case 'loading':
      return <p class="empty">Searching…</p>
    case 'error':
      return <p class="error">{result.message}</p>
    case 'fts':
      if (result.hits.length === 0) return <p class="empty">No notes match.</p>
      return (
        <div class="results">
          {result.hits.map((hit) => (
            <a
              key={hit.note.id}
              class="hit"
              href={`/n/${hit.note.id}`}
              onClick={(ev) => { ev.preventDefault(); onOpen(hit.note.id) }}
            >
              <div class="hit-title">{hit.note.title}</div>
              <div class="hit-path">{hit.note.path}</div>
              <div class="hit-snippet" dangerouslySetInnerHTML={{ __html: escapeExceptMark(hit.snippet) }} />
            </a>
          ))}
        </div>
      )
    case 'regex':
      if (result.hits.length === 0) return <p class="empty">No lines match.</p>
      return (
        <div class="results">
          {result.hits.map((hit, i) => (
            <a
              key={`${hit.path}:${hit.line}:${i}`}
              class={'hit hit-regex' + (hit.id ? '' : ' unindexed')}
              href={hit.id ? `/n/${hit.id}` : '#'}
              onClick={(ev) => { ev.preventDefault(); if (hit.id) onOpen(hit.id) }}
            >
              <div class="hit-path">{`${hit.path}:${hit.line}`}</div>
              <pre class="hit-line">{hit.text}</pre>
            </a>
          ))}
        </div>
      )
  }
}

// The FTS snippet is server-generated text with <mark> boundaries; escape
// everything else so a note cannot inject markup through its own body.
function escapeExceptMark(s: string): string {
  const esc = s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  return esc.replace(/&lt;mark&gt;/g, '<mark>').replace(/&lt;\/mark&gt;/g, '</mark>')
}
