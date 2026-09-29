// New from template: the templates in a space's templates/ folder, a
// small form for the {{prompt:...}} variables a template asks, and the
// title its own name suggests. The server does the substitution
// (POST /api/templates/{id}/expand); this collects the answers and hands
// the finished body to whoever opened it.

import { useEffect, useMemo, useRef, useState } from 'preact/hooks'

import type { TemplateExpanded } from './api'
import { api, ApiError, spaceOf, stem } from './api'
import { fuzzy } from './fuzzy'
import { Icon } from './icons'
import type { FlatNote } from './tree'

/** A note is a template when it sits somewhere under a templates/ folder. */
export function isTemplateNote(path: string): boolean {
  return path.slice(0, path.lastIndexOf('/') + 1).split('/').includes('templates')
}

/** The markdown templates of one space, in path order. */
/** The space a folder sits in: its first segment, or the folder itself
 * when it is a space root (a folder path, unlike a note path, may be a
 * whole space with no slash in it). */
export function spaceOfDir(dir: string): string {
  const i = dir.indexOf('/')
  return i < 0 ? dir : dir.slice(0, i)
}

export function templatesIn(notes: FlatNote[], space: string): FlatNote[] {
  return notes.filter((n) => !n.conflict && n.kind !== 'html' && spaceOf(n.path) === space && isTemplateNote(n.path))
}

export interface FromTemplateProps {
  /** The space's templates to offer. */
  templates: FlatNote[]
  /** The folder the note lands in (a full path) — what {{folder}} says. */
  folder: string
  /** A new note is made: the name field shows and the result is created.
   * False inserts into an open note, at its caret. */
  make: boolean
  /** The finished work: the note's name, the body, and where the caret
   * lands (runes into the body; -1 without a {{cursor}}). */
  onApply: (name: string, body: string, cursor: number) => void
  onToast: (msg: string) => void
  onClose: () => void
}

export function FromTemplate({ templates, folder, make, onApply, onToast, onClose }: FromTemplateProps) {
  const [chosen, setChosen] = useState<FlatNote | null>(null)
  const [first, setFirst] = useState<TemplateExpanded | null>(null)
  const [name, setName] = useState('')
  const [answers, setAnswers] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [query, setQuery] = useState('')
  const [cursor, setCursor] = useState(0)
  const form = useRef<HTMLDivElement>(null)
  const listInput = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLUListElement>(null)

  const rows = useMemo(() => {
    const q = query.trim()
    if (q === '') return templates
    return templates
      .map((t) => ({ t, m: fuzzy(q, stem(t.name)) }))
      .filter((x) => x.m)
      .sort((a, b) => (b.m?.score ?? 0) - (a.m?.score ?? 0))
      .map((x) => x.t)
  }, [templates, query])

  useEffect(() => setCursor(0), [query])

  useEffect(() => {
    list.current?.querySelector<HTMLElement>(`[data-i="${cursor}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  // Picking a template learns its prompts and the name its own suggests;
  // the second call, with the answers, makes the body.
  function pick(t: FlatNote): void {
    setBusy(true)
    api
      .expandTemplate(t.id, { folder })
      .then((res) => {
        setChosen(t)
        setFirst(res)
        setName(res.suggest || stem(t.name))
        setAnswers({})
      })
      .catch((err: unknown) => {
        onToast(err instanceof ApiError ? err.message : 'Could not read the template.')
        onClose()
      })
      .finally(() => setBusy(false))
  }

  function submit(): void {
    if (!chosen || busy) return
    if (make && name.trim() === '') return
    setBusy(true)
    api
      .expandTemplate(chosen.id, { title: name.trim(), folder, answers })
      .then((res) => {
        onClose()
        onApply(name.trim(), res.body, res.cursor)
      })
      .catch((err: unknown) => {
        onToast(err instanceof ApiError ? err.message : 'Could not expand the template.')
        setBusy(false)
      })
  }

  useEffect(() => {
    if (first) return
    // The list takes the keyboard: filter, arrows, Escape.
    const el = listInput.current
    el?.focus()
  }, [first])

  useEffect(() => {
    if (!first) return
    const el = form.current?.querySelector<HTMLInputElement>('input')
    el?.focus()
    el?.select()
  }, [first])

  function onKey(ev: KeyboardEvent): void {
    if (ev.key === 'Escape') {
      ev.preventDefault()
      if (chosen && first) {
        setChosen(null)
        setFirst(null)
        return
      }
      onClose()
    } else if (ev.key === 'Enter') {
      ev.preventDefault()
      if (chosen && first) submit()
      else if (rows[cursor]) pick(rows[cursor])
    } else if (ev.key === 'ArrowDown' && !chosen) {
      ev.preventDefault()
      if (rows.length) setCursor((c) => (c + 1) % rows.length)
    } else if (ev.key === 'ArrowUp' && !chosen) {
      ev.preventDefault()
      if (rows.length) setCursor((c) => (c - 1 + rows.length) % rows.length)
    }
  }

  if (chosen && first) {
    return (
      <div class="overlay" onMouseDown={(ev) => { if (ev.target === ev.currentTarget) onClose() }}>
        <div class="palette tpl" role="dialog" aria-label="New from template">
          <div class="tpl-head">
            <Icon name="copy" size={15} />
            <span class="tpl-head-name">{stem(chosen.name)}</span>
            <span class="tpl-head-folder">{folder || spaceOf(chosen.path) + '/'}</span>
          </div>
          <div class="tpl-form" ref={form} onKeyDown={onKey}>
            {make && (
              <label class="tpl-field">
                <span>Name</span>
                <input class="input" type="text" value={name} autocomplete="off" onInput={(ev) => setName((ev.target as HTMLInputElement).value)} />
              </label>
            )}
            {first.prompts.map((p) => (
              <label class="tpl-field" key={p}>
                <span>{p}</span>
                <input
                  class="input"
                  type="text"
                  value={answers[p] ?? ''}
                  autocomplete="off"
                  onInput={(ev) => setAnswers((a) => ({ ...a, [p]: (ev.target as HTMLInputElement).value }))}
                />
              </label>
            ))}
          </div>
          <div class="tpl-foot">
            <span class="tpl-says">{make ? `Creates ${folder ? folder + '/' : ''}${name.trim() || '…'}` : 'Inserts at the caret'}</span>
            <button type="button" class="btn primary" disabled={busy || (make && name.trim() === '')} onClick={submit}>
              {make ? 'Create' : 'Insert'}
            </button>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div class="overlay" onMouseDown={(ev) => { if (ev.target === ev.currentTarget) onClose() }}>
      <div class="palette tpl" role="dialog" aria-label="New from template">
        <input
          ref={listInput}
          class="palette-input"
          type="text"
          value={query}
          placeholder="New from template"
          autocomplete="off"
          spellcheck={false}
          onInput={(ev) => setQuery((ev.target as HTMLInputElement).value)}
          onKeyDown={onKey}
        />
        <ul class="palette-list" ref={list} role="listbox">
          {rows.length === 0 && <li class="palette-empty">No templates match.</li>}
          {rows.map((t, i) => (
            <li
              key={t.id}
              data-i={i}
              class={'palette-row' + (i === cursor ? ' active' : '')}
              role="option"
              aria-selected={i === cursor}
              onMouseEnter={() => setCursor(i)}
              onMouseDown={(ev) => {
                ev.preventDefault()
                pick(t)
              }}
            >
              <Icon name="copy" size={15} class="palette-folder" />
              <span class="palette-label">{stem(t.name)}</span>
              <span class="palette-detail">{t.path}</span>
            </li>
          ))}
        </ul>
        <p class="palette-hint">
          {templates.length} template{templates.length === 1 ? '' : 's'} in templates/
        </p>
      </div>
    </div>
  )
}
