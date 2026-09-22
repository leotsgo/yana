// The tasks page: every open box across a space (or every space the
// account belongs to), grouped by the note it lives in. Each row links
// back to its line in the note, ticks in place through the same write
// the in-note checkbox uses, and the list follows changes live over the
// relay's space watch.

import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks'

import { api, ApiError, dirOf, spaceOf, stem } from './api'
import type { SpaceInfo, TagCount, Task } from './api'
import { Icon } from './icons'
import type { FlatNote } from './tree'
import { SpaceWatch } from './watch'
import { howFrom, openProps, wireOpen } from './workspace'
import type { OpenHow } from './workspace'

/** How long a change signal waits before the list refetches, so a run
 * of typing (or a write-back settling) arrives as one refresh. */
const LIVE_DEBOUNCE_MS = 2000

export interface TasksPageProps {
  /** '' merges every space the account belongs to. */
  space: string
  tag: string
  path: string
  spaces: SpaceInfo[] | null
  /** The tree's notes, for resolving wikilinks in task text and listing
   * the folders a filter can pick. */
  notes: FlatNote[]
  onOpen: (id: string, line: number | null, how?: OpenHow) => void
  /** Change the filters; reroutes the page. */
  onNavigate: (space: string, tag: string, path: string) => void
  onToast: (msg: string, action?: { label: string; run: () => void }) => void
}

interface NoteGroup {
  note: Task['note']
  rows: Task[]
}

export function TasksPage({ space, tag, path, spaces, notes, onOpen, onNavigate, onToast }: TasksPageProps) {
  const [showDone, setShowDone] = useState(false)
  const [tasks, setTasks] = useState<Task[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [tags, setTags] = useState<TagCount[]>([])
  const [busy, setBusy] = useState(false)
  const seq = useRef(0)

  const targets = useMemo(() => (space ? [space] : (spaces ?? []).map((s) => s.name)), [space, spaces])

  const fetchTasks = useCallback(
    async (done: boolean) => {
      const mine = ++seq.current
      setBusy(true)
      try {
        const q: Parameters<typeof api.tasks>[0] = { done }
        if (space) q.space = space
        if (tag) q.tag = tag
        if (path) q.path = path
        const { tasks: rows } = await api.tasks(q)
        if (mine !== seq.current) return
        setTasks(rows)
        setError(null)
      } catch (err) {
        if (mine !== seq.current) return
        setError(err instanceof ApiError ? err.message : 'Could not load the tasks.')
      } finally {
        if (mine === seq.current) setBusy(false)
      }
    },
    [space, tag, path],
  )

  useEffect(() => {
    if (!space && spaces === null) return
    setTasks(null)
    void fetchTasks(showDone)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [space, tag, path, showDone, spaces])

  useEffect(() => {
    api
      .tags()
      .then(({ tags }) => setTags(tags))
      .catch(() => setTags([]))
  }, [])

  // Live: hear about note changes in the spaces being listed and
  // refetch once they settle. The own tick arrives here too, which is
  // what confirms the row's new state.
  useEffect(() => {
    const key = targets.join('\0')
    if (key === '') return
    let timer: number | undefined
    const schedule = () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => void fetchTasks(showDone), LIVE_DEBOUNCE_MS)
    }
    const watch = new SpaceWatch('user:tasks', schedule)
    watch.watch(key.split('\0'))
    watch.start()
    return () => {
      window.clearTimeout(timer)
      watch.destroy()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [targets.join('\0'), showDone])

  // The folders a filter can pick: the directories of the listed
  // spaces, one level of choice to keep the picker readable.
  const folders = useMemo(() => {
    const inSpace = space ? [space] : (spaces ?? []).map((s) => s.name)
    const names = new Set(inSpace)
    const dirs = new Set<string>()
    for (const n of notes) {
      const sp = spaceOf(n.path)
      if (!names.has(sp)) continue
      const dir = dirOf(n.path)
      if (dir && dir !== sp) dirs.add(dir)
    }
    return [...dirs].sort()
  }, [notes, spaces, space])

  const groups = useMemo(() => groupByNote(tasks ?? []), [tasks])

  const tick = useCallback(
    (t: Task, to: boolean) => {
      if (t.done === to) return
      const row = (r: Task) => r.note.id === t.note.id && r.line === t.line
      // Optimistic: the box moves now, the write lands or the row
      // reverts with the reason said out loud.
      const set = (done: boolean) => setTasks((rows) => (rows ?? []).map((r) => (row(r) ? { ...r, done } : r)))
      set(to)
      api
        .tickTask(t.note.id, t.line, to)
        .then(() => {
          onToast(to ? 'Ticked.' : 'Unticked.', {
            label: 'Undo',
            run: () => {
              api
                .tickTask(t.note.id, t.line, !to)
                .then(() => {
                  set(!to)
                  onToast('Put back.')
                })
                .catch((err: unknown) => onToast(err instanceof ApiError ? err.message : 'Could not put it back.'))
            },
          })
        })
        .catch((err: unknown) => {
          set(!to)
          if (err instanceof ApiError && err.status === 409) void fetchTasks(showDone)
          else onToast(err instanceof ApiError ? err.message : 'Could not tick it.')
        })
    },
    [onToast, fetchTasks, showDone],
  )

  const spaceOptions = useMemo(() => {
    const names = new Set((spaces ?? []).map((s) => s.name))
    if (space && !names.has(space)) names.add(space)
    return [...names]
  }, [spaces, space])

  return (
    <div class="page-scroll report tasks-report">
      <header class="report-head">
        <h1 class="report-title">Tasks</h1>
        <p class="report-sub">
          {space ? (path ? `Open boxes under ${space}/${path}` : `Open boxes in ${space}`) : 'Every open box across your spaces'}
          {tasks !== null && !showDone ? ` — ${tasks.length}` : ''}
        </p>
      </header>
      <div class="activity-controls">
        <label class="activity-filter">
          <span>Space</span>
          <select value={space} onChange={(ev) => onNavigate((ev.target as HTMLSelectElement).value, tag, '')} aria-label="Filter by space">
            <option value="">Every space</option>
            {spaceOptions.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </label>
        <label class="activity-filter">
          <span>Folder</span>
          <select value={path} onChange={(ev) => onNavigate(space, tag, (ev.target as HTMLSelectElement).value)} aria-label="Filter by folder">
            <option value="">Every folder</option>
            {folders.map((d) => (
              <option key={d} value={d}>
                {d.slice((space ? space : spaceOf(d)).length + 1)}/
              </option>
            ))}
          </select>
        </label>
        <label class="activity-filter">
          <span>Tag</span>
          <select value={tag} onChange={(ev) => onNavigate(space, (ev.target as HTMLSelectElement).value, path)} aria-label="Filter by tag">
            <option value="">Any tag</option>
            {tags.map((t) => (
              <option key={t.tag} value={t.tag}>
                #{t.tag} ({t.count})
              </option>
            ))}
          </select>
        </label>
        <label class="activity-filter">
          <span>Show</span>
          <select value={showDone ? 'done' : 'open'} onChange={(ev) => setShowDone((ev.target as HTMLSelectElement).value === 'done')} aria-label="Open or done">
            <option value="open">Open</option>
            <option value="done">Done, last 30 days</option>
          </select>
        </label>
      </div>
      {error ? (
        <div class="empty-state">
          <Icon name="check-square" size={28} />
          <p>{error}</p>
        </div>
      ) : tasks === null || (!space && spaces === null) ? (
        <div class="placeholder muted">{busy ? 'Loading…' : 'Opening…'}</div>
      ) : groups.length === 0 ? (
        <div class="empty-state">
          <Icon name="check-square" size={28} />
          <p>{showDone ? 'Nothing was completed here in the last 30 days.' : tag || path ? 'No open boxes here by that filter.' : 'No open boxes. Write `- [ ]` on a line to make one.'}</p>
        </div>
      ) : (
        <div class="tasks-list">
          {groups.map((g) => (
            <TaskGroup key={g.note.id} group={g} showDone={showDone} notes={notes} onOpen={onOpen} onTick={tick} onTag={(t) => onNavigate(space, t, path)} />
          ))}
        </div>
      )}
    </div>
  )
}

function TaskGroup({
  group,
  showDone,
  notes,
  onOpen,
  onTick,
  onTag,
}: {
  group: NoteGroup
  showDone: boolean
  notes: FlatNote[]
  onOpen: (id: string, line: number | null, how?: OpenHow) => void
  onTick: (t: Task, to: boolean) => void
  onTag: (tag: string) => void
}) {
  const host = useRef<HTMLUListElement>(null)
  const note = group.note
  const first = group.rows[0]

  useEffect(() => {
    const el = host.current
    if (!el) return
    wireTaskLinks(el, note.space, notes, onOpen, onTag)
  }, [note, notes, onOpen, onTag, showDone])

  return (
    <section class="task-group" aria-label={note.title || note.path}>
      <h2 class="task-group-head">
        <a
          class="task-group-title"
          href={`/n/${note.id}`}
          title={note.path}
          {...openProps((how) => onOpen(note.id, first ? first.line : null, how))}
        >
          {note.title || note.path}
        </a>
        <span class="task-group-path">{note.path}</span>
        <span class="task-group-count">{group.rows.length}</span>
      </h2>
      <ul class="task-rows" ref={host}>
        {group.rows.map((t, i) => {
          const prev = i > 0 ? group.rows[i - 1] : undefined
          const newHeading = !prev || prev.heading !== t.heading
          return (
            <li key={t.line} class={'task-row' + (t.done ? ' done' : '')} style={t.indent > 0 ? `--task-indent:${Math.min(t.indent, 6)}` : undefined}>
              <input
                type="checkbox"
                class="task-box"
                checked={t.done}
                aria-label={t.done ? 'Mark open' : 'Tick'}
                title={t.done ? 'Untick this box' : 'Tick this box'}
                onChange={(ev) => onTick(t, (ev.target as HTMLInputElement).checked)}
              />
              <span
                class="task-text"
                title={`Line ${t.line + 1} of ${note.path}`}
                dangerouslySetInnerHTML={{ __html: t.text || '<em class="task-empty">(no text)</em>' }}
                onClick={(ev) => {
                  const target = ev.target as HTMLElement
                  if (target.closest('a, span.tag')) return
                  onOpen(note.id, t.line, howFrom(ev))
                }}
                onAuxClick={(ev) => {
                  const target = ev.target as HTMLElement
                  if (ev.button !== 1 || target.closest('a, span.tag')) return
                  ev.preventDefault()
                  onOpen(note.id, t.line, 'background')
                }}
              />
              {newHeading && t.heading && <span class="task-heading">{t.heading}</span>}
            </li>
          )
        })}
      </ul>
    </section>
  )
}

/** Groups tasks by note in the listing order; rows within a note keep
 * their file order from the server. */
function groupByNote(rows: Task[]): NoteGroup[] {
  const groups: NoteGroup[] = []
  const byNote = new Map<string, NoteGroup>()
  for (const t of rows) {
    let g = byNote.get(t.note.id)
    if (!g) {
      g = { note: t.note, rows: [] }
      byNote.set(t.note.id, g)
      groups.push(g)
    }
    g.rows.push(t)
  }
  return groups
}

/** Wires the tags and wikilinks in rendered task text. Wikilinks resolve
 * against the tree's notes in the linking note's space — the same
 * by-name rule the editor's completions use — and an unresolved one
 * stays plain text rather than offering to create anything here. */
function wireTaskLinks(el: HTMLElement, space: string, notes: FlatNote[], onOpen: (id: string, line: number | null, how?: OpenHow) => void, onTag: (tag: string) => void): void {
  const byStem = new Map<string, string[]>()
  for (const n of notes) {
    if (spaceOf(n.path) !== space) continue
    const key = stem(n.name).toLowerCase()
    const list = byStem.get(key) ?? []
    list.push(n.id)
    byStem.set(key, list)
  }
  for (const span of [...el.querySelectorAll<HTMLSpanElement>('span.tag[data-tag]')]) {
    const tag = span.dataset['tag'] ?? ''
    if (!tag) continue
    span.setAttribute('role', 'link')
    span.setAttribute('tabindex', '0')
    span.title = `Filter by #${tag}`
    const go = () => onTag(tag)
    span.addEventListener('click', go)
    span.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter' || ev.key === ' ') {
        ev.preventDefault()
        go()
      }
    })
  }
  for (const span of [...el.querySelectorAll<HTMLSpanElement>('span.wikilink[data-target]')]) {
    const raw = span.dataset['target'] ?? ''
    const hits = byStem.get(raw.toLowerCase()) ?? []
    if (hits.length !== 1) {
      span.title = hits.length === 0 ? `${raw} does not exist (yet)` : `${raw} matches several notes`
      continue
    }
    const id = hits[0] as string
    const a = document.createElement('a')
    a.href = `/n/${id}`
    a.className = 'wikilink resolved'
    a.title = raw
    a.textContent = span.textContent ?? raw
    wireOpen(a, (how) => onOpen(id, null, how))
    span.replaceWith(a)
  }
}
