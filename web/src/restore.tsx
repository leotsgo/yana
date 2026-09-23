// The point-in-time restore dialog: what restoring to a commit would
// do, listed exactly, before anything is touched. The preview comes
// from the server over the same endpoint the restore runs, so what the
// list says is what the restore does.

import { useEffect, useRef, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { PITPreview } from './api'
import { fmtDate } from './dom'
import { Icon } from './icons'

/** Opening the dialog for one feed entry: which commit, and which scope
 * ('' is the whole tree). onDone carries the summary when a restore
 * actually ran. */
export interface RestoreSpec {
  commit: string
  space: string
  onDone: (summary: { added: number; changed: number; deleted: number; moved: number }) => void
}

export function RestoreDialog({ spec, onClose }: { spec: RestoreSpec; onClose: () => void }) {
  const [preview, setPreview] = useState<PITPreview | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const cancel = useRef<HTMLButtonElement>(null)
  const closed = useRef(false)
  const close = () => {
    closed.current = true
    onClose()
  }

  useEffect(() => {
    let alive = true
    api
      .pitPreview(spec.commit, spec.space)
      .then((r) => {
        if (alive) setPreview(r.preview)
      })
      .catch((err: unknown) => {
        if (!alive) return
        setError(err instanceof ApiError ? err.message : 'Could not read what the restore would do.')
      })
    return () => {
      alive = false
    }
  }, [spec.commit, spec.space])

  useEffect(() => {
    cancel.current?.focus()
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === 'Escape' && !busy) {
        ev.preventDefault()
        close()
      }
    }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [busy])

  const tree = spec.space === ''
  const title = tree ? 'Restore the tree to here' : `Restore ${spec.space} to here`
  const what = tree ? 'every space' : spec.space

  const run = () => {
    setBusy(true)
    api
      .pitRestore(spec.commit, spec.space)
      .then((r) => {
        if (closed.current) return
        onClose()
        spec.onDone({ added: r.added, changed: r.changed, deleted: r.deleted, moved: r.moved })
      })
      .catch((err: unknown) => {
        setBusy(false)
        setError(err instanceof ApiError ? err.message : 'Could not restore.')
      })
  }

  const counts = preview ? countList(preview) : ''

  return (
    <div class="overlay" onMouseDown={(ev) => { if (ev.target === ev.currentTarget && !busy) close() }}>
      <div class="confirm restore-dialog" role="alertdialog" aria-label={title}>
        <h2 class="confirm-title">{title}</h2>
        {preview ? (
          <>
            <p class="confirm-body">
              {what === 'every space' ? 'The tree' : 'This space'} returns to how it stood at{' '}
              <strong>{preview.subject || 'that commit'}</strong>
              {preview.author ? <> — {preview.author}, {fmtDate(preview.date)}</> : null}.
            </p>
            {preview.changes.length === 0 ? (
              <p class="confirm-body">Nothing changes: it already stands here.</p>
            ) : (
              <>
                <p class="restore-counts">{counts}</p>
                <ul class="restore-list">
                  {preview.changes.map((c) => (
                    <li key={c.action + c.path} class="restore-row">
                      <span class={'activity-action ' + pitLabel(c.action)}>{pitLabel(c.action)}</span>
                      <span class="restore-path" title={c.path}>
                        {c.title || c.path}
                      </span>
                      {c.action === 'moved' && c.from && <span class="activity-from">from {c.from}</span>}
                    </li>
                  ))}
                </ul>
              </>
            )}
            <p class="confirm-body">
              What stands now is committed and tagged first, and everything this removes moves to the trash — so the
              restore itself can be undone. Open editors converge on the restored text.
            </p>
            <div class="confirm-actions">
              <button type="button" class="btn" ref={cancel} disabled={busy} onClick={close}>
                Cancel
              </button>
              <button type="button" class="btn danger" disabled={busy || preview.changes.length === 0} onClick={run}>
                <Icon name="restore" />
                {busy ? 'Restoring…' : 'Restore'}
              </button>
            </div>
          </>
        ) : error ? (
          <>
            <p class="confirm-body error">{error}</p>
            <div class="confirm-actions">
              <button type="button" class="btn" ref={cancel} onClick={close}>
                Close
              </button>
            </div>
          </>
        ) : (
          <>
            <p class="confirm-body">Reading what the restore would do…</p>
            <div class="confirm-actions">
              <button type="button" class="btn" ref={cancel} disabled onClick={() => {}}>
                Cancel
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

function pitLabel(action: string): string {
  if (action === 'added') return 'added'
  if (action === 'deleted') return 'deleted'
  if (action === 'moved') return 'moved'
  return 'edited'
}

function countList(p: PITPreview): string {
  const parts: string[] = []
  if (p.added > 0) parts.push(`${p.added} added`)
  if (p.changed > 0) parts.push(`${p.changed} changed`)
  if (p.deleted > 0) parts.push(`${p.deleted} deleted`)
  if (p.moved > 0) parts.push(`${p.moved} moved`)
  const total = p.changes.length
  return `${total} ${total === 1 ? 'path' : 'paths'}: ${parts.join(', ')}.`
}
