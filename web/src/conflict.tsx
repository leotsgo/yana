// Conflict copies: the dialog that resolves one against the note it
// belongs to. The diff is the same unified text the history panel
// shows; the three ways out are the trash (keep mine), an edit (keep
// theirs), and a rename (keep both). The module also remembers the
// conflict files this client created itself, so the shell's
// new-conflict toast keeps quiet about those.

import { useEffect, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { ConflictAction, Note } from './api'
import { fmtDate } from './dom'
import { Icon } from './icons'

// Paths this client parked itself in the last two minutes: a save over
// a diverged file, a restore beside a newer note.
const localConflicts = new Map<string, number>()

const conflictNameRe = /\.conflict-\d{8}[-T]\d{6}(-\d+)?\.(md|markdown|html|htm)$/i

/** Whether a path carries a conflict copy's name. */
export function isConflictName(path: string): boolean {
  return conflictNameRe.test(path)
}

/** Records a conflict path this client just created. */
export function markLocalConflict(path: string): void {
  localConflicts.set(path, Date.now())
}

/** Reports whether this client created the conflict at path recently. */
export function isLocalConflict(path: string): boolean {
  const at = localConflicts.get(path)
  if (at === undefined) return false
  if (Date.now() - at > 120_000) {
    localConflicts.delete(path)
    return false
  }
  return true
}

export interface ConflictDialogProps {
  /** The surviving note whose conflicts are being resolved. */
  noteId: string
  onClose: () => void
  onToast: (msg: string) => void
  /** A resolution landed: the tree and the note need a reload. */
  onResolved: () => void
}

export function ConflictDialog({ noteId, onClose, onToast, onResolved }: ConflictDialogProps) {
  const [conflicts, setConflicts] = useState<Note[] | null>(null)
  const [pick, setPick] = useState(0)
  const [diff, setDiff] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const reload = () => {
    api
      .noteConflicts(noteId)
      .then(({ conflicts }) => {
        setConflicts(conflicts)
        if (conflicts.length === 0) onClose()
      })
      .catch((err: unknown) => setError(err instanceof ApiError ? err.message : 'Could not load the conflicts.'))
  }

  useEffect(() => {
    reload()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [noteId])

  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === 'Escape') {
        ev.preventDefault()
        onClose()
      }
    }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [onClose])

  const current = conflicts && conflicts.length > 0 ? conflicts[Math.min(pick, conflicts.length - 1)] ?? null : null

  useEffect(() => {
    if (!current) return
    setDiff(null)
    let alive = true
    api
      .conflictDiff(current.id)
      .then(({ diff }) => {
        if (alive) setDiff(diff || 'The two bodies are the same; only the names differ.')
      })
      .catch((err: unknown) => {
        if (alive) setDiff(err instanceof ApiError ? err.message : 'Could not load the diff.')
      })
    return () => {
      alive = false
    }
  }, [current?.id])

  const resolve = (action: ConflictAction) => {
    if (!current || busy) return
    setBusy(true)
    api
      .resolveConflict(current.id, action)
      .then((res) => {
        setBusy(false)
        onResolved()
        const name = current.title || current.path
        if (action === 'mine') onToast(`Kept this note. ${name} is in the trash.`)
        else if (action === 'theirs') onToast('Kept the copy. Its text is this note now.')
        else onToast(`Kept both. The copy is ${res.path ?? 'renamed'} now.`)
        reload()
        setPick(0)
      })
      .catch((err: unknown) => {
        setBusy(false)
        onToast(err instanceof ApiError ? err.message : 'Could not resolve the conflict.')
      })
  }

  return (
    <div class="overlay" onMouseDown={(ev) => { if (ev.target === ev.currentTarget) onClose() }}>
      <div class="confirm conflict-dialog" role="dialog" aria-label="Resolve conflicts">
        <div class="share-head">
          <h2 class="confirm-title">
            {conflicts === null ? 'Conflicts' : `${conflicts.length} ${conflicts.length === 1 ? 'conflict' : 'conflicts'}`}
          </h2>
          <button type="button" class="icon-btn" aria-label="Close" onClick={onClose}>
            <Icon name="x" size={18} />
          </button>
        </div>
        {error && <p class="error confirm-body">{error}</p>}
        {conflicts !== null && conflicts.length > 1 && (
          <div class="conflict-tabs" role="tablist" aria-label="conflict copies">
            {conflicts.map((c, i) => (
              <button
                type="button"
                role="tab"
                key={c.id}
                aria-selected={i === pick}
                class={i === pick ? 'on' : ''}
                onClick={() => setPick(i)}
                title={c.path}
              >
                {conflictWhen(c)} — {c.title || c.path}
              </button>
            ))}
          </div>
        )}
        {current && (
          <>
            <p class="confirm-body">
              {current.path} was parked beside this note. Keep this note, keep the copy, or keep both as an ordinary
              note.
            </p>
            <pre class="history-diff conflict-diff">{diff ?? 'Loading diff…'}</pre>
            <div class="confirm-actions">
              <button type="button" class="btn" disabled={busy} title="The copy moves to the trash; this note stays as it is" onClick={() => resolve('mine')}>
                <Icon name="check" />
                Keep mine
              </button>
              <button type="button" class="btn" disabled={busy} title="The copy's text replaces this note; the copy moves to the trash" onClick={() => resolve('theirs')}>
                <Icon name="restore" />
                Keep theirs
              </button>
              <button type="button" class="btn primary" disabled={busy} title="The copy is renamed to an ordinary note beside this one" onClick={() => resolve('both')}>
                <Icon name="layers" />
                Keep both
              </button>
            </div>
          </>
        )}
        {conflicts !== null && conflicts.length === 0 && <p class="confirm-body">Nothing waiting.</p>}
      </div>
    </div>
  )
}

/** How long ago an ISO time was, at day granularity. */
export function fmtAge(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime()
  if (Number.isNaN(ms)) return iso
  const min = Math.floor(ms / 60_000)
  if (min < 1) return 'just now'
  if (min < 60) return `${min} min ago`
  const h = Math.floor(min / 60)
  if (h < 24) return `${h} h ago`
  const d = Math.floor(h / 24)
  return `${d} day${d === 1 ? '' : 's'} ago`
}

/** When a conflict copy was made, read from its file name timestamp. */
function conflictWhen(n: Note): string {
  const m = /\.conflict-(\d{8})[-T](\d{6})/.exec(n.path)
  if (!m || !m[1] || !m[2]) return fmtDate(n.mtime)
  const d = new Date(
    Date.UTC(
      Number(m[1].slice(0, 4)),
      Number(m[1].slice(4, 6)) - 1,
      Number(m[1].slice(6, 8)),
      Number(m[2].slice(0, 2)),
      Number(m[2].slice(2, 4)),
    ),
  )
  return fmtDate(d.toISOString())
}
