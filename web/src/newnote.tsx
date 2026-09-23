// The new-note picker: the place and the name are chosen before the note
// is made. The input holds a path; everything up to the last slash is the
// folder, the rest is the name. Enter makes `<folder>/<name>.md` (a
// trailing slash makes an untitled note there), Tab completes the
// highlighted folder into the input, Shift+Tab goes up a level. Folders
// match fuzzily per segment, so `pe/da` then Tab lands on personal/Daily/.
// A path that names a note that exists offers to open it instead. The
// folders used lately on this device sit at the top until something is
// typed, then match along with the rest.

import { useEffect, useMemo, useRef, useState } from 'preact/hooks'

import { baseOf, dirOf } from './api'
import { fuzzy } from './fuzzy'
import { isMac } from './hotkeys'
import { Icon } from './icons'
import { fileName, resolveDir } from './paths'

export interface NewNoteSpec {
  /** The folder to start in, with a trailing slash; empty for the root. */
  initial: string
  /** The space a first segment that is not a space resolves inside. */
  space: string
}

/** Where the made note opens: a tab in the focused pane, the other pane,
 * or a tab behind the current one. */
export type CreateHow = 'tab' | 'right' | 'background'

export interface PickerNote {
  id: string
  path: string
  title: string
}

interface Props {
  spec: NewNoteSpec
  dirs: Array<{ path: string; depth: number }>
  spaces: string[]
  notes: PickerNote[]
  /** Folders used lately on this device, newest first. */
  recent: string[]
  /** A desktop: Alt+Enter has another pane to open in. */
  panes: boolean
  /** A touch screen: rows carry a button that completes into the folder. */
  touch: boolean
  /** Make a note. A null name is an untitled note with its title selected. */
  onCreate: (dir: string, name: string | null, how: CreateHow, force: boolean) => void
  onOpen: (id: string, how: CreateHow) => void
  onClose: () => void
}

type Row =
  | { kind: 'open'; id: string; label: string; detail: string }
  | { kind: 'note'; id: string; label: string; detail: string }
  | { kind: 'folder'; path: string; label: string; recent?: boolean }
  | { kind: 'up'; path: string; label: string }

/** What the typed path means: the folder the note lands in, the name, the
 * folders that will be made, and the folder whose children are listed. */
export interface Parsed {
  dir: string
  name: string
  missing: string[]
  error: string | null
  /** The folder the list shows, found fuzzily; null when nothing fits. */
  browse: string | null
}

const NOTE_EXT = /\.(md|markdown|html?)$/i

export function parsePath(text: string, space: string, spaces: string[], dirs: Array<{ path: string }>): Parsed {
  const cut = text.lastIndexOf('/')
  const folderPart = cut >= 0 ? text.slice(0, cut + 1) : ''
  const name = (cut >= 0 ? text.slice(cut + 1) : text).replace(/\s+/g, ' ').trim()
  const byLower = new Map(dirs.map((d) => [d.path.toLowerCase(), d.path]))
  const spaceByLower = new Map(spaces.map((s) => [s.toLowerCase(), s]))

  // Typed from the root when it starts with a slash or with a space's
  // name; otherwise inside the current space, as the Move picker does.
  const trimmed = folderPart.trim()
  const first = trimmed.replace(/^\/+/, '').split('/')[0] ?? ''
  const abs = trimmed.startsWith('/') || spaceByLower.has(first.toLowerCase()) || !space
  const raw = resolveDir(abs ? '' : space, abs ? trimmed.replace(/^\/+/, '') : trimmed)
  // The folders that are there keep their spelling: `Personal/daily`
  // lands in personal/Daily rather than making a second one.
  let dir = ''
  for (const seg of raw ? raw.split('/') : []) {
    const next = dir ? `${dir}/${seg}` : seg
    dir = byLower.get(next.toLowerCase()) ?? next
  }
  const missing: string[] = []
  const parts = dir ? dir.split('/') : []
  for (let i = 2; i <= parts.length; i++) {
    const p = parts.slice(0, i).join('/')
    if (!byLower.has(p.toLowerCase())) missing.push(p)
  }
  let error: string | null = null
  const top = parts[0] ?? ''
  if (!dir) error = 'A note lives inside a space. Start the path with one.'
  else if (!spaceByLower.has(top.toLowerCase())) error = `${top} is not a space. A note lives inside one.`

  return { dir, name, missing, error, browse: browseDir(folderPart, space, spaces, dirs, dir) }
}

/** The folder to list under what is typed: the typed folder when it is
 * there, else each segment matched fuzzily against the folders one level
 * down, from the root and then from the current space. */
function browseDir(folderPart: string, space: string, spaces: string[], dirs: Array<{ path: string }>, literal: string): string | null {
  const t = folderPart.trim()
  if (t === '' || t === '/') return ''
  if (dirs.some((d) => d.path === literal)) return literal
  const segs = t.split('/').map((s) => s.trim()).filter((s) => s !== '' && s !== '.')
  if (segs.includes('..')) return null
  const walk = (from: string): string | null => {
    let cur = from
    for (const seg of segs) {
      const kids = children(cur, spaces, dirs)
      const exact = kids.find((k) => baseOf(k).toLowerCase() === seg.toLowerCase())
      if (exact) {
        cur = exact
        continue
      }
      let best: string | null = null
      let score = -Infinity
      for (const k of kids) {
        const m = fuzzy(seg, baseOf(k))
        if (m && m.score > score) {
          best = k
          score = m.score
        }
      }
      if (best === null) return null
      cur = best
    }
    return cur
  }
  if (t.startsWith('/') || !space) return walk('')
  return walk('') ?? walk(space)
}

/** The folders one level under a folder; the spaces under the root. */
function children(dir: string, spaces: string[], dirs: Array<{ path: string }>): string[] {
  if (dir === '') return spaces.filter((s) => s !== '')
  return dirs.filter((d) => dirOf(d.path) === dir && d.path !== dir).map((d) => d.path)
}

/** The heading above a row, where a group starts: the recent folders,
 * and the folders after them. */
function section(rows: Row[], i: number): string | null {
  const row = rows[i]
  const prev = rows[i - 1]
  const isRecent = (r: Row | undefined) => r?.kind === 'folder' && r.recent === true
  if (isRecent(row) && !isRecent(prev)) return 'Recent'
  if (row?.kind === 'folder' && !row.recent && isRecent(prev)) return 'Folders'
  return null
}

/** The file a name makes: `.md` unless it already names a note file. */
export function noteFile(name: string): string {
  const clean = fileName(name)
  return NOTE_EXT.test(clean) ? clean : clean + '.md'
}

/** The note a folder and a name point at, with or without the extension,
 * in any case. */
export function existingNote<N extends { path: string }>(notes: N[], dir: string, name: string): N | null {
  if (!name) return null
  const want = fileName(name).toLowerCase()
  const bare = want.replace(NOTE_EXT, '')
  const prefix = dir ? dir.toLowerCase() + '/' : ''
  for (const n of notes) {
    const p = n.path.toLowerCase()
    if (!p.startsWith(prefix) || p.slice(prefix.length).includes('/')) continue
    const base = p.slice(prefix.length)
    if (base === want || base.replace(NOTE_EXT, '') === bare) return n
  }
  return null
}

/** The next free name in a folder: `name 2`, `name 3`, and so on. */
export function freeName(notes: Array<{ path: string }>, dir: string, name: string): string {
  const file = noteFile(name)
  const ext = file.slice(file.lastIndexOf('.'))
  const stem = file.slice(0, -ext.length)
  const taken = new Set(notes.filter((n) => dirOf(n.path) === dir).map((n) => baseOf(n.path).toLowerCase().replace(NOTE_EXT, '')))
  if (!taken.has(stem.toLowerCase())) return stem + ext
  let i = 2
  while (taken.has(`${stem} ${i}`.toLowerCase())) i++
  return `${stem} ${i}${ext}`
}

export function NewNotePicker({ spec, dirs, spaces, notes, recent, panes, touch, onCreate, onOpen, onClose }: Props) {
  const [text, setText] = useState(spec.initial)
  const [cursor, setCursor] = useState(0)
  // A message for the path it was said about; typing moves past it.
  const [said, setSaid] = useState<{ at: string; msg: string } | null>(null)
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLUListElement>(null)

  useEffect(() => {
    const el = input.current
    if (!el) return
    setText(spec.initial)
    el.value = spec.initial
    el.focus()
    el.setSelectionRange(el.value.length, el.value.length)
  }, [spec])

  const parsed = useMemo(() => parsePath(text, spec.space, spaces, dirs), [text, spec.space, spaces, dirs])
  const exists = useMemo(() => (parsed.error ? null : existingNote(notes, parsed.dir, parsed.name)), [notes, parsed])

  const rows = useMemo<Row[]>(() => {
    const out: Row[] = []
    const { dir, name, browse } = parsed
    if (exists) out.push({ kind: 'open', id: exists.id, label: `Open ${exists.title || baseOf(exists.path)}`, detail: exists.path })
    // Notes in the folder whose names are close to the one typed.
    if (name && !parsed.error) {
      const near: Array<{ n: PickerNote; score: number }> = []
      for (const n of notes) {
        if (dirOf(n.path) !== dir || n === exists) continue
        const m = fuzzy(name.replace(NOTE_EXT, ''), baseOf(n.path).replace(NOTE_EXT, ''))
        if (m) near.push({ n, score: m.score })
      }
      near.sort((a, b) => b.score - a.score)
      for (const { n } of near.slice(0, exists ? 4 : 3)) out.push({ kind: 'note', id: n.id, label: n.title || baseOf(n.path), detail: n.path })
    }
    if (touch && browse) out.push({ kind: 'up', path: dirOf(browse), label: dirOf(browse) ? `Up to ${dirOf(browse)}/` : 'Up to the spaces' })
    const listed = new Set<string>()
    // Untouched, the recent folders come first; typed, the ones that fit
    // lead the folders from elsewhere.
    const known = new Set(dirs.map((d) => d.path))
    const recents = recent.filter((p) => known.has(p))
    if (text === spec.initial) {
      for (const p of recents) out.push({ kind: 'folder', path: p, label: p + '/', recent: true })
    }
    // The folders under what is typed, narrowed by the name part.
    if (browse !== null) {
      const kids = children(browse, spaces, dirs)
      let picked = kids
      if (name) {
        picked = kids
          .map((k) => ({ k, m: fuzzy(name, baseOf(k)) }))
          .filter((x) => x.m)
          .sort((a, b) => (b.m?.score ?? 0) - (a.m?.score ?? 0))
          .map((x) => x.k)
      }
      for (const k of picked.slice(0, 60)) {
        listed.add(k)
        out.push({ kind: 'folder', path: k, label: baseOf(k) + '/' })
      }
    }
    // Folders elsewhere that fit the name, by their whole path.
    if (name) {
      const ranked = (paths: string[]) =>
        paths
          .filter((p) => p !== '' && p !== browse && !listed.has(p))
          .map((p) => ({ p, m: fuzzy(name, p) }))
          .filter((x) => x.m)
          .sort((a, b) => (b.m?.score ?? 0) - (a.m?.score ?? 0))
          .map((x) => x.p)
      for (const p of ranked(recents).slice(0, 4)) {
        listed.add(p)
        out.push({ kind: 'folder', path: p, label: p + '/', recent: true })
      }
      for (const p of ranked(dirs.map((d) => d.path)).slice(0, 12)) out.push({ kind: 'folder', path: p, label: p + '/' })
    }
    return out
  }, [parsed, exists, notes, dirs, spaces, touch, recent, text, spec.initial])

  // The highlight starts on the first real row, not on the way up;
  // with only the way up there is none.
  useEffect(() => setCursor(rows.findIndex((r) => r.kind !== 'up')), [text])
  useEffect(() => {
    const el = list.current?.querySelector<HTMLElement>(`[data-i="${cursor}"]`)
    el?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  function put(v: string): void {
    setText(v)
    const el = input.current
    if (el) {
      el.value = v
      el.focus()
      el.setSelectionRange(v.length, v.length)
    }
  }

  /** Writes a folder into the input and lists its children. */
  function complete(row: Row | undefined): void {
    if (!row || (row.kind !== 'folder' && row.kind !== 'up')) return
    put(row.path ? row.path + '/' : '')
  }

  function up(): void {
    const d = parsed.browse ?? parsed.dir
    const parent = dirOf(d)
    put(parent ? parent + '/' : '')
  }

  function create(how: CreateHow, force: boolean): void {
    const row = rows[cursor]
    if (!force && row && (row.kind === 'open' || row.kind === 'note')) {
      onClose()
      onOpen(row.id, how === 'background' ? 'background' : how)
      return
    }
    if (parsed.error) {
      setSaid({ at: text, msg: parsed.error })
      return
    }
    const name = parsed.name
    // Shift+Enter keeps the picker, back at the folder, for the next one.
    if (how === 'background') {
      onCreate(parsed.dir, name || null, how, force)
      const at = parsed.dir + '/'
      put(at)
      setSaid({ at, msg: `Made ${name ? noteFile(name) : 'an untitled note'} in ${at}, in a tab behind this one.` })
      return
    }
    onClose()
    onCreate(parsed.dir, name || null, how, force)
  }

  function onKey(ev: KeyboardEvent): void {
    const mod = isMac ? ev.metaKey : ev.ctrlKey
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
      case 'Tab': {
        ev.preventDefault()
        if (ev.shiftKey) {
          up()
          break
        }
        const row = rows[cursor]
        complete(row && (row.kind === 'folder' || row.kind === 'up') ? row : rows.find((r) => r.kind === 'folder'))
        break
      }
      case 'Backspace': {
        // Right after a slash, Backspace takes the whole folder off.
        const el = ev.currentTarget as HTMLInputElement
        const v = el.value
        if (!v.endsWith('/') || el.selectionStart !== v.length || el.selectionEnd !== v.length || ev.altKey || mod) break
        ev.preventDefault()
        const rest = v.replace(/\/+$/, '')
        put(rest.slice(0, rest.lastIndexOf('/') + 1))
        break
      }
      case 'Enter':
        ev.preventDefault()
        if (ev.isComposing) break
        create(ev.altKey && panes ? 'right' : ev.shiftKey ? 'background' : 'tab', mod)
        break
    }
  }

  const active = rows[cursor]
  const flash = said && said.at === text ? said.msg : null
  const opening = active && (active.kind === 'open' || active.kind === 'note')
  const k = (s: string) => <kbd>{s}</kbd>

  return (
    <div class="overlay" onMouseDown={(ev) => { if (ev.target === ev.currentTarget) onClose() }}>
      <div class={'palette newnote' + (touch ? ' newnote-touch' : '')} role="dialog" aria-label="New note">
        <input
          ref={input}
          class="palette-input"
          type="text"
          value={text}
          placeholder="space/folder/name"
          autocomplete="off"
          autocapitalize="off"
          spellcheck={false}
          enterkeyhint="go"
          aria-label="Path for the new note"
          onInput={(ev) => setText((ev.target as HTMLInputElement).value)}
          onKeyDown={onKey}
        />
        <div class={'newnote-what' + (flash || parsed.error ? ' newnote-error' : '')} aria-live="polite">
          <span class="newnote-says">
            {flash ??
              (opening
                ? `Opens ${active.detail}`
                : parsed.error
                  ? parsed.error
                  : parsed.name
                    ? `Creates ${parsed.dir}/${exists ? freeName(notes, parsed.dir, parsed.name) : noteFile(parsed.name)}`
                    : `Creates an untitled note in ${parsed.dir}/`)}
          </span>
          {!flash &&
            !opening &&
            !parsed.error &&
            parsed.missing.map((m) => (
              <span key={m} class="newnote-tag" title={`${m}/ is made`}>
                {baseOf(m)}/ <em>new folder</em>
              </span>
            ))}
          {touch && (
            <button type="button" class="btn primary newnote-go" onMouseDown={(ev) => ev.preventDefault()} onClick={() => create('tab', false)}>
              {opening ? 'Open' : 'Create'}
            </button>
          )}
        </div>
        {rows.length > 0 && (
          <ul class="palette-list" ref={list} role="listbox">
            {rows.map((row, i) => [
              section(rows, i) && (
                <li key={'head' + i} class="palette-head" role="presentation">
                  {section(rows, i)}
                </li>
              ),
              <li
                key={row.kind + (row.kind === 'folder' && row.recent ? ':recent:' : '') + (row.kind === 'open' || row.kind === 'note' ? row.id : row.path)}
                data-i={i}
                class={'palette-row' + (i === cursor ? ' active' : '') + (row.kind === 'open' ? ' newnote-open' : '')}
                role="option"
                aria-selected={i === cursor}
                onMouseEnter={() => !touch && setCursor(i)}
                onMouseDown={(ev) => {
                  ev.preventDefault()
                  if (row.kind === 'folder' || row.kind === 'up') complete(row)
                  else {
                    onClose()
                    onOpen(row.id, 'tab')
                  }
                }}
              >
                <Icon name={row.kind === 'folder' ? 'folder' : row.kind === 'up' ? 'arrow-left' : 'file'} size={15} class="palette-folder" />
                <span class="palette-label">{row.label}</span>
                {'detail' in row && row.detail && <span class="palette-detail">{row.detail}</span>}
                {row.kind === 'folder' && touch && (
                  <span class="newnote-into" aria-label={`Into ${row.path}/`}>
                    <Icon name="chevron-right" size={16} />
                  </span>
                )}
                {row.kind === 'folder' && !touch && i === cursor && <kbd class="palette-key">Tab</kbd>}
                {row.kind === 'open' && !touch && <kbd class="palette-key">Enter</kbd>}
              </li>,
            ])}
          </ul>
        )}
        {!touch && (
          <p class="palette-hint newnote-keys">
            {k('Enter')} create {k('Tab')} into folder {k('⇧Tab')} up {panes && <>{k(isMac ? '⌥Enter' : 'Alt+Enter')} other pane </>}
            {k('⇧Enter')} in the background {exists && <>{k(isMac ? '⌘Enter' : 'Ctrl+Enter')} create anyway</>}
          </p>
        )}
      </div>
    </div>
  )
}
