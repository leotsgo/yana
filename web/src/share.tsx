// The share target. The manifest sends shares here as GET /share with
// the shared title, text and URL in the query string. The page adds one
// markdown line to today's daily note — or to a note picked from the
// tree — through the note's CRDT document, and confirms. Offline, the
// share goes to the outbox and lands when the connection returns. iOS
// has no share targets; docs/pwa.md carries a Shortcut that posts to the
// API instead.

import { useEffect, useState } from 'preact/hooks'

import { api } from './api'
import * as cache from './cache'
import { Icon } from './icons'
import { enqueueShare } from './outbox'
import { Palette } from './palette'
import type { PaletteSpec } from './palette'
import { appendToNote, composeShareBlock, today } from './sharelib'

interface ShareNote {
  id: string
  path: string
  title: string
}

export interface SharePageProps {
  notes: ShareNote[]
  /** The space today's daily note goes to, from the settings. */
  dailySpace: string
  onOpen: (id: string) => void
  onToast: (msg: string) => void
  /** The query the share arrived with (title, text, url). */
  search: string
}

export function SharePage({ notes, dailySpace, onOpen, onToast, search }: SharePageProps) {
  const params = new URLSearchParams(search)
  const [title, setTitle] = useState(params.get('title') ?? '')
  const [text, setText] = useState(params.get('text') ?? '')
  const [url, setUrl] = useState(params.get('url') ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<{ path: string; id: string } | null>(null)
  const [queued, setQueued] = useState(false)
  const [picking, setPicking] = useState<PaletteSpec | null>(null)

  useEffect(() => {
    document.title = 'Share — YANA/'
  }, [])

  const block = composeShareBlock(title, text, url)
  const hasShare = title.trim() !== '' || text.trim() !== '' || url.trim() !== ''

  async function addDaily(): Promise<void> {
    setBusy(true)
    setError(null)
    try {
      const res = await api.daily(dailySpace, today())
      await appendToNote(res.id, block)
      setDone({ path: res.path, id: res.id })
    } catch (err) {
      if (cache.networkDown(err)) {
        enqueueShare(dailySpace, title, text, url)
        setQueued(true)
      } else {
        setError(err instanceof Error ? err.message : 'Could not add the share.')
      }
    } finally {
      setBusy(false)
    }
  }

  async function addNote(id: string, path: string): Promise<void> {
    setBusy(true)
    setError(null)
    try {
      const res = await appendToNote(id, block)
      if (res.offline) onToast('Added on this device. It merges when the server is back.')
      setDone({ path, id })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not add the share.')
    } finally {
      setBusy(false)
    }
  }

  function chooseNote(): void {
    setPicking({
      mode: 'list',
      placeholder: 'Add to which note',
      items: notes.map((n) => ({
        id: n.id,
        label: n.title || n.path,
        detail: n.path,
        run: () => void addNote(n.id, n.path),
      })),
    })
  }

  if (done) {
    return (
      <div class="home share-page">
        <div class="home-mark" aria-hidden="true">
          <Icon name="check" size={56} />
        </div>
        <h1 class="home-title">Added</h1>
        <p class="home-sub">The share is in {done.path}.</p>
        <div class="home-actions">
          <button type="button" class="btn primary large" onClick={() => onOpen(done.id)}>
            <Icon name="book-open" size={18} />
            Open the note
          </button>
        </div>
      </div>
    )
  }

  if (queued) {
    return (
      <div class="home share-page">
        <div class="home-mark" aria-hidden="true">
          <Icon name="share" size={56} />
        </div>
        <h1 class="home-title">Queued</h1>
        <p class="home-sub">The server is unreachable. The share lands in today's note when the connection returns.</p>
      </div>
    )
  }

  return (
    <div class="home share-page">
      <div class="home-mark" aria-hidden="true">
        <Icon name="share" size={56} />
      </div>
      <h1 class="home-title">Share</h1>
      {!hasShare ? (
        <p class="home-sub">Nothing came with this share. Copy it here and it goes to today's note.</p>
      ) : (
        <p class="home-sub share-preview mono">{block}</p>
      )}
      <div class="home-actions">
        <button type="button" class="btn primary large" disabled={!hasShare || busy} onClick={() => void addDaily()}>
          <Icon name="calendar" size={18} />
          {busy ? 'Adding…' : "Add to today's note"}
        </button>
        <button type="button" class="btn large" disabled={!hasShare || busy} onClick={chooseNote}>
          <Icon name="folder" size={18} />
          Choose a note
        </button>
      </div>
      {!hasShare && (
        <textarea
          class="share-paste"
          placeholder="Paste what you meant to share"
          onInput={(ev) => {
            const v = (ev.target as HTMLTextAreaElement).value
            setText(v)
            if (/https?:\/\/\S+/.test(v)) setUrl(v.match(/https?:\/\/\S+/)?.[0] ?? '')
          }}
        />
      )}
      {error && <p class="error">{error}</p>}
      {picking && <Palette spec={picking} onClose={() => setPicking(null)} />}
    </div>
  )
}
