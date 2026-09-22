// The activity page: what changed, by whom, over the git history. The
// server delivers entries already folded — an agent's overnight run is
// one entry, a person's quiet-window commits one each — and this page
// adds the reading layer: day groups, the "since you last looked" line,
// and filters for a kind of author or one author. Without a space in
// the URL the feed merges every space the account belongs to.

import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { ActivityEntry, ActivityKind, SpaceInfo } from './api'
import { fmtDate } from './dom'
import { Icon } from './icons'
import * as prefs from './prefs'
import { openProps } from './workspace'
import type { OpenHow } from './workspace'

/** How far back the feed reaches. */
type FeedWindow = 'seen' | 'day' | 'week' | 'month' | 'all'

const WINDOWS: Array<{ id: FeedWindow; label: string }> = [
  { id: 'seen', label: 'Since I last looked' },
  { id: 'day', label: 'Last 24 hours' },
  { id: 'week', label: 'Last 7 days' },
  { id: 'month', label: 'Last 30 days' },
  { id: 'all', label: 'Everything' },
]

const KINDS: Array<{ id: 'all' | ActivityKind; label: string }> = [
  { id: 'all', label: 'Everyone' },
  { id: 'person', label: 'People' },
  { id: 'agent', label: 'Agents' },
  { id: 'filesystem', label: 'The files' },
]

/** One entry stamped with the space it came from. */
type Row = ActivityEntry & { space: string }

export interface ActivityPageProps {
  /** '' merges every space the account belongs to. */
  space: string
  /** A folder inside the space, from a folder's context menu. */
  path: string
  spaces: SpaceInfo[] | null
  onOpen: (id: string, how?: OpenHow) => void
  /** Change the space or folder filter; reroutes the page. */
  onNavigate: (space: string, path: string) => void
}

export function ActivityPage({ space, path, spaces, onOpen, onNavigate }: ActivityPageProps) {
  const [win, setWin] = useState<FeedWindow>(prefs.activitySeen() !== null ? 'seen' : 'week')
  const [kind, setKind] = useState<'all' | ActivityKind>('all')
  const [author, setAuthor] = useState('')
  const [entries, setEntries] = useState<Row[] | null>(null)
  const [authors, setAuthors] = useState<string[]>([])
  const [error, setError] = useState<string | null>(null)
  const [cursors, setCursors] = useState<Record<string, string>>({})
  const [more, setMore] = useState(false)
  const [busy, setBusy] = useState(false)
  // The marker as it was when the page opened; the divider sits under
  // everything newer than it. Captured once, before this visit advances
  // it.
  const seen = useRef<number | null>(prefs.activitySeen())
  const marked = useRef(false)

  const targets = useMemo(() => (space ? [space] : (spaces ?? []).map((s) => s.name)), [space, spaces])

  const sinceFor = useCallback((w: FeedWindow): string | undefined => {
    const now = Date.now()
    const day = 86_400_000
    if (w === 'seen') {
      const s = seen.current
      return new Date(s ?? now - 7 * day).toISOString()
    }
    if (w === 'day') return new Date(now - day).toISOString()
    if (w === 'week') return new Date(now - 7 * day).toISOString()
    if (w === 'month') return new Date(now - 30 * day).toISOString()
    return undefined
  }, [])

  const fetchPage = useCallback(
    async (mode: 'first' | 'older') => {
      if (targets.length === 0) {
        setEntries([])
        setMore(false)
        return
      }
      if (mode === 'first') setBusy(true)
      try {
        const results = await Promise.all(
          targets.map(async (sp) => {
            const cursor = mode === 'older' ? cursors[sp] ?? '' : ''
            const q: Parameters<typeof api.activity>[1] = {
              since: sinceFor(win),
              limit: 50,
            }
            if (author) q.author = author
            if (sp === space && path) q.path = path
            if (cursor) q.cursor = cursor
            return [sp, await api.activity(sp, q)] as const
          }),
        )
        const page = results
          .flatMap(([sp, r]) => r.entries.map((e) => ({ ...e, space: sp })))
          .sort((a, b) => (a.to < b.to ? 1 : a.to > b.to ? -1 : 0))
        const next: Record<string, string> = {}
        let any = false
        for (const [sp, r] of results) {
          next[sp] = r.next_cursor
          if (r.more) any = true
        }
        setError(null)
        if (mode === 'first') {
          setEntries(page)
          if (!author) {
            const names = new Set(page.map((e) => e.author))
            setAuthors((prev) => {
              const merged = new Set([...prev, ...names])
              return [...merged].sort((a, b) => a.localeCompare(b))
            })
          }
        } else {
          setEntries((prev) => [...(prev ?? []), ...page].sort((a, b) => (a.to < b.to ? 1 : a.to > b.to ? -1 : 0)))
        }
        setCursors(next)
        setMore(any)
        // The visit itself is the marker: next time, everything here is
        // old news. Only a successful load counts.
        if (!marked.current) {
          marked.current = true
          prefs.touchActivitySeen()
        }
      } catch (err) {
        if (err instanceof ApiError && err.status === 501) setError('History is off on this server.')
        else setError(err instanceof ApiError ? err.message : 'Could not load the activity.')
      } finally {
        setBusy(false)
      }
    },
    [targets, cursors, win, author, sinceFor, space, path],
  )

  useEffect(() => {
    if (!space && spaces === null) return // the space list is still loading
    setEntries(null)
    setCursors({})
    setMore(false)
    void fetchPage('first')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [space, path, win, author, spaces])

  const rows = useMemo(() => (entries ?? []).filter((e) => kind === 'all' || e.kind === kind), [entries, kind])
  // The seen marker's place in the filtered rows: everything above it
  // landed since the last visit.
  const seenIdx = useMemo(() => {
    if (seen.current === null) return -1
    return rows.findIndex((r) => new Date(r.to).getTime() <= (seen.current as number))
  }, [rows])
  const groups = useMemo(() => groupByDay(rows), [rows])

  return (
    <div class="page-scroll report activity">
      <header class="report-head">
        <h1 class="report-title">What changed</h1>
        <p class="report-sub">
          {space ? (path ? `Activity under ${space}/${path}` : `Activity in ${space}`) : 'Across every space you belong to'}
        </p>
      </header>
      <div class="activity-controls">
        <label class="activity-filter">
          <span>Space</span>
          <select
            value={space}
            onChange={(ev) => onNavigate((ev.target as HTMLSelectElement).value, '')}
            aria-label="Filter by space"
          >
            <option value="">Every space</option>
            {(spaces ?? []).map((s) => (
              <option key={s.name} value={s.name}>
                {s.label || s.name}
              </option>
            ))}
          </select>
        </label>
        <label class="activity-filter">
          <span>Window</span>
          <select value={win} onChange={(ev) => setWin((ev.target as HTMLSelectElement).value as FeedWindow)} aria-label="How far back">
            {WINDOWS.map((w) => (
              <option key={w.id} value={w.id}>
                {w.label}
              </option>
            ))}
          </select>
        </label>
        <label class="activity-filter">
          <span>Who</span>
          <select value={kind} onChange={(ev) => setKind((ev.target as HTMLSelectElement).value as 'all' | ActivityKind)} aria-label="Filter by kind of author">
            {KINDS.map((k) => (
              <option key={k.id} value={k.id}>
                {k.label}
              </option>
            ))}
          </select>
        </label>
        <label class="activity-filter">
          <span>Author</span>
          <select value={author} onChange={(ev) => setAuthor((ev.target as HTMLSelectElement).value)} aria-label="Filter by one author" disabled={authors.length === 0}>
            <option value="">Anyone</option>
            {authors.map((a) => (
              <option key={a} value={a}>
                {a}
              </option>
            ))}
          </select>
        </label>
      </div>
      {error ? (
        <div class="empty-state">
          <Icon name="history" size={28} />
          <p>{error}</p>
        </div>
      ) : entries === null || (!space && spaces === null) ? (
        <div class="placeholder muted">{busy ? 'Loading…' : 'Opening…'}</div>
      ) : rows.length === 0 ? (
        <div class="empty-state">
          <Icon name="history" size={28} />
          <p>
            {entries.length === 0
              ? 'Nothing changed here in this window.'
              : 'Nothing by that filter in this window.'}
          </p>
        </div>
      ) : (
        <>
          {groups.map((g) => (
            <section key={g.label} class="activity-day" aria-label={g.label}>
              <h2 class="section-title">{g.label}</h2>
              <ul class="activity-list">
                {g.rows.map((e, i) => {
                  const at = g.start + i
                  return (
                    <>
                      {at === seenIdx && (
                        <li class="activity-seen" role="separator">
                          <span>You've seen everything below this line</span>
                        </li>
                      )}
                      <Entry key={at} row={e} showSpace={!space} onOpen={onOpen} />
                    </>
                  )
                })}
              </ul>
            </section>
          ))}
          {more && (
            <div class="activity-more">
              <button type="button" class="btn" disabled={busy} onClick={() => void fetchPage('older')}>
                <Icon name="history" />
                Older
              </button>
            </div>
          )}
        </>
      )}
    </div>
  )
}

function Entry({ row, showSpace, onOpen }: { row: Row; showSpace: boolean; onOpen: (id: string, how?: OpenHow) => void }) {
  return (
    <li class="activity-entry">
      <div class="activity-head">
        <span class={'activity-chip ' + row.kind} title={kindTitle(row.kind)}>
          <Icon name={row.kind === 'agent' ? 'code' : row.kind === 'filesystem' ? 'folder' : 'user'} size={13} />
        </span>
        <span class="activity-author">{row.author}</span>
        {row.commits > 1 && (
          <span class="activity-run" title={`${fmtDate(row.from)} → ${fmtDate(row.to)}`}>
            {row.commits} commits over {fmtSpan(row.from, row.to)}
          </span>
        )}
        <span class="activity-time" title={fmtDate(row.to)}>
          {fmtAgo(row.to)}
        </span>
        {showSpace && <span class="activity-space">{row.space}</span>}
      </div>
      <ul class="activity-changes">
        {row.changes.map((c) => (
          <li key={c.path + c.action} class="activity-change">
            <span class={'activity-action ' + c.action}>{ACTION_LABELS[c.action]}</span>
            {c.id ? (
              <a
                class="activity-note"
                href={`/n/${c.id}`}
                title={c.path}
                {...openProps((how) => onOpen(c.id as string, how))}
              >
                {c.title || c.path}
              </a>
            ) : (
              <span class="activity-note gone" title={c.path}>
                {c.path}
              </span>
            )}
            {c.action === 'renamed' && c.from && <span class="activity-from">from {c.from}</span>}
          </li>
        ))}
      </ul>
    </li>
  )
}

const ACTION_LABELS: Record<string, string> = {
  added: 'added',
  modified: 'edited',
  renamed: 'renamed',
  deleted: 'deleted',
}

function kindTitle(k: ActivityKind): string {
  if (k === 'agent') return 'An agent made these changes'
  if (k === 'filesystem') return 'These changes arrived on the files'
  return 'A person made these changes'
}

interface DayGroup {
  label: string
  rows: Row[]
  /** This group's first index in the flat, filtered rows. */
  start: number
}

function groupByDay(rows: Row[]): DayGroup[] {
  const groups: DayGroup[] = []
  for (const r of rows) {
    const label = dayLabel(new Date(r.to))
    const last = groups[groups.length - 1]
    if (last && last.label === label) last.rows.push(r)
    else groups.push({ label, rows: [r], start: groups.reduce((n, g) => n + g.rows.length, 0) })
  }
  return groups
}

function dayLabel(d: Date): string {
  if (Number.isNaN(d.getTime())) return ''
  const start = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const today = start(new Date())
  const that = start(d)
  if (that === today) return 'Today'
  if (today - that === 86_400_000) return 'Yesterday'
  return d.toLocaleDateString(undefined, { month: 'long', day: 'numeric', year: d.getFullYear() === new Date().getFullYear() ? undefined : 'numeric' })
}

function fmtAgo(iso: string): string {
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return iso
  const s = Math.max(0, (Date.now() - t) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86_400) return `${Math.floor(s / 3600)}h ago`
  if (s < 7 * 86_400) return `${Math.floor(s / 86_400)}d ago`
  return fmtDate(iso)
}

function fmtSpan(fromIso: string, toIso: string): string {
  const s = Math.max(0, (new Date(toIso).getTime() - new Date(fromIso).getTime()) / 1000)
  if (s < 5400) return `${Math.max(1, Math.round(s / 60))} minutes`
  if (s < 129_600) return `${Math.round(s / 3600)} hours`
  return `${Math.round(s / 86_400)} days`
}

/** The home screen's "What changed" line: how many entries landed since
 * the marker, and the newest one as a teaser. It never marks anything
 * seen; only opening the feed does. */
export function WhatsChanged({ spaceNames, ready, onOpen }: { spaceNames: string[]; ready: boolean; onOpen: () => void }) {
  const [state, setState] = useState<{ count: number; latest: ActivityEntry | null } | null | 'off'>(null)
  const key = spaceNames.join('\0')
  useEffect(() => {
    if (!ready || key === '') return
    let alive = true
    const seen = prefs.activitySeen()
    const since = new Date(seen ?? Date.now() - 7 * 86_400_000).toISOString()
    Promise.all(key.split('\0').slice(0, 8).map((sp) => api.activity(sp, { since, limit: 30 })))
      .then((results) => {
        if (!alive) return
        const entries = results.flatMap((r) => r.entries)
        const latest = entries.reduce<ActivityEntry | null>((a, b) => (!a || b.to > a.to ? b : a), null)
        setState({ count: entries.length, latest })
      })
      .catch((err: unknown) => {
        if (!alive) return
        // With history off there is nothing to say; any other failure
        // stays quiet rather than nagging on the home screen.
        if (err instanceof ApiError && err.status === 501) setState('off')
        else setState(null)
      })
    return () => {
      alive = false
    }
  }, [key, ready])

  if (state === null || state === 'off') return null
  const seen = prefs.activitySeen()
  let text: string
  if (state.count === 0) {
    text = seen === null ? 'See what changed this week' : 'Nothing has changed since you last looked'
  } else {
    const n = state.count === 1 ? '1 change' : `${state.count} changes`
    const lead = seen === null ? 'this week' : 'since you last looked'
    const teaser = state.latest ? latestTeaser(state.latest) : ''
    text = `${n} ${lead}${teaser ? ' — ' + teaser : ''}`
  }
  return (
    <p class="home-whats">
      <a href="/activity" onClick={(ev) => { ev.preventDefault(); onOpen() }}>
        <Icon name="history" size={14} />
        {text}
      </a>
    </p>
  )
}

function latestTeaser(e: ActivityEntry): string {
  if (e.changes.length > 1) return `latest: ${e.author} touched ${e.changes.length} ${e.changes.length === 1 ? 'note' : 'notes'}, ${fmtAgo(e.to)}`
  const c = e.changes[0]
  if (!c) return ''
  const what = c.title || c.path
  return `latest: ${e.author} ${ACTION_LABELS[c.action] ?? 'changed'} ${what}, ${fmtAgo(e.to)}`
}
