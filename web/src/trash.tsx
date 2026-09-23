// The trash: every deleted note with something left to recover, its
// deletion time and original path. Restore puts the file back where it
// lived (or beside a newer occupant, marked as a conflict). Delete
// forever and Empty trash are the only permanent destruction, and both
// ask before they act.

import { useCallback, useEffect, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { TrashEntry } from './api'
import { markLocalConflict } from './conflict'
import { fmtDate } from './dom'
import { Icon } from './icons'
import type { ConfirmSpec } from './confirm'

export interface TrashPageProps {
  onOpen: (id: string) => void
  onToast: (msg: string) => void
  confirm: (spec: ConfirmSpec) => void
  /** Called after anything here changes the tree. */
  onChanged: () => void
}

export function TrashPage({ onOpen, onToast, confirm, onChanged }: TrashPageProps) {
  const [entries, setEntries] = useState<TrashEntry[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const reload = useCallback(async () => {
    try {
      const { entries } = await api.trash()
      setEntries(entries)
      setError(null)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Could not load the trash.')
    }
  }, [])

  useEffect(() => {
    void reload()
  }, [reload])

  const restore = (e: TrashEntry) => {
    if (!e.id || busy) return
    setBusy(e.id)
    api
      .restoreTrash(e.id)
      .then((res) => {
        setBusy(null)
        onChanged()
        void reload()
        if (res.conflict) {
          markLocalConflict(res.path)
          onToast(`A note now lives at ${e.path}; restored beside it as ${res.path}.`)
        } else {
          onToast(`Restored ${e.path}.`)
        }
        if (res.note?.id && !res.deferred) onOpen(res.note.id)
      })
      .catch((err: unknown) => {
        setBusy(null)
        onToast(err instanceof ApiError ? err.message : 'Could not restore.')
      })
  }

  const destroy = (e: TrashEntry) => {
    if (busy) return
    confirm({
      title: `Delete ${e.title || e.path} forever?`,
      body: 'The file, the edit history, and everything recoverable about this note are destroyed. This cannot be undone.',
      confirmLabel: 'Delete forever',
      danger: true,
      onConfirm: () => {
        setBusy(e.id)
        api
          .destroyTrash(e.id)
          .then(() => {
            setBusy(null)
            void reload()
            onToast('Destroyed.')
          })
          .catch((err: unknown) => {
            setBusy(null)
            onToast(err instanceof ApiError ? err.message : 'Could not destroy the entry.')
          })
      },
    })
  }

  const empty = () => {
    if (!entries || entries.length === 0 || busy) return
    confirm({
      title: `Empty the trash? ${entries.length} ${entries.length === 1 ? 'entry is' : 'entries are'} destroyed forever.`,
      body: 'Files, edit histories, everything recoverable. This is the only permanent deletion, and it cannot be undone.',
      rows: entries.slice(0, 12).map((e) => ({ label: e.title || e.path, detail: e.path })),
      confirmLabel: 'Empty trash',
      danger: true,
      onConfirm: () => {
        setBusy('all')
        api
          .emptyTrash()
          .then((res) => {
            setBusy(null)
            void reload()
            onChanged()
            onToast(`Destroyed ${res.destroyed} ${res.destroyed === 1 ? 'entry' : 'entries'}.`)
          })
          .catch((err: unknown) => {
            setBusy(null)
            onToast(err instanceof ApiError ? err.message : 'Could not empty the trash.')
          })
      },
    })
  }

  if (error) {
    return (
      <div class="placeholder">
        <p class="error">{error}</p>
      </div>
    )
  }
  if (!entries) {
    return <div class="placeholder muted">Opening…</div>
  }

  return (
    <div class="page-scroll report trash">
      <header class="report-head">
        <h1 class="report-title">Trash</h1>
        <p class="report-sub">
          {entries.length === 0
            ? 'Nothing in it.'
            : `${entries.length} ${entries.length === 1 ? 'note' : 'notes'}. Kept for 30 days; emptying is forever.`}
        </p>
        {entries.length > 0 && (
          <button type="button" class="btn danger" disabled={busy !== null} onClick={empty}>
            <Icon name="trash" />
            Empty trash
          </button>
        )}
      </header>
      {entries.length === 0 ? (
        <div class="empty-state">
          <Icon name="trash" size={28} />
          <p>Deleted notes sit here for 30 days, then go for good.</p>
        </div>
      ) : (
        <ul class="trash-list">
          {entries.map((e) => (
            <li key={(e.trash_path ?? '') + e.id + e.deleted_at} class="trash-row">
              <div class="trash-main">
                <span class="trash-name">{e.title || e.path}</span>
                <span class="trash-path" title={e.trash_path ? `now at ${e.trash_path}` : undefined}>
                  {e.path}
                </span>
                {e.untracked && <span class="trash-flag" title="The index was rebuilt; this entry is read from the trash folder itself">untracked</span>}
                {!e.has_file && <span class="trash-flag" title="The file was removed outside the app; the edit history is what remains">history only</span>}
              </div>
              <div class="trash-side">
                <span class="trash-date" title={`deleted ${fmtDate(e.deleted_at)}`}>
                  {fmtDate(e.deleted_at)}
                </span>
                {e.id && (
                  <>
                    <button type="button" class="btn small" disabled={busy !== null} onClick={() => restore(e)}>
                      <Icon name="restore" />
                      Restore
                    </button>
                    <button type="button" class="btn small danger" disabled={busy !== null} onClick={() => destroy(e)}>
                      <Icon name="x" />
                      Delete forever
                    </button>
                  </>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
