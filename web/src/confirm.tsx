// A small confirmation dialog for destructive actions: deleting a note,
// destroying a trash entry, emptying the trash. Cancel is the default;
// the destructive button is only reachable by pointer or Tab + Enter.

import { useEffect, useRef } from 'preact/hooks'

export interface ConfirmRow {
  label: string
  detail?: string
}

export interface ConfirmSpec {
  title: string
  body?: string
  /** Listed under the title — the inbound links of a note about to be deleted. */
  rows?: ConfirmRow[]
  confirmLabel: string
  danger?: boolean
  onConfirm: () => void
}

export function Confirm({ spec, onClose }: { spec: ConfirmSpec; onClose: () => void }) {
  const cancel = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    cancel.current?.focus()
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === 'Escape') {
        ev.preventDefault()
        onClose()
      }
    }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [onClose])

  return (
    <div class="overlay" onMouseDown={(ev) => { if (ev.target === ev.currentTarget) onClose() }}>
      <div class="confirm" role="alertdialog" aria-label={spec.title}>
        <h2 class="confirm-title">{spec.title}</h2>
        {spec.body && <p class="confirm-body">{spec.body}</p>}
        {spec.rows && spec.rows.length > 0 && (
          <ul class="confirm-rows">
            {spec.rows.map((r) => (
              <li key={r.label + (r.detail ?? '')}>
                <span class="confirm-row-label">{r.label}</span>
                {r.detail && <span class="confirm-row-detail">{r.detail}</span>}
              </li>
            ))}
          </ul>
        )}
        <div class="confirm-actions">
          <button type="button" class="btn" ref={cancel} onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            class={'btn' + (spec.danger ? ' danger' : ' primary')}
            onClick={() => {
              onClose()
              spec.onConfirm()
            }}
          >
            {spec.confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
