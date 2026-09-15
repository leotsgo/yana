// One open note: the header, the presence and sync status line, the
// editor, the optional live preview beside it, and the details drawer
// (backlinks, history). The realtime session starts as soon as the id is
// known so the editor is typeable as early as the relay answers.

import { useEffect, useMemo, useRef, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { Note } from './api'
import { fmtBytes, fmtDate } from './dom'
import { Editor } from './editor'
import { HtmlNote } from './htmlnote'
import { backlinksPanel, historyPanel, rewriteRelative, wireWikiLinks } from './panels'
import { SyncClient, presence as localPresence } from './sync'
import type { PresenceState, PresenceUser, SyncStatus } from './sync'

export interface NotePageProps {
  id: string
  preview: boolean
  onTogglePreview: () => void
  onOpen: (id: string) => void
  onNote: (note: Note | null) => void
  onToast: (msg: string) => void
  /** True when the note was just created: focus the editor on open. */
  fresh: boolean
}

export function NotePage({ id, preview, onTogglePreview, onOpen, onNote, onToast, fresh }: NotePageProps) {
  const [note, setNote] = useState<Note | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [sync, setSync] = useState<SyncClient | null>(null)
  const [status, setStatus] = useState<SyncStatus>('connecting')
  const [synced, setSynced] = useState(false)
  const [others, setOthers] = useState<PresenceUser[]>([])
  const [readOnly, setReadOnly] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [details, setDetails] = useState(false)

  // Fetch the note's metadata, then open the realtime session for
  // markdown notes. HTML notes do not merge — no session, no CRDT — so
  // their page never waits for one.
  useEffect(() => {
    let live = true
    setNote(null)
    setError(null)
    setNotice(null)
    setReadOnly(false)
    setSynced(false)
    setOthers([])
    setStatus('connecting')
    let client: SyncClient | null = null
    api
      .note(id)
      .then((n) => {
        if (!live) return
        setNote(n)
        onNote(n)
        document.title = `${n.title} — YANA/`
        if (n.kind !== 'md') return
        client = new SyncClient(id, {
          onStatus(s) {
            if (!live) return
            setStatus(s)
            if (s === 'synced') setSynced(true)
          },
          onError(code) {
            if (!live) return
            if (code === 'rate_limited') setNotice('Typing faster than the server allows; edits are kept and retried.')
            else if (code === 'forbidden') {
              setReadOnly(true)
              setNotice('This space is read-only for your account.')
            }
          },
          onGone(kind, path) {
            if (!live) return
            setNotice(kind === 'moved' ? `This note moved to ${path ?? 'another path'}.` : 'This note was deleted on disk.')
          },
          onPresence() {
            if (!live || !client) return
            const out: PresenceUser[] = []
            for (const [clientID, raw] of client.awareness.getStates()) {
              if (clientID === client.awareness.clientID) continue
              const st = raw as Partial<PresenceState>
              if (st.user) out.push(st.user)
            }
            setOthers(out)
          },
        })
        setSync(client)
      })
      .catch((err: unknown) => {
        if (!live) return
        setError(err instanceof ApiError ? err.message : 'Could not load the note.')
      })
    return () => {
      live = false
      onNote(null)
      setSync(null)
      client?.destroy()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id])

  const me = useMemo(() => localPresence(), [])

  if (error) {
    return (
      <div class="placeholder">
        <p class="error">{error}</p>
      </div>
    )
  }
  if (!note) {
    return <div class="placeholder muted">Opening…</div>
  }
  if (note.kind === 'html') {
    return (
      <article class="page html-page">
        <NoteHeader note={note} />
        <div class="page-body html-body">
          <HtmlNote note={note} onOpen={onOpen} onToast={onToast} />
        </div>
      </article>
    )
  }

  if (!sync) {
    return <div class="placeholder muted">Opening…</div>
  }

  return (
    <article class={'page' + (preview ? ' split' : '') + (details ? ' with-details' : '')}>
      <NoteHeader note={note} />
      <div class="editor-toolbar">
        <div class="presence" aria-label="who is editing">
          <span class="presence-chip" title={`${me.name} (you)`}>
            <span class="presence-dot" style={`background:${me.color}`} />
            {me.name}
            <span class="presence-you">you</span>
          </span>
          {others.map((u, i) => (
            <span key={`${u.name}-${i}`} class="presence-chip" title={u.name}>
              <span class="presence-dot" style={`background:${u.color}`} />
              {u.name}
            </span>
          ))}
        </div>
        <span class={'sync-status ' + status}>{statusLabel(status)}</span>
        {notice && <span class="editor-notice">{notice}</span>}
        <span class="spacer" />
        <button type="button" class={'btn' + (preview ? ' on' : '')} onClick={onTogglePreview} title="Toggle the live preview">
          Preview
        </button>
        <button type="button" class={'btn' + (details ? ' on' : '')} onClick={() => setDetails((d) => !d)} title="Backlinks and history">
          Details
        </button>
      </div>
      <div class="page-body">
        {synced ? (
          <Editor sync={sync} note={note} readOnly={readOnly} autofocus={fresh} onToast={onToast} />
        ) : (
          <div class="editor editor-wait muted">{status === 'offline' ? 'Offline. Waiting for the server.' : 'Connecting…'}</div>
        )}
        {preview && synced && <Preview sync={sync} note={note} onOpen={onOpen} />}
        {details && <Details note={note} onOpen={onOpen} />}
      </div>
    </article>
  )
}

function statusLabel(s: SyncStatus): string {
  switch (s) {
    case 'synced':
      return 'live'
    case 'connecting':
      return 'connecting…'
    case 'offline':
      return 'offline — edits are kept and merge on reconnect'
  }
}

function NoteHeader({ note }: { note: Note }) {
  const parts = note.path.split('/')
  return (
    <header class="note-header">
      <nav class="crumbs" aria-label="path">
        {parts.map((p, i) => (
          <span key={i}>
            {i > 0 && <span class="crumb-sep">/</span>}
            <span class={i === parts.length - 1 ? 'crumb crumb-file' : 'crumb'}>{p}</span>
          </span>
        ))}
      </nav>
      <div class="note-meta">
        <span title="created">{fmtDate(note.created)}</span>
        <span class="meta-sep">·</span>
        <span title="last written to disk">{fmtDate(note.mtime)}</span>
        <span class="meta-sep">·</span>
        <span>{fmtBytes(note.size)}</span>
        {note.tags.length > 0 && <span class="meta-sep">·</span>}
        {note.tags.map((t) => (
          <span key={t} class="tag">
            #{t}
          </span>
        ))}
      </div>
    </header>
  )
}

// The preview renders the live document through the server so it matches
// the read view exactly (same renderer, same wikilink handling). A short
// debounce keeps it from rendering every keystroke.
function Preview({ sync, note, onOpen }: { sync: SyncClient; note: Note; onOpen: (id: string) => void }) {
  const host = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = host.current
    if (!el) return
    let seq = 0
    let timer: number | undefined
    let last = ''
    const render = () => {
      const text = sync.text.toString()
      if (text === last) return
      last = text
      const mine = ++seq
      api
        .render(text)
        .then(({ html }) => {
          if (mine !== seq) return
          el.innerHTML = html
          rewriteRelative(el, note.base)
          wireWikiLinks(el, note, onOpen)
        })
        .catch(() => {
          // Keep the last good render; the editor is still live.
        })
    }
    const schedule = () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(render, 220)
    }
    sync.text.observe(schedule)
    render()
    return () => {
      sync.text.unobserve(schedule)
      window.clearTimeout(timer)
      seq++
    }
  }, [sync, note, onOpen])

  return <div class="preview markdown" ref={host} aria-label="preview" />
}

function Details({ note, onOpen }: { note: Note; onOpen: (id: string) => void }) {
  const host = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = host.current
    if (!el) return
    el.replaceChildren(backlinksPanel(note, onOpen), historyPanel(note, onOpen))
  }, [note, onOpen])
  return <aside class="details" ref={host} aria-label="details" />
}
