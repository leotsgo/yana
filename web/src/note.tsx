// One open note: the title, the toolbar (sync state, presence, view
// switches, overflow), the editor, the optional live preview beside it,
// and the details drawer (path and dates, backlinks, history). The
// realtime session starts as soon as the id is known so the editor is
// typeable as early as the relay answers.
//
// Wide screens show the editor and preview side by side; a phone shows
// one of them. The title is edited in place: it rewrites the note's H1
// and renames the file to match.

import { useEffect, useMemo, useRef, useState } from 'preact/hooks'

import { api, ApiError, baseOf, dirOf } from './api'
import type { MoveResult, Note } from './api'
import { fmtBytes, fmtDate } from './dom'
import { Editor } from './editor'
import { HtmlNote } from './htmlnote'
import { Icon } from './icons'
import type { Layout } from './layout'
import type { MenuSpec } from './menu'
import { backlinksPanel, historyPanel, rewriteRelative, wireWikiLinks } from './panels'
import { SyncClient } from './sync'
import type { PresenceState, PresenceUser, SyncStatus } from './sync'

export interface NotePageProps {
  id: string
  layout: Layout
  preview: boolean
  onTogglePreview: () => void
  onOpen: (id: string) => void
  onNote: (note: Note | null) => void
  onToast: (msg: string) => void
  onMenu: (spec: MenuSpec | null) => void
  /** True when the note was just created: focus the editor on open. */
  fresh: boolean
  onDelete: () => void
  onRename: () => void
  onExport: () => void
  /** The note was renamed from the title; the tree needs a refresh. */
  onMoved: (res: MoveResult) => void
}

type PhoneView = 'edit' | 'preview'

export function NotePage(props: NotePageProps) {
  const { id, layout, preview, onTogglePreview, onOpen, onNote, onToast, onMenu, fresh, onDelete, onRename, onExport, onMoved } = props
  const [note, setNote] = useState<Note | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [sync, setSync] = useState<SyncClient | null>(null)
  const [status, setStatus] = useState<SyncStatus>('connecting')
  const [synced, setSynced] = useState(false)
  const [others, setOthers] = useState<PresenceUser[]>([])
  const [readOnly, setReadOnly] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [details, setDetails] = useState(false)
  const [phoneView, setPhoneView] = useState<PhoneView>('edit')
  const phone = layout === 'phone'
  // A rename from the title moves the file; the relay's "moved" for it is ours.
  const expectMove = useRef<string | null>(null)

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
            if (kind === 'moved' && path && path === expectMove.current) {
              expectMove.current = null
              return
            }
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

  // Escape closes the details drawer when it is an overlay.
  useEffect(() => {
    if (!details || layout === 'desktop') return
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === 'Escape') setDetails(false)
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [details, layout])

  // The title edit: the H1 in the document follows, then the file name.
  function commitTitle(raw: string): void {
    if (!note) return
    const title = raw.replace(/\s+/g, ' ').trim()
    if (title === '' || title === note.title) return
    if (note.kind === 'md' && sync) {
      const text = sync.text.toString()
      const h1 = findHeading(text)
      if (h1) {
        sync.doc.transact(() => {
          sync.text.delete(h1.from, h1.length)
          sync.text.insert(h1.from, title)
        })
      }
    }
    const apply = (n: Note) => {
      setNote(n)
      onNote(n)
      document.title = `${n.title} — YANA/`
    }
    const base = baseOf(note.path)
    const dot = base.lastIndexOf('.')
    const ext = dot > 0 ? base.slice(dot) : ''
    const next = fileName(title) + ext
    if (next === base) {
      apply({ ...note, title })
      return
    }
    const dir = dirOf(note.path)
    const to = dir ? `${dir}/${next}` : next
    expectMove.current = to
    // The index may not have seen the new heading yet; the title we just
    // wrote wins over whatever the move response carries.
    api
      .moveNote(note.id, to)
      .then((res) => {
        apply({ ...res.note, title })
        onMoved(res)
      })
      .catch((err: unknown) => {
        expectMove.current = null
        apply({ ...note })
        onToast(err instanceof ApiError ? err.message : 'Could not rename the file.')
      })
  }

  function openOverflow(anchor: HTMLElement): void {
    onMenu({
      anchor,
      label: 'note actions',
      items: [
        { id: 'rename', label: 'Rename or move', icon: 'move', run: onRename },
        { id: 'export', label: 'Export as HTML', icon: 'download', detail: 'one file', run: onExport },
        'sep',
        { id: 'delete', label: 'Delete', icon: 'trash', detail: 'to the trash', danger: true, run: onDelete },
      ],
    })
  }

  if (error) {
    return (
      <div class="placeholder">
        <Icon name="alert" size={28} class="placeholder-icon" />
        <p class="error">{error}</p>
      </div>
    )
  }
  if (!note) {
    return <div class="placeholder muted">Opening…</div>
  }

  const header = <NoteHeader note={note} onCommit={commitTitle} />
  const detailsPane = details && (
    <Details note={note} onOpen={onOpen} overlay={layout !== 'desktop'} onClose={() => setDetails(false)} />
  )

  if (note.kind === 'html') {
    return (
      <article class={'page html-page' + (details ? ' with-details' : '')}>
        {header}
        <div class="page-body html-body">
          <HtmlNote
            note={note}
            onOpen={onOpen}
            onToast={onToast}
            onMore={openOverflow}
            details={details}
            onToggleDetails={() => setDetails((d) => !d)}
          />
          {detailsPane}
        </div>
      </article>
    )
  }

  if (!sync) {
    return <div class="placeholder muted">Opening…</div>
  }

  const split = !phone && preview
  const showEditor = !phone || phoneView === 'edit'
  const showPreview = phone ? phoneView === 'preview' : preview

  return (
    <article class={'page' + (split ? ' split' : '') + (details ? ' with-details' : '')}>
      {header}
      <div class="editor-toolbar">
        <span class={'sync-status ' + status} title={statusTitle(status)}>
          <span class="sync-dot" />
          <span class="sync-label">{statusLabel(status)}</span>
        </span>
        {others.length > 0 && (
          <div class="presence" aria-label="also editing">
            {others.map((u, i) => (
              <span key={`${u.name}-${i}`} class="presence-chip" title={u.name}>
                <span class="presence-dot" style={`background:${u.color}`} />
                {u.name}
              </span>
            ))}
          </div>
        )}
        {notice && <span class="editor-notice">{notice}</span>}
        <span class="spacer" />
        {phone ? (
          <div class="segmented" role="tablist" aria-label="view">
            <button type="button" role="tab" aria-selected={phoneView === 'edit'} class={phoneView === 'edit' ? 'on' : ''} onClick={() => setPhoneView('edit')}>
              <Icon name="pencil" />
              Edit
            </button>
            <button type="button" role="tab" aria-selected={phoneView === 'preview'} class={phoneView === 'preview' ? 'on' : ''} onClick={() => setPhoneView('preview')}>
              <Icon name="eye" />
              Preview
            </button>
          </div>
        ) : (
          <button type="button" class={'btn' + (preview ? ' on' : '')} onClick={onTogglePreview} title="Show the preview beside the editor" aria-pressed={preview}>
            <Icon name="columns" />
            Preview
          </button>
        )}
        <button type="button" class={'btn' + (details ? ' on' : '')} onClick={() => setDetails((d) => !d)} title="Path, backlinks and history" aria-pressed={details}>
          <Icon name="panel-right" />
          {!phone && 'Details'}
        </button>
        <button type="button" class="icon-btn" title="More" aria-label="More actions" onClick={(ev) => openOverflow(ev.currentTarget as HTMLElement)}>
          <Icon name="more" size={18} />
        </button>
      </div>
      <div class="page-body">
        {showEditor &&
          (synced ? (
            <Editor sync={sync} note={note} readOnly={readOnly} autofocus={fresh} onToast={onToast} />
          ) : (
            <div class="editor editor-wait muted">{status === 'offline' ? 'Offline. Waiting for the server.' : 'Connecting…'}</div>
          ))}
        {showPreview && synced && <Preview sync={sync} note={note} onOpen={onOpen} />}
        {detailsPane}
      </div>
    </article>
  )
}

/** The first H1 in the body, past any frontmatter block. */
function findHeading(text: string): { from: number; length: number } | null {
  let offset = 0
  if (text.startsWith('---\n')) {
    const end = text.indexOf('\n---', 4)
    if (end >= 0) {
      const nl = text.indexOf('\n', end + 1)
      offset = nl < 0 ? text.length : nl + 1
    }
  }
  const rest = text.slice(offset)
  const m = /^(\s*)#[ \t]+([^\n]*?)[ \t]*$/m.exec(rest)
  if (!m || m[2] === undefined) return null
  // Only a heading before any other content counts as the title.
  const before = rest.slice(0, m.index)
  if (before.trim() !== '') return null
  const hashes = /^(\s*)#[ \t]+/.exec(m[0])
  const from = offset + m.index + (hashes ? hashes[0].length : 0)
  return { from, length: m[2].length }
}

/** A file name for a title: the characters a path cannot hold are dropped. */
function fileName(title: string): string {
  return title
    .replace(/[\\/:*?"<>| -]/g, '')
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/^\.+/, '')
    .slice(0, 120) || 'untitled'
}

function statusLabel(s: SyncStatus): string {
  switch (s) {
    case 'synced':
      return 'Live'
    case 'connecting':
      return 'Connecting'
    case 'offline':
      return 'Offline'
  }
}

function statusTitle(s: SyncStatus): string {
  switch (s) {
    case 'synced':
      return 'Edits sync as you type.'
    case 'connecting':
      return 'Waiting for the server.'
    case 'offline':
      return 'Edits are kept here and merge when the server is back.'
  }
}

function NoteHeader({ note, onCommit }: { note: Note; onCommit: (title: string) => void }) {
  const el = useRef<HTMLHeadingElement>(null)
  const dir = dirOf(note.path)

  // The heading is editable text, not an input, so it wraps like a title.
  useEffect(() => {
    const h = el.current
    if (h && h.textContent !== note.title) h.textContent = note.title
  }, [note.title])

  return (
    <header class="note-header">
      {dir && (
        <nav class="crumbs" aria-label="folder">
          <Icon name="folder" />
          {dir.split('/').map((p, i) => (
            <span key={i}>
              {i > 0 && <span class="crumb-sep">/</span>}
              {p}
            </span>
          ))}
        </nav>
      )}
      <h1
        ref={el}
        class="note-title"
        contentEditable={'plaintext-only' as unknown as boolean}
        spellcheck={false}
        role="textbox"
        aria-label="Title"
        onKeyDown={(ev) => {
          if (ev.key === 'Enter') {
            ev.preventDefault()
            ;(ev.currentTarget as HTMLElement).blur()
          } else if (ev.key === 'Escape') {
            ev.preventDefault()
            const h = ev.currentTarget as HTMLElement
            h.textContent = note.title
            h.blur()
          }
        }}
        onPaste={(ev) => {
          ev.preventDefault()
          const t = ev.clipboardData?.getData('text/plain') ?? ''
          document.execCommand('insertText', false, t.replace(/\s+/g, ' '))
        }}
        onBlur={(ev) => {
          const h = ev.currentTarget as HTMLElement
          const v = h.textContent ?? ''
          if (v.trim() === '') h.textContent = note.title
          else onCommit(v)
        }}
      />
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

interface DetailsProps {
  note: Note
  onOpen: (id: string) => void
  overlay: boolean
  onClose: () => void
}

function Details({ note, onOpen, overlay, onClose }: DetailsProps) {
  const host = useRef<HTMLDivElement>(null)
  const me = useMemo(() => note, [note])
  useEffect(() => {
    const el = host.current
    if (!el) return
    el.replaceChildren(backlinksPanel(me, onOpen), historyPanel(me, onOpen))
  }, [me, onOpen])

  const body = (
    <aside class="details" aria-label="details">
      <div class="details-head">
        <h2 class="panel-title">Details</h2>
        {overlay && (
          <button type="button" class="icon-btn" aria-label="Close details" onClick={onClose}>
            <Icon name="x" size={18} />
          </button>
        )}
      </div>
      <dl class="meta">
        <dt>Path</dt>
        <dd class="mono">{note.path}</dd>
        <dt>Created</dt>
        <dd>{fmtDate(note.created)}</dd>
        <dt>Modified</dt>
        <dd>{fmtDate(note.mtime)}</dd>
        <dt>Size</dt>
        <dd>{fmtBytes(note.size)}</dd>
        {note.tags.length > 0 && (
          <>
            <dt>Tags</dt>
            <dd class="tags">
              {note.tags.map((t) => (
                <span key={t} class="tag">
                  #{t}
                </span>
              ))}
            </dd>
          </>
        )}
      </dl>
      <div ref={host} />
    </aside>
  )

  if (!overlay) return body
  return (
    <div class="details-layer">
      <div class="scrim" onClick={onClose} />
      {body}
    </div>
  )
}
