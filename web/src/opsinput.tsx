// The visual half of the search grammar: the mirror that highlights
// recognised operators as chips over the input while typing, and the
// completion list offered after tag:, path:, space:, is: and has: — or
// the operator list itself when the box is empty. The mirror renders
// the exact text the input holds (chips add no width), with the input's
// own text made transparent, so the caret stays where it belongs.

import { useEffect, useRef, useState } from 'preact/hooks'

import { completionsFor, OPERATORS, applyCompletion, parseQuery } from './opsearch'
import type { CompletionSource } from './opsearch'

/** Renders the query with recognised operators as chips. Every glyph the
 * input holds is rendered at the same width, so the overlay can sit
 * exactly behind the transparent-text input. */
export function QueryMirror({ query }: { query: string }) {
  const parts: Array<import('preact').VNode> = []
  let at = 0
  for (const t of parseQuery(query)) {
    if (t.start > at) parts.push(<span class="op-sep">{query.slice(at, t.start)}</span>)
    if (t.op) {
      const body = t.negated ? t.raw.slice(1) : t.raw
      const mark = body.startsWith('#') ? '#' : t.op + ':'
      parts.push(
        <span key={t.start} class={'op-chip' + (t.negated ? ' neg' : '') + (t.value === '' ? ' open' : '')}>
          {t.negated ? '-' : ''}
          <span class="op-name">{mark}</span>
          {t.quoted ? '"' + t.value + '"' : t.value}
        </span>,
      )
    } else if (t.quoted) {
      parts.push(
        <span key={t.start} class="op-phrase">
          {t.raw}
        </span>,
      )
    } else {
      parts.push(<span key={t.start}>{t.raw}</span>)
    }
    at = t.end
  }
  if (at < query.length) parts.push(<span class="op-sep">{query.slice(at)}</span>)
  return <>{parts}</>
}

export interface CompletionsProps {
  query: string
  source: CompletionSource
  cursor: number
  onCursor: (i: number) => void
  onPick: (value: string) => void
  /** Show the operator list when the query is empty (the palette does;
   * a box over the tree does not). */
  emptyList?: boolean
}

/** The list under the box: values for the operator being typed, or the
 * operator list for an empty box. Renders nothing when there is nothing
 * to offer. */
export function Completions({ query, source, cursor, onCursor, onPick, emptyList }: CompletionsProps) {
  const list = query.trim() === '' && !emptyList ? null : completionsFor(query, source)
  if (!list || list.values.length === 0) return null
  const isOpList = query.trim() === ''
  return (
    <div class="op-completions" role="listbox" aria-label={isOpList ? 'search operators' : 'completions'}>
      <div class="op-completions-head">{isOpList ? 'Operators — put one in front of what you search for' : list.title}</div>
      {isOpList
        ? OPERATORS.map((o) => (
            <button
              key={o.op}
              type="button"
              class="op-completion"
              onMouseDown={(ev) => {
                ev.preventDefault()
                onPick(o.example)
              }}
            >
              <code class="op-completion-value">{o.example}</code>
              <span class="op-completion-hint">{o.hint}</span>
            </button>
          ))
        : list.values.map((v, i) => (
            <button
              key={v}
              type="button"
              class={'op-completion' + (i === cursor ? ' active' : '')}
              role="option"
              aria-selected={i === cursor}
              onMouseDown={(ev) => {
                ev.preventDefault()
                onPick(v)
              }}
              onMouseEnter={() => onCursor(i)}
            >
              <code class="op-completion-value">{v}</code>
            </button>
          ))}
    </div>
  )
}

export interface OperatorInputProps {
  query: string
  onQuery: (q: string) => void
  source: CompletionSource
  placeholder: string
  ariaLabel: string
  className?: string
  inputRef?: { current: HTMLInputElement | null }
  /** Keys the host still owns; completion keys are consumed first. */
  onKey?: (ev: KeyboardEvent) => void
  onFocus?: () => void
}

/** A search input with the chip mirror over it and the completion list
 * under it. Arrow keys move the list, Tab (or Enter on a highlighted
 * row) accepts, Escape closes it. */
export function OperatorInput({ query, onQuery, source, placeholder, ariaLabel, className, inputRef, onKey, onFocus }: OperatorInputProps) {
  const [cursor, setCursor] = useState(-1)
  const [focused, setFocused] = useState(false)
  const [dismissed, setDismissed] = useState(false)
  const mirror = useRef<HTMLDivElement>(null)
  const ownRef = useRef<HTMLInputElement>(null)
  const ref = inputRef ?? ownRef

  // The empty box offers no dropdown: on the sidebar it would sit over
  // the tree, and the search page carries the operator list in its own
  // empty state. The palette (an overlay) does show it.
  const list = query.trim() === '' ? null : completionsFor(query, source)
  const values = list?.values ?? []
  useEffect(() => {
    setCursor(-1)
    setDismissed(false)
  }, [query])

  function pick(v: string): void {
    if (query.trim() === '') {
      onQuery(v + ' ')
    } else {
      onQuery(applyCompletion(query, v))
    }
    ref.current?.focus()
  }

  function key(ev: KeyboardEvent): void {
    const open = focused && !dismissed && values.length > 0
    if (open && (ev.key === 'ArrowDown' || ev.key === 'ArrowUp')) {
      ev.preventDefault()
      ev.stopPropagation()
      setCursor((c) => {
        const n = values.length
        return ev.key === 'ArrowDown' ? (c + 1) % n : (c - 1 + n) % n
      })
      return
    }
    if (open && ev.key === 'Tab') {
      ev.preventDefault()
      ev.stopPropagation()
      pick(values[cursor >= 0 ? cursor : 0] ?? '')
      return
    }
    if (open && cursor >= 0 && ev.key === 'Enter') {
      ev.preventDefault()
      ev.stopPropagation()
      pick(values[cursor] ?? '')
      return
    }
    if (open && ev.key === 'Escape') {
      setDismissed(true)
      ev.stopPropagation()
      return
    }
    onKey?.(ev)
  }

  return (
    <div class={'search-field ' + (className ?? '')}>
      <div class="search-mirror" ref={mirror} aria-hidden="true">
        <QueryMirror query={query} />
      </div>
      <input
        ref={ref}
        type="search"
        class="search-input"
        placeholder={placeholder}
        autocomplete="off"
        spellcheck={false}
        aria-label={ariaLabel}
        value={query}
        onInput={(ev) => onQuery((ev.target as HTMLInputElement).value)}
        onFocus={() => {
          setFocused(true)
          setDismissed(false)
          onFocus?.()
        }}
        onBlur={() => setFocused(false)}
        onScroll={() => {
          if (mirror.current && ref.current) mirror.current.scrollLeft = ref.current.scrollLeft
        }}
        onKeyDown={key}
      />
      {focused && !dismissed && <Completions query={query} source={source} cursor={cursor} onCursor={setCursor} onPick={pick} />}
    </div>
  )
}
