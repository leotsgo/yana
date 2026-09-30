// One overlay for the command palette, the quick switcher, and the short
// prompts (new note, rename). A list mode filters items by fuzzy match; a
// prompt mode takes one line of text. Arrow keys move, Enter picks,
// Escape closes. The folder picker is a list in path mode: the input
// starts as the current path and can be edited by hand, the rows are the
// tree under what is typed, indented, and Enter on a path that is not
// there makes it.

import { useEffect, useMemo, useRef, useState } from 'preact/hooks'

import { fuzzy } from './fuzzy'
import { Icon } from './icons'
import { applyCompletion, completionsFor } from './opsearch'
import type { CompletionSource } from './opsearch'
import { Completions, QueryMirror } from './opsinput'

export interface PaletteItem {
  id: string
  label: string
  detail?: string
  hint?: string
  /** Path mode: the full path the row stands for, matched against the query. */
  path?: string
  /** Path mode: how deep the row sits; the row indents to match. */
  depth?: number
  /** Path mode: the row is where the thing already is; picking it does nothing. */
  here?: boolean
  /** A heading shown above the row when it starts a group. */
  section?: string
  run: () => void
}

/** How an operator-aware list matches its rows: the query is parsed, the
 * text part fuzzy-matches as always, and the operator part decides which
 * rows survive at all. Returns null when the query has no operators. */
export type OperatorFilter = (query: string) => { text: string; keep: (item: PaletteItem) => boolean } | null

export interface ListPalette {
  mode: 'list'
  placeholder: string
  items: PaletteItem[]
  /** Offered as the last row when the query matches nothing exactly. */
  onCreate?: (query: string) => void
  /** What the create row makes; "new note" unless said otherwise. */
  createHint?: string
  /** Path mode: the label for the create row, given the tidied query. */
  createLabel?: (query: string) => string
  /** How many rows to show at most. */
  limit?: number
  /** Text in the input to begin with, caret at the end: a path to edit. */
  initial?: string
  /** How rows match the query: fuzzy on the label, or by path (see above). */
  match?: 'fuzzy' | 'path'
  /** A line under the list. */
  hint?: string
  /** Operator mode: highlight operators as chips, offer completions, and
   * match rows through the grammar instead of plain fuzzy. */
  operators?: CompletionSource
  operatorFilter?: OperatorFilter
}

export interface PromptPalette {
  mode: 'prompt'
  placeholder: string
  initial: string
  hint: string
  /** Where the caret starts; selects the range when two numbers. */
  select?: [number, number]
  onSubmit: (value: string) => void
}

export type PaletteSpec = ListPalette | PromptPalette

export function Palette({ spec, onClose }: { spec: PaletteSpec; onClose: () => void }) {
  const [query, setQuery] = useState(spec.mode === 'prompt' ? spec.initial : spec.initial ?? '')
  // Path mode shows the whole tree until the path is edited: a chooser
  // first, a filter once something is typed.
  const [touched, setTouched] = useState(false)
  const [cursor, setCursor] = useState(0)
  // Operator mode: the completion list under the input.
  const [cc, setCc] = useState(-1)
  const [ccOff, setCcOff] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLUListElement>(null)

  const opMode = spec.mode === 'list' && spec.operators !== undefined && spec.operatorFilter !== undefined
  const completions = useMemo(
    () => (opMode && spec.mode === 'list' && spec.operators ? completionsFor(query, spec.operators) : null),
    [opMode, spec, query],
  )
  const ccValues = completions?.values ?? []
  const ccOpen = opMode && !ccOff && ccValues.length > 0

  useEffect(() => {
    const el = input.current
    if (!el) return
    // One palette opening another (Help from the command list) lands in
    // the same render, so the box is reset here rather than by a remount.
    const initial = spec.mode === 'prompt' ? spec.initial : spec.initial ?? ''
    setQuery(initial)
    setTouched(false)
    setCursor(0)
    setCc(-1)
    setCcOff(false)
    el.value = initial
    el.focus()
    if (spec.mode === 'prompt') {
      const [a, b] = spec.select ?? [el.value.length, el.value.length]
      el.setSelectionRange(a, b)
    } else if (spec.initial) {
      el.setSelectionRange(el.value.length, el.value.length)
    }
  }, [spec])

  useEffect(() => {
    setCc(-1)
    setCcOff(false)
  }, [query])

  const pathMode = spec.mode === 'list' && spec.match === 'path'

  const rows = useMemo<PaletteItem[]>(() => {
    if (spec.mode === 'prompt') return []
    const limit = spec.limit ?? 40
    if (spec.match === 'path') {
      // The tree under what is typed, with the parents of every match so
      // the indentation still reads. Nothing typed shows everything.
      const q = cleanPath(query)
      const lq = q.toLowerCase()
      let out = spec.items
      if (q !== '' && touched) {
        const keep = new Set<string>()
        for (const item of spec.items) {
          const p = (item.path ?? '').toLowerCase()
          if (!p.includes(lq)) continue
          keep.add(item.path ?? '')
          const parts = (item.path ?? '').split('/')
          for (let i = 1; i < parts.length; i++) keep.add(parts.slice(0, i).join('/'))
        }
        out = spec.items.filter((it) => keep.has(it.path ?? ''))
      }
      out = out.slice(0, limit)
      const exists = spec.items.some((it) => (it.path ?? '').toLowerCase() === lq)
      if (spec.onCreate && q !== '' && !exists) {
        const create = spec.onCreate
        out.push({ id: '\0create', label: spec.createLabel ? spec.createLabel(q) : `Make ${q}/`, hint: spec.createHint ?? 'new folder', run: () => create(q) })
      }
      return out
    }
    const q = query.trim()
    // Operator mode: the grammar splits the query into a text part and a
    // row filter; rows survive the filter, the text fuzzy-matches as ever.
    let pool = spec.items
    let text = q
    if (opMode && spec.operatorFilter && q !== '') {
      const parsed = spec.operatorFilter(query)
      if (parsed) {
        pool = pool.filter(parsed.keep)
        text = parsed.text.trim()
      }
    }
    let out: PaletteItem[]
    if (text === '') {
      out = pool.slice(0, limit)
    } else {
      const scored: Array<{ item: PaletteItem; score: number }> = []
      for (const item of pool) {
        const a = fuzzy(text, item.label)
        const b = item.detail ? fuzzy(text, item.detail) : null
        const score = Math.max(a?.score ?? -Infinity, b?.score ?? -Infinity)
        if (score !== -Infinity) scored.push({ item, score })
      }
      scored.sort((x, y) => y.score - x.score)
      out = scored.slice(0, limit).map((s) => s.item)
    }
    if (spec.onCreate && text !== '' && !out.some((r) => r.label.toLowerCase() === text.toLowerCase())) {
      const create = spec.onCreate
      out.push({ id: '\0create', label: `Create "${text}"`, hint: spec.createHint ?? 'new note', run: () => create(text) })
    }
    return out
  }, [spec, query, touched, opMode])

  // The cursor starts on the row the query names: the exact path, else
  // the first under it, else the create row at the end.
  useEffect(() => {
    if (!pathMode) {
      setCursor(0)
      return
    }
    const q = cleanPath(query).toLowerCase()
    const exact = rows.findIndex((r) => (r.path ?? '').toLowerCase() === q)
    if (exact >= 0) {
      setCursor(exact)
      return
    }
    const under = rows.findIndex((r) => r.path !== undefined && r.path.toLowerCase().startsWith(q))
    setCursor(under >= 0 ? under : rows.length ? rows.length - 1 : 0)
  }, [query, rows, pathMode])

  useEffect(() => {
    const el = list.current?.querySelector<HTMLElement>(`[data-i="${cursor}"]`)
    el?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  function pick(i: number): void {
    const row = rows[i]
    if (!row) return
    onClose()
    if (!row.here) row.run()
  }

  function acceptCompletion(v: string): void {
    if (query.trim() === '') setQuery(v + ' ')
    else setQuery(applyCompletion(query, v))
    setCc(-1)
  }

  function onKey(ev: KeyboardEvent): void {
    if (ccOpen && (ev.key === 'ArrowDown' || ev.key === 'ArrowUp')) {
      ev.preventDefault()
      const n = ccValues.length
      setCc((c) => (ev.key === 'ArrowDown' ? (c + 1) % n : (c - 1 + n) % n))
      return
    }
    if (ccOpen && ev.key === 'Tab') {
      ev.preventDefault()
      acceptCompletion(ccValues[cc >= 0 ? cc : 0] ?? '')
      return
    }
    switch (ev.key) {
      case 'Escape':
        ev.preventDefault()
        if (ccOpen) {
          setCcOff(true)
          return
        }
        onClose()
        break
      case 'ArrowDown':
        ev.preventDefault()
        if (rows.length) setCursor((c) => (c + 1) % rows.length)
        break
      case 'ArrowUp':
        ev.preventDefault()
        if (rows.length) setCursor((c) => (c - 1 + rows.length) % rows.length)
        break
      case 'Enter':
        ev.preventDefault()
        if (ccOpen && cc >= 0) {
          acceptCompletion(ccValues[cc] ?? '')
          return
        }
        if (spec.mode === 'prompt') {
          const v = query.trim()
          if (v === '') return
          onClose()
          spec.onSubmit(v)
        } else {
          pick(cursor)
        }
        break
    }
  }

  const inputEl = (
    <input
      ref={input}
      class="palette-input"
      type="text"
      value={query}
      placeholder={spec.placeholder}
      autocomplete="off"
      spellcheck={false}
      onInput={(ev) => {
        setQuery((ev.target as HTMLInputElement).value)
        setTouched(true)
      }}
      onKeyDown={onKey}
    />
  )

  return (
    <div class="overlay" onMouseDown={(ev) => { if (ev.target === ev.currentTarget) onClose() }}>
      <div class="palette" role="dialog" aria-label={spec.placeholder}>
        {opMode && spec.mode === 'list' && spec.operators ? (
          <div class="search-field palette-field">
            <div class="search-mirror palette-mirror" aria-hidden="true">
              <QueryMirror query={query} />
            </div>
            {inputEl}
            {!ccOff && (
              <Completions query={query} source={spec.operators} cursor={cc} onCursor={setCc} onPick={acceptCompletion} emptyList />
            )}
          </div>
        ) : (
          inputEl
        )}
        {spec.mode === 'prompt' ? (
          <p class="palette-hint">{spec.hint}</p>
        ) : (
          <ul class="palette-list" ref={list} role="listbox">
            {rows.length === 0 && <li class="palette-empty">Nothing matches.</li>}
            {rows.map((row, i) => [
              row.section && row.section !== rows[i - 1]?.section && (
                <li key={'head:' + row.section} class="palette-head" role="presentation">
                  {row.section}
                </li>
              ),
              <li
                key={row.id}
                data-i={i}
                class={'palette-row' + (i === cursor ? ' active' : '') + (row.here ? ' here' : '')}
                role="option"
                aria-selected={i === cursor}
                style={row.depth !== undefined ? `--depth:${row.depth}` : undefined}
                onMouseEnter={() => setCursor(i)}
                onMouseDown={(ev) => { ev.preventDefault(); pick(i) }}
              >
                {pathMode && <Icon name={row.path === undefined ? 'folder-plus' : 'folder'} size={15} class="palette-folder" />}
                <span class="palette-label">{row.label}</span>
                {row.detail && <span class="palette-detail">{row.detail}</span>}
                {row.here ? <span class="palette-here">here</span> : row.hint && <kbd class="palette-key">{row.hint}</kbd>}
              </li>,
            ])}
          </ul>
        )}
        {spec.mode === 'list' && spec.hint && <p class="palette-hint">{spec.hint}</p>}
      </div>
    </div>
  )
}

/** A typed path, tidied: no surrounding slashes or spaces, one slash
 * between segments. */
function cleanPath(q: string): string {
  return q
    .trim()
    .replace(/\/+/g, '/')
    .replace(/^\/|\/$/g, '')
}
