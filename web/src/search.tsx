// Search results: full text through the index, or regex through
// ripgrep. On a desktop the query lives in the sidebar and this renders
// what it finds; on a phone the whole screen is the search page, with
// the queries typed before it under an empty box. Opening a result
// carries the matched text along so the note scrolls to it.

import { useEffect, useRef, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { AttachmentHit, RegexHit, SearchHit, Status } from './api'
import { Icon } from './icons'
import * as prefs from './prefs'

type Result =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'fts'; hits: SearchHit[]; attachments: AttachmentHit[] }
  | { kind: 'regex'; hits: RegexHit[] }
  | { kind: 'error'; message: string }

export interface SearchResultsProps {
  query: string
  regex: boolean
  /** Open a hit; the second argument is the matched text, for scrolling to it. */
  onOpen: (id: string, highlight: string | null) => void
}

export function SearchResults({ query, regex, onOpen }: SearchResultsProps) {
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
        : api.search(q).then((r) => ({ kind: 'fts', hits: r.hits, attachments: r.attachments }) as Result)
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
      return <p class="empty muted">Searching…</p>
    case 'error':
      return <p class="empty error">{result.message}</p>
    case 'fts':
      if (result.hits.length === 0 && result.attachments.length === 0) return <p class="empty muted">No notes match. Search looks at titles and bodies; the .* switch matches a regular expression against the files instead.</p>
      return (
        <div class="results">
          {result.hits.map((hit) => (
            <a
              key={hit.note.id}
              class="hit"
              href={`/n/${hit.note.id}`}
              onClick={(ev) => { ev.preventDefault(); prefs.touchQuery(query); onOpen(hit.note.id, markedText(hit.snippet) ?? query) }}
            >
              <div class="hit-title">{hit.note.title}</div>
              <div class="hit-path">{hit.note.path}</div>
              <div class="hit-snippet" dangerouslySetInnerHTML={{ __html: escapeExceptMark(hit.snippet) }} />
            </a>
          ))}
          {result.attachments.map((att) => (
            <div key={att.path} class="hit hit-attachment">
              <div class="hit-title">
                <Icon name="file-text" size={14} class="hit-attachment-icon" />
                {att.name}
                {att.pages !== undefined && <span class="hit-attachment-pages">{att.pages} {att.pages === 1 ? 'page' : 'pages'}</span>}
              </div>
              <div class="hit-path">{att.path}</div>
              {att.snippet && <div class="hit-snippet" dangerouslySetInnerHTML={{ __html: escapeExceptMark(att.snippet) }} />}
              {att.refs.length > 0 && (
                <div class="hit-attachment-refs">
                  In{' '}
                  {att.refs.map((ref, i) => (
                    <span key={ref.id}>
                      {i > 0 && ', '}
                      <a
                        href={`/n/${ref.id}`}
                        onClick={(ev) => { ev.preventDefault(); prefs.touchQuery(query); onOpen(ref.id, att.name) }}
                      >
                        {ref.title || ref.path}
                      </a>
                    </span>
                  ))}
                </div>
              )}
            </div>
          ))}
        </div>
      )
    case 'regex':
      if (result.hits.length === 0) return <p class="empty muted">No lines match that expression.</p>
      return (
        <div class="results">
          {result.hits.map((hit, i) => (
            <a
              key={`${hit.path}:${hit.line}:${i}`}
              class={'hit hit-regex' + (hit.id ? '' : ' unindexed')}
              href={hit.id ? `/n/${hit.id}` : '#'}
              onClick={(ev) => {
                ev.preventDefault()
                if (!hit.id) return
                prefs.touchQuery(query)
                onOpen(hit.id, regexMatch(query, hit.text))
              }}
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

/** The first marked run in a snippet: the text the note scrolls to. */
function markedText(snippet: string): string | null {
  const m = /<mark>([\s\S]*?)<\/mark>/.exec(snippet)
  return m && m[1] ? m[1] : null
}

/** What the expression matched on the line, as literal text. */
function regexMatch(raw: string, line: string): string | null {
  try {
    const m = new RegExp(raw, 'i').exec(line)
    return m && m[0] ? m[0] : null
  } catch {
    return null
  }
}

export interface SearchPageProps {
  status: Status | null
  onOpen: (id: string, highlight: string | null) => void
  onClose: () => void
}

// The phone's search: one screen, the box at the top with the keyboard
// up as soon as it opens, recent queries under it until something is
// typed, results after that.
export function SearchPage({ status, onOpen, onClose }: SearchPageProps) {
  const [query, setQuery] = useState('')
  const [regex, setRegex] = useState(false)
  const [recent, setRecent] = useState(prefs.recentQueries)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    document.title = 'Search — YANA/'
    input.current?.focus()
  }, [])

  useEffect(() => prefs.onChange(() => setRecent(prefs.recentQueries())), [])

  return (
    <div class="search-page">
      <div class="search-page-bar">
        <button type="button" class="icon-btn" aria-label="Back" onClick={onClose}>
          <Icon name="arrow-left" size={18} />
        </button>
        <div class="sidebar-search">
          <Icon name="search" class="sidebar-search-icon" />
          <input
            ref={input}
            type="search"
            class="search-input"
            placeholder="Search notes"
            autocomplete="off"
            spellcheck={false}
            enterkeyhint="search"
            aria-label="Search notes"
            value={query}
            onInput={(ev) => setQuery((ev.target as HTMLInputElement).value)}
            onKeyDown={(ev) => {
              if (ev.key === 'Enter') {
                prefs.touchQuery(query)
                ;(ev.target as HTMLInputElement).blur()
              } else if (ev.key === 'Escape') {
                if (query) setQuery('')
                else onClose()
              }
            }}
          />
          <button
            type="button"
            class={'regex-btn' + (regex ? ' on' : '')}
            disabled={status ? !status.regex_search : false}
            aria-pressed={regex}
            title={status && !status.regex_search ? 'Regex search needs ripgrep on the server.' : 'Match a regular expression against the files'}
            onClick={() => setRegex((r) => !r)}
          >
            .*
          </button>
        </div>
      </div>
      <div class="search-page-body">
        {query.trim() === '' ? (
          recent.length === 0 ? (
            <p class="empty muted">Search looks at titles and bodies. What you search for shows up here for next time.</p>
          ) : (
            <div class="recent-queries" aria-label="recent searches">
              <h2 class="section-title">Recent</h2>
              {recent.map((q) => (
                <div key={q} class="recent-query">
                  <button type="button" class="recent-query-run" onClick={() => setQuery(q)}>
                    <Icon name="clock" size={15} />
                    <span>{q}</span>
                  </button>
                  <button type="button" class="icon-btn" aria-label={`Forget ${q}`} onClick={() => prefs.forgetQuery(q)}>
                    <Icon name="x" size={15} />
                  </button>
                </div>
              ))}
            </div>
          )
        ) : (
          <SearchResults query={query} regex={regex} onOpen={onOpen} />
        )}
      </div>
    </div>
  )
}
