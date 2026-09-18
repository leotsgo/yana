// Sharing one note by link. The dialog behind "Share a link": it makes
// the note's public link (or shows the one it has), with the address, a
// copy button, the phone's share sheet when the browser has one, a QR
// code drawn on the spot, the expiry, and Revoke. The link is index
// state on the server; the tree and the title carry a globe while it is
// live.

import { useEffect, useRef, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { LinkExpiry, PublicLink } from './api'
import { fmtDate } from './dom'
import { Icon } from './icons'

export interface ShareLinkProps {
  noteID: string
  title: string
  onClose: () => void
  onToast: (msg: string) => void
  /** The link was made, changed or revoked: the globe marks need a refresh. */
  onChanged: (live: boolean) => void
}

const EXPIRIES: Array<{ id: LinkExpiry; label: string }> = [
  { id: '1d', label: '1 day' },
  { id: '1w', label: '1 week' },
  { id: 'never', label: 'Never' },
]

/** Which expiry choice a link's expires_at is closest to. */
function expiryOf(link: PublicLink): LinkExpiry {
  if (!link.expires_at) return 'never'
  const left = new Date(link.expires_at).getTime() - Date.now()
  return left <= 24 * 3600 * 1000 ? '1d' : '1w'
}

export function msgOf(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback
}

export async function copyText(text: string, say: (m: string) => void, what: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    say(`Copied ${what}.`)
  } catch {
    say('The browser refused the clipboard. Select the text and copy it.')
  }
}

/** The Web Share API, when the browser has one (phones, mostly). */
export function canShare(): boolean {
  return typeof navigator !== 'undefined' && typeof navigator.share === 'function'
}

export function ShareLinkDialog({ noteID, title, onClose, onToast, onChanged }: ShareLinkProps) {
  const [link, setLink] = useState<PublicLink | null | undefined>(undefined)
  const [expiry, setExpiry] = useState<LinkExpiry>('never')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const first = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    let alive = true
    api
      .publicLink(noteID)
      .then((r) => {
        if (!alive) return
        setLink(r.link)
        if (r.link) setExpiry(expiryOf(r.link))
      })
      .catch((err: unknown) => {
        if (alive) setError(msgOf(err, 'Could not read the link.'))
      })
    return () => {
      alive = false
    }
  }, [noteID])

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

  useEffect(() => {
    if (link !== undefined) first.current?.focus()
  }, [link === undefined])

  const create = () => {
    setBusy(true)
    api
      .createPublicLink(noteID, expiry)
      .then((r) => {
        setLink(r.link)
        setExpiry(expiryOf(r.link))
        onChanged(true)
        onToast(r.created ? 'The note has a public link.' : 'The note already had a link.')
      })
      .catch((err: unknown) => onToast(msgOf(err, 'Could not make the link.')))
      .finally(() => setBusy(false))
  }

  const changeExpiry = (e: LinkExpiry) => {
    setExpiry(e)
    if (!link) return
    setBusy(true)
    api
      .setPublicLinkExpiry(noteID, e)
      .then((r) => setLink(r.link))
      .catch((err: unknown) => onToast(msgOf(err, 'Could not change the expiry.')))
      .finally(() => setBusy(false))
  }

  const revoke = () => {
    setBusy(true)
    api
      .revokePublicLink(noteID)
      .then(() => {
        setLink(null)
        onChanged(false)
        onToast('The link is revoked. It opens nothing now.')
      })
      .catch((err: unknown) => onToast(msgOf(err, 'Could not revoke the link.')))
      .finally(() => setBusy(false))
  }

  const share = () => {
    if (!link) return
    navigator.share({ title, url: link.url }).catch(() => {
      /* the sheet was dismissed */
    })
  }

  return (
    <div class="overlay" onMouseDown={(ev) => { if (ev.target === ev.currentTarget) onClose() }}>
      <div class="confirm share-dialog" role="dialog" aria-label="Share a link">
        <div class="share-head">
          <h2 class="confirm-title">Share a link</h2>
          <button type="button" class="icon-btn" aria-label="Close" onClick={onClose}>
            <Icon name="x" size={18} />
          </button>
        </div>
        {error ? (
          <p class="confirm-body error">{error}</p>
        ) : link === undefined ? (
          <p class="confirm-body muted">Looking…</p>
        ) : link === null ? (
          <>
            <p class="confirm-body">
              Anyone with the link reads <b>{title || 'this note'}</b> and its pictures, with no account. The link says nothing about where the note lives, and it stops working the moment you revoke it.
            </p>
            <div class="share-expiry">
              <span class="share-expiry-label">Stops working after</span>
              <ExpiryPicker value={expiry} onChange={setExpiry} disabled={busy} />
            </div>
            <div class="confirm-actions">
              <button type="button" class="btn" onClick={onClose}>
                Cancel
              </button>
              <button type="button" class="btn primary" ref={first} disabled={busy} onClick={create}>
                <Icon name="globe" />
                Make the link
              </button>
            </div>
          </>
        ) : (
          <>
            <p class="confirm-body">
              Anyone with this link reads <b>{title || 'this note'}</b> and its pictures, with no account.
            </p>
            <div class="copy-row share-url">
              <code class="copy-value">{link.url}</code>
              <button type="button" class="btn" ref={first} onClick={() => void copyText(link.url, onToast, 'the link')}>
                <Icon name="copy" />
                Copy
              </button>
              {canShare() && (
                <button type="button" class="btn" onClick={share}>
                  <Icon name="share" />
                  Share
                </button>
              )}
            </div>
            <div class="share-body">
              <QRCode text={link.url} />
              <dl class="meta facts share-facts">
                <dt>Made</dt>
                <dd>{fmtDate(link.created_at)}</dd>
                <dt>Stops</dt>
                <dd>{link.expires_at ? fmtDate(link.expires_at) : 'when you revoke it'}</dd>
              </dl>
            </div>
            <div class="share-expiry">
              <span class="share-expiry-label">Stops working after</span>
              <ExpiryPicker value={expiry} onChange={changeExpiry} disabled={busy} />
            </div>
            <div class="confirm-actions">
              <button type="button" class="btn danger" disabled={busy} onClick={revoke}>
                <Icon name="unlink" />
                Revoke
              </button>
              <span class="spacer" />
              <button type="button" class="btn" onClick={onClose}>
                Done
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

function ExpiryPicker({ value, onChange, disabled }: { value: LinkExpiry; onChange: (e: LinkExpiry) => void; disabled: boolean }) {
  return (
    <div class="segmented" role="radiogroup" aria-label="Stops working after">
      {EXPIRIES.map((e) => (
        <button
          key={e.id}
          type="button"
          class={value === e.id ? 'on' : ''}
          role="radio"
          aria-checked={value === e.id}
          disabled={disabled}
          onClick={() => onChange(e.id)}
        >
          {e.label}
        </button>
      ))}
    </div>
  )
}

/** The link as a QR code, drawn on the spot as an SVG so a phone can
 * scan it off the screen. The encoder is its own chunk, fetched the
 * first time a link is shown. */
function QRCode({ text }: { text: string }) {
  const [svg, setSvg] = useState<string | null>(null)
  useEffect(() => {
    let alive = true
    import('qrcode-generator')
      .then((m) => {
        if (!alive) return
        const qr = m.default(0, 'M')
        qr.addData(text)
        qr.make()
        setSvg(qr.createSvgTag({ cellSize: 4, margin: 0, scalable: true }))
      })
      .catch(() => {
        if (alive) setSvg('')
      })
    return () => {
      alive = false
    }
  }, [text])
  if (svg === null) return <div class="share-qr" aria-hidden="true" />
  if (svg === '') return null
  return <div class="share-qr" role="img" aria-label="The link as a QR code" dangerouslySetInnerHTML={{ __html: svg }} />
}
