// Search results: full text through the index, or regex through
// ripgrep. On a desktop the query lives in the sidebar and this renders
// what it finds; on a phone the whole screen is the search page, with
// the queries typed before it under an empty box. Opening a result
// carries the matched text along so the note scrolls to it.

import { useEffect, useRef, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { AttachmentHit, RegexHit, SearchHit, Status } from './api'
import { Icon } from './icons'
import { OPERATORS } from './opsearch'
import type { CompletionSource } from './opsearch'
import { OperatorInput } from './opsinput'
import * as prefs from './prefs'
import { openProps } from './workspace'
import type { OpenHow } from './workspace'

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
  onOpen: (id: string, highlight: string | null, how?: OpenHow) => void
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
              {...openProps((how) => { prefs.touchQuery(query); onOpen(hit.note.id, markedText(hit.snippet) ?? query, how) })}
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
                        {...openProps((how) => { prefs.touchQuery(query); onOpen(ref.id, att.name, how) })}
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
              {...openProps((how) => {
                if (!hit.id) return
                prefs.touchQuery(query)
                onOpen(hit.id, regexMatch(query, hit.text), how)
              })}
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
  /** Tags, folders and spaces for the operator completions. */
  source: CompletionSource
  onOpen: (id: string, highlight: string | null) => void
  onClose: () => void
  /** Save the query under a name (the name prompt lives in the shell). */
  onSave: (query: string) => void
}

// The phone's search: one screen, the box at the top with the keyboard
// up as soon as it opens, the operator list and recent queries under it
// until something is typed, results after that.
export function SearchPage({ status, source, onOpen, onClose, onSave }: SearchPageProps) {
  const [query, setQuery] = useState('')
  const [regex, setRegex] = useState(false)
  const [recent, setRecent] = useState(prefs.recentQueries)
  const input = useRef<HTMLInputElement>(null)

  useEffect(() => {
    document.title = 'Search — YANA/'
    input.current?.focus()
  }, [])

  useEffect(() => prefs.onChange(() => setRecent(prefs.recentQueries())), [])

  const saved = prefs.isSearchSaved(query)

  return (
    <div class="search-page">
      <div class="search-page-bar">
        <button type="button" class="icon-btn" aria-label="Back" onClick={onClose}>
          <Icon name="arrow-left" size={18} />
        </button>
        <div class="sidebar-search">
          <Icon name="search" class="sidebar-search-icon" />
          <OperatorInput
            query={query}
            onQuery={setQuery}
            source={source}
            placeholder="Search notes"
            ariaLabel="Search notes"
            inputRef={input}
            onKey={(ev) => {
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
            title={status && !status.regex_search ? 'Regex search needs ripgrep on the server.' : 'Match a regular expression against the files; path: and space: narrow it'}
            onClick={() => setRegex((r) => !r)}
          >
            .*
          </button>
        </div>
      </div>
      <div class="search-page-body">
        {query.trim() === '' ? (
          <>
            <div class="op-empty" aria-label="search operators">
              <h2 class="section-title">Operators</h2>
              <p class="op-empty-lead">Search looks at titles and bodies. Put one of these in front of a word to narrow it:</p>
              <ul class="op-empty-list">
                {OPERATORS.map((o) => (
                  <li key={o.op}>
                    <code>{o.example}</code>
                    <span>{o.hint}</span>
                  </li>
                ))}
                <li>
                  <code>-tag:done</code>
                  <span>a minus in front excludes</span>
                </li>
                <li>
                  <code>"exact phrase"</code>
                  <span>quotes make a phrase</span>
                </li>
              </ul>
            </div>
            {recent.length > 0 && (
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
            )}
          </>
        ) : (
          <>
            {!regex && (
              <button type="button" class="search-save" onClick={() => onSave(query)} disabled={saved}>
                <Icon name={saved ? 'pin' : 'plus'} size={14} />
                {saved ? 'Saved' : 'Save this search'}
              </button>
            )}
            <SearchResults query={query} regex={regex} onOpen={onOpen} />
          </>
        )}
      </div>
    </div>
  )
}
