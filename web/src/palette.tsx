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
  run: () => void
}

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
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLUListElement>(null)

  useEffect(() => {
    const el = input.current
    if (!el) return
    el.focus()
    if (spec.mode === 'prompt') {
      const [a, b] = spec.select ?? [el.value.length, el.value.length]
      el.setSelectionRange(a, b)
    } else if (spec.initial) {
      el.setSelectionRange(el.value.length, el.value.length)
    }
  }, [spec])

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
    let out: PaletteItem[]
    if (q === '') {
      out = spec.items.slice(0, limit)
    } else {
      const scored: Array<{ item: PaletteItem; score: number }> = []
      for (const item of spec.items) {
        const a = fuzzy(q, item.label)
        const b = item.detail ? fuzzy(q, item.detail) : null
        const score = Math.max(a?.score ?? -Infinity, b?.score ?? -Infinity)
        if (score !== -Infinity) scored.push({ item, score })
      }
      scored.sort((x, y) => y.score - x.score)
      out = scored.slice(0, limit).map((s) => s.item)
    }
    if (spec.onCreate && q !== '' && !out.some((r) => r.label.toLowerCase() === q.toLowerCase())) {
      const create = spec.onCreate
      out.push({ id: '\0create', label: `Create "${q}"`, hint: spec.createHint ?? 'new note', run: () => create(q) })
    }
    return out
  }, [spec, query, touched])

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
    const el = list.current?.children[cursor] as HTMLElement | undefined
    el?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  function pick(i: number): void {
    const row = rows[i]
    if (!row) return
    onClose()
    if (!row.here) row.run()
  }

  function onKey(ev: KeyboardEvent): void {
    switch (ev.key) {
      case 'Escape':
        ev.preventDefault()
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

  return (
    <div class="overlay" onMouseDown={(ev) => { if (ev.target === ev.currentTarget) onClose() }}>
      <div class="palette" role="dialog" aria-label={spec.placeholder}>
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
        {spec.mode === 'prompt' ? (
          <p class="palette-hint">{spec.hint}</p>
        ) : (
          <ul class="palette-list" ref={list} role="listbox">
            {rows.length === 0 && <li class="palette-empty">Nothing matches.</li>}
            {rows.map((row, i) => (
              <li
                key={row.id}
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
              </li>
            ))}
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
