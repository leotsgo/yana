// One open note: the title, the toolbar (sync state, presence, view
// switches, overflow), the body in one of three modes, and the details
// drawer (path and dates, backlinks, history). The realtime session
// starts as soon as the id is known so the editor is typeable as early
// as the relay answers.
//
// Read shows the rendered note; its task boxes write back through the
// CRDT. Edit is the source editor, with a formatting bar above the
// keyboard on a phone. Split puts both side by side and is a wide-screen
// thing; a phone in split mode reads. The title is edited in place: it
// rewrites the note's H1 and renames the file to match.

import { useEffect, useMemo, useRef, useState } from 'preact/hooks'
import type { EditorView } from '@codemirror/view'

import { api, ApiError, dirOf } from './api'
import type { MoveResult, Note } from './api'
import * as cache from './cache'
import { fmtBytes, fmtDate } from './dom'
import { Editor } from './editor'
import { FormatBar } from './format'
import type { Completions } from './editor'
import { isEditable } from './hotkeys'
import { HtmlNote } from './htmlnote'
import { Icon } from './icons'
import { coarsePointer } from './layout'
import type { Layout } from './layout'
import type { MenuSpec } from './menu'
import { backlinksPanel, historyPanel, rewriteRelative, wireWikiLinks } from './panels'
import { resolveTitle } from './paths'
import type { OpenMode } from './prefs'
import { SyncClient, presence } from './sync'
import type { PresenceState, PresenceUser, SyncStatus } from './sync'

export interface NotePageProps {
  id: string
  layout: Layout
  mode: OpenMode
  onMode: (m: OpenMode) => void
  /** Hide markdown syntax on the lines the caret is not on. */
  live: boolean
  /** The page is showing the editor on a phone; the shell swaps its bottom bar for the formatting bar. */
  onEditing: (editing: boolean) => void
  onOpen: (id: string) => void
  onNote: (note: Note | null) => void
  onToast: (msg: string) => void
  onMenu: (spec: MenuSpec | null) => void
  /** True when the note was just created: focus the title, then the
   * editor once the title is committed. */
  fresh: boolean
  /** Goes up each time the title is asked for again on the same note. */
  freshSeq: number
  onDelete: () => void
  onRename: () => void
  /** Pick a folder for the note: the crumbs, the overflow menu, the phone's way to move one. */
  onMove: () => void
  /** True when a folder exists in the tree; the title hint says when it will be made. */
  hasDir: (path: string) => boolean
  onExport: () => void
  /** The note was renamed from the title; the tree needs a refresh. */
  onMoved: (res: MoveResult) => void
  /** Open the page for a tag. */
  onTag: (tag: string) => void
  pinned: boolean
  onPin: () => void
  /** Text to scroll into view once the note renders (a search hit). */
  highlight: string | null
  /** The notes and tags the editor offers after `[[` and `#`, for a space. */
  lookup: (space: string) => Completions
}

export function NotePage(props: NotePageProps) {
  const { id, layout, mode, onMode, live, onEditing, onOpen, onNote, onToast, onMenu, fresh, freshSeq, onDelete, onRename, onMove, hasDir, onExport, onMoved, onTag, pinned, onPin, highlight, lookup } = props
  const [note, setNote] = useState<Note | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [sync, setSync] = useState<SyncClient | null>(null)
  const [status, setStatus] = useState<SyncStatus>('connecting')
  const [synced, setSynced] = useState(false)
  const [local, setLocal] = useState(false) // the local document copy is loaded
  const [others, setOthers] = useState<PresenceUser[]>([])
  const [readOnly, setReadOnly] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [details, setDetails] = useState(false)
  const [view, setView] = useState<EditorView | null>(null)
  // A fresh note starts in the title; Enter there hands focus to the editor.
  // A double-click in the tree asks for the title again on an open note.
  const [titleDone, setTitleDone] = useState(!fresh)
  useEffect(() => {
    if (fresh) setTitleDone(false)
  }, [fresh, freshSeq])
  // A search hit stays marked while reading; editing clears it.
  const [hitShown, setHitShown] = useState(highlight)
  const phone = layout === 'phone'
  // Split needs the width; a phone in split mode reads.
  const shown: OpenMode = phone && mode === 'split' ? 'read' : mode
  const editing = shown !== 'read'
  // A rename from the title moves the file; the relay's "moved" for it is ours.
  const expectMove = useRef<string | null>(null)

  // Fetch the note's metadata, then open the realtime session for
  // markdown notes. HTML notes do not merge — no session, no CRDT — so
  // their page never waits for one. Offline, the metadata and the last
  // render come from the read cache and the session runs on the local
  // document, which y-indexeddb has already loaded.
  useEffect(() => {
    let alive = true
    setNote(null)
    setError(null)
    setNotice(null)
    setReadOnly(false)
    setSynced(false)
    setLocal(false)
    setOthers([])
    setStatus('connecting')
    let client: SyncClient | null = null
    api
      .note(id)
      .then((n) => {
        if (!alive) return
        void cache.putNote(n)
        return n
      })
      .catch(async (err: unknown) => {
        if (!alive || !cache.networkDown(err)) throw err
        const cached = await cache.getNote(id)
        if (!cached) throw err
        return cached
      })
      .then((n) => {
        if (!alive || !n) return
        setNote(n)
        onNote(n)
        document.title = `${n.title} — YANA/`
        // A viewer reads: no pencil, no formatting bar, no title edit.
        if (n.role === 'viewer') setReadOnly(true)
        if (n.kind !== 'md') return
        client = new SyncClient(id, {
          onStatus(s) {
            if (!alive) return
            setStatus(s)
            if (s === 'synced') setSynced(true)
          },
          onLocal() {
            if (alive) setLocal(true)
          },
          onError(code) {
            if (!alive) return
            if (code === 'rate_limited') setNotice('Typing faster than the server allows; edits are kept and retried.')
            else if (code === 'forbidden') {
              setReadOnly(true)
              setNotice('This space is read-only for your account.')
            }
          },
          onGone(kind, path) {
            if (!alive) return
            if (kind === 'moved' && path && path === expectMove.current) {
              expectMove.current = null
              return
            }
            setNotice(kind === 'moved' ? `This note moved to ${path ?? 'another path'}.` : 'This note was deleted on disk.')
          },
          onPresence() {
            if (!alive || !client) return
            // Someone else: not this tab, and not this account in
            // another one. One chip per name however many tabs they have.
            const out: PresenceUser[] = []
            const seen = new Set([presence().name])
            for (const [clientID, raw] of client.awareness.getStates()) {
              if (clientID === client.awareness.clientID) continue
              const st = raw as Partial<PresenceState>
              if (!st.user || seen.has(st.user.name)) continue
              seen.add(st.user.name)
              out.push(st.user)
            }
            setOthers(out)
          },
        })
        setSync(client)
      })
      .catch((err: unknown) => {
        if (!alive) return
        setError(err instanceof ApiError ? err.message : 'Could not load the note.')
      })
    return () => {
      alive = false
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

  // In the read view a bare `e` opens the editor; in the editor, Escape
  // goes back (the editor's own keymap handles it while it has focus).
  useEffect(() => {
    if (note?.kind !== 'md') return
    const onKey = (ev: KeyboardEvent) => {
      if (isEditable(document.activeElement)) return
      if (shown === 'read' && ev.key === 'e' && !ev.ctrlKey && !ev.metaKey && !ev.altKey && !ev.shiftKey) {
        ev.preventDefault()
        if (!readOnly) onMode('edit')
      } else if (shown === 'edit' && ev.key === 'Escape') {
        onMode('read')
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [shown, note?.kind, onMode, readOnly])

  useEffect(() => {
    if (shown === 'edit') setHitShown(null)
  }, [shown])

  // The shell needs to know when the phone keyboard is the point.
  const md = note?.kind === 'md'
  useEffect(() => {
    onEditing(phone && md && shown === 'edit')
    return () => onEditing(false)
  }, [phone, md, shown, onEditing])

  // The title edit: the H1 in the document follows, then the file name.
  // Slashes in the title place the note: `projects/kiln` moves it into
  // projects/ (made if it is not there) and calls it kiln.
  function commitTitle(raw: string): void {
    if (!note) return
    const { title, path: to } = resolveTitle(note.path, note.title, raw)
    if (title === '' || (title === note.title && to === note.path)) return
    if (title !== note.title && note.kind === 'md' && sync) {
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
    if (to === note.path) {
      apply({ ...note, title })
      return
    }
    expectMove.current = to
    // The index may not have seen the new heading yet; the title we just
    // wrote wins over whatever the move response carries. The response
    // is the bare index row: tags, links, base and role carry over.
    api
      .moveNote(note.id, to)
      .then((res) => {
        apply({ ...note, ...res.note, title })
        onMoved(res)
      })
      .catch((err: unknown) => {
        expectMove.current = null
        apply({ ...note, title })
        onToast(err instanceof ApiError ? err.message : 'Could not rename the file.')
      })
  }

  function openOverflow(anchor: HTMLElement): void {
    const viewer = note?.role === 'viewer'
    onMenu({
      anchor,
      label: 'note actions',
      items: viewer
        ? [
            { id: 'pin', label: pinned ? 'Unpin from the sidebar' : 'Pin to the sidebar', icon: pinned ? 'pin-off' : 'pin', run: onPin },
            { id: 'export', label: 'Export as HTML', icon: 'download', detail: 'one file', run: onExport },
          ]
        : [
            { id: 'pin', label: pinned ? 'Unpin from the sidebar' : 'Pin to the sidebar', icon: pinned ? 'pin-off' : 'pin', run: onPin },
            { id: 'move', label: 'Move to a folder', icon: 'move', run: onMove },
            { id: 'rename', label: 'Rename or move by path', icon: 'pencil', run: onRename },
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

  const header = (
    <NoteHeader
      note={note}
      onCommit={commitTitle}
      readOnly={note.role === 'viewer'}
      autofocus={fresh && !titleDone}
      onNext={() => setTitleDone(true)}
      onTag={onTag}
      onLocation={onMove}
      hasDir={hasDir}
    />
  )
  const detailsPane = details && (
    <Details note={note} onOpen={onOpen} overlay={layout !== 'desktop'} onClose={() => setDetails(false)} onRename={onRename} />
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

  const split = shown === 'split'
  // The editor opens once the document is in step with the server, or —
  // offline — once the local copy has loaded; edits are kept either way.
  const docReady = synced || (local && status === 'offline')
  const modeBtn = (m: OpenMode, icon: 'book-open' | 'pencil' | 'columns', text: string, title: string) => (
    <button type="button" role="tab" aria-selected={shown === m} class={shown === m ? 'on' : ''} title={title} onClick={() => onMode(m)}>
      <Icon name={icon} />
      {text}
    </button>
  )

  return (
    <article class={'page' + (split ? ' split' : '') + (details ? ' with-details' : '') + (editing ? ' editing' : ' reading')}>
      {header}
      <div class="editor-toolbar">
        <span class={'sync-status ' + status} title={statusTitle(status)}>
          <span class="sync-dot" />
          <span class="sync-label">{statusLabel(status)}</span>
        </span>
        {others.length > 0 && (
          <div class="presence" aria-label="also here">
            {others.map((u, i) => (
              <span key={`${u.name}-${i}`} class="presence-chip" title={`${u.name} has this note open`}>
                <span class="presence-dot" style={`background:${u.color}`} />
                {u.name}
              </span>
            ))}
          </div>
        )}
        {notice && <span class="editor-notice">{notice}</span>}
        {!phone && editing && !readOnly && note.role !== 'viewer' && <FormatBar view={view} note={note} onToast={onToast} compact />}
        <span class="spacer" />
        {phone ? (
          editing ? (
            <button type="button" class="btn primary" onClick={() => onMode('read')} title="Back to reading (Esc)">
              <Icon name="check" />
              Done
            </button>
          ) : note.role === 'viewer' ? (
            <span class="role-badge" title="Your account reads this space">
              <Icon name="eye" />
              Viewer
            </span>
          ) : (
            <button type="button" class="btn" onClick={() => onMode('edit')} title="Edit (E)" disabled={readOnly}>
              <Icon name="pencil" />
              Edit
            </button>
          )
        ) : note.role === 'viewer' ? (
          <span class="role-badge" title="Your account reads this space; an editor role would let you write">
            <Icon name="eye" />
            Viewer
          </span>
        ) : (
          <div class="segmented" role="tablist" aria-label="view">
            {modeBtn('read', 'book-open', 'Read', 'Read (Esc)')}
            {modeBtn('edit', 'pencil', 'Edit', 'Edit (E)')}
            {modeBtn('split', 'columns', 'Split', 'Editor and preview side by side')}
          </div>
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
        {editing &&
          (docReady ? (
            <Editor
              sync={sync}
              note={note}
              lookup={() => lookup(note.space)}
              readOnly={readOnly}
              autofocus={fresh ? titleDone : shown === 'edit'}
              atEnd={fresh || phone}
              phone={phone}
              live={live}
              onToast={onToast}
              onDone={() => onMode('read')}
              onView={setView}
            />
          ) : (
            <div class="editor editor-wait muted">{status === 'offline' ? 'Offline. Opening the copy on this device.' : 'Connecting…'}</div>
          ))}
        {shown !== 'edit' && (
          <Reader
            html={note.html ?? ''}
            sync={synced ? sync : null}
            note={note}
            readOnly={readOnly}
            cls={split ? 'preview' : 'reader'}
            onOpen={onOpen}
            onTag={onTag}
            highlight={hitShown}
            onEdit={!split && layout === 'desktop' && !coarsePointer && !readOnly ? () => onMode('edit') : undefined}
          />
        )}
        {detailsPane}
      </div>
      {phone && shown === 'edit' && !readOnly && <FormatBar view={view} note={note} onToast={onToast} />}
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

interface NoteHeaderProps {
  note: Note
  onCommit: (title: string) => void
  readOnly: boolean
  /** Focus the title with its text selected: a new note is named first. */
  autofocus: boolean
  /** Enter in the title: the body is next. */
  onNext: () => void
  onTag: (tag: string) => void
  /** The crumbs are a button: pick another folder for the note. */
  onLocation: () => void
  hasDir: (path: string) => boolean
}

function NoteHeader({ note, onCommit, readOnly, autofocus, onNext, onTag, onLocation, hasDir }: NoteHeaderProps) {
  const el = useRef<HTMLHeadingElement>(null)
  const dir = dirOf(note.path)
  // What the title says while it is being typed; a slash in it places
  // the note, and the hint under the title says where.
  const [draft, setDraft] = useState<string | null>(null)

  // The heading is editable text, not an input, so it wraps like a title.
  useEffect(() => {
    const h = el.current
    if (h && h.textContent !== note.title) h.textContent = note.title
  }, [note.title])

  useEffect(() => {
    const h = el.current
    if (!h || !autofocus) return
    h.focus()
    const range = document.createRange()
    range.selectNodeContents(h)
    const sel = window.getSelection()
    sel?.removeAllRanges()
    sel?.addRange(range)
  }, [autofocus])

  const target = draft !== null && draft.includes('/') ? resolveTitle(note.path, note.title, draft) : null
  const crumbs = dir === '' ? [] : dir.split('/')

  return (
    <header class="note-header">
      <nav class="crumbs" aria-label="folder">
        <button
          type="button"
          class="crumbs-btn"
          title={readOnly ? 'The folder this note is in' : 'Move to another folder'}
          disabled={readOnly}
          onClick={onLocation}
        >
          <Icon name="folder" />
          {crumbs.length === 0 ? (
            <span>/</span>
          ) : (
            crumbs.map((p, i) => (
              <span key={i}>
                {i > 0 && <span class="crumb-sep">/</span>}
                {p}
              </span>
            ))
          )}
          {!readOnly && <Icon name="chevron-down" class="crumbs-caret" size={12} />}
        </button>
      </nav>
      <h1
        ref={el}
        class={'note-title' + (readOnly ? ' static' : '')}
        contentEditable={readOnly ? false : ('plaintext-only' as unknown as boolean)}
        spellcheck={false}
        role={readOnly ? undefined : 'textbox'}
        aria-label="Title"
        onInput={(ev) => setDraft((ev.currentTarget as HTMLElement).textContent ?? '')}
        onKeyDown={(ev) => {
          if (ev.key === 'Enter') {
            ev.preventDefault()
            ;(ev.currentTarget as HTMLElement).blur()
            onNext()
          } else if (ev.key === 'Escape') {
            ev.preventDefault()
            const h = ev.currentTarget as HTMLElement
            h.textContent = note.title
            setDraft(null)
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
          setDraft(null)
          if (v.trim() === '') {
            h.textContent = note.title
            return
          }
          // The heading shows the name, not the path that placed it.
          h.textContent = resolveTitle(note.path, note.title, v).title
          onCommit(v)
        }}
      />
      {target && (
        <p class="title-hint" aria-live="polite">
          <Icon name={target.moves ? 'move' : 'folder'} size={13} />
          {target.moves ? (
            <>
              Moves to <b>{target.dir || '/'}</b>
              {target.dir && !hasDir(target.dir) && <span class="title-hint-new">new folder</span>}
              {' '}as <b>{target.title || 'Untitled'}</b>
            </>
          ) : (
            <>
              Stays in <b>{dir || '/'}</b> as <b>{target.title || 'Untitled'}</b>
            </>
          )}
        </p>
      )}
      {note.tags.length > 0 && (
        <div class="note-tags" aria-label="tags">
          {note.tags.map((t) => (
            <a key={t} class="tag" href={`/tags/${encodeURIComponent(t)}`} onClick={(ev) => { ev.preventDefault(); onTag(t) }}>
              #{t}
            </a>
          ))}
        </div>
      )}
    </header>
  )
}

interface ReaderProps {
  /** The render that came with the note; shown until the live one lands. */
  html: string
  sync: SyncClient | null
  note: Note
  readOnly: boolean
  cls: 'reader' | 'preview'
  onOpen: (id: string) => void
  onTag: (tag: string) => void
  /** Text to scroll to and mark on the first render. */
  highlight: string | null
  /** A click on the body (not a link or a box) opens the editor. */
  onEdit?: () => void
}

// The rendered note. It renders the live document through the server so
// it matches every other render (same goldmark, same wikilink handling);
// a short debounce keeps it from rendering every keystroke. Task boxes
// carry the line their marker is on and flip it through the CRDT.
function Reader({ html, sync, note, readOnly, cls, onOpen, onTag, highlight, onEdit }: ReaderProps) {
  const host = useRef<HTMLDivElement>(null)
  // The search hit is marked on every render (the live one replaces the
  // first) and scrolled to once, on whichever render lands first.
  const scrolled = useRef(false)

  const wire = (el: HTMLElement) => {
    rewriteRelative(el, note.base)
    wireWikiLinks(el, note, onOpen)
    wireTags(el, onTag)
    if (highlight && markText(el, highlight, !scrolled.current)) scrolled.current = true
  }

  useEffect(() => {
    const el = host.current
    if (!el || sync) return
    el.innerHTML = html
    wire(el)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [html, sync, note, onOpen])

  useEffect(() => {
    const el = host.current
    if (!el || !sync) return
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
          wire(el)
          if (!readOnly) wireTasks(el, sync, () => { last = ''; render() })
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sync, note, readOnly, onOpen])

  return (
    <div
      class={cls + ' markdown'}
      ref={host}
      aria-label={cls === 'preview' ? 'preview' : 'note'}
      onClick={
        onEdit &&
        ((ev) => {
          const t = ev.target as HTMLElement
          if (t.closest('a, input, button, summary, .wikilink, .tag')) return
          // A drag to select text is not a request to edit.
          if (!(window.getSelection()?.isCollapsed ?? true)) return
          onEdit()
        })
      }
    />
  )
}

/** Inline #tags render as spans carrying the tag; a click opens its page. */
function wireTags(el: HTMLElement, onTag: (tag: string) => void): void {
  for (const span of el.querySelectorAll<HTMLElement>('span.tag[data-tag]')) {
    const tag = span.dataset['tag'] ?? ''
    if (!tag) continue
    span.setAttribute('role', 'link')
    span.setAttribute('tabindex', '0')
    span.title = `Notes tagged #${tag}`
    const go = () => onTag(tag)
    span.addEventListener('click', go)
    span.addEventListener('keydown', (ev) => {
      if (ev.key === 'Enter' || ev.key === ' ') {
        ev.preventDefault()
        go()
      }
    })
  }
}

/** Wraps the first occurrence of needle in the rendered text in a mark
 * and, when asked, scrolls it into view. Matching is case-insensitive
 * and stays inside one text node, which is where a search snippet's
 * words sit. Returns whether anything was found. */
function markText(el: HTMLElement, needle: string, scroll: boolean): boolean {
  const q = needle.trim().toLowerCase()
  if (!q) return false
  const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT)
  // The whole phrase first, then its first word, so a snippet that
  // spans a line break still lands near the right place.
  for (const want of [q, q.split(/\s+/)[0] ?? q]) {
    if (!want) continue
    walker.currentNode = el
    let node: Node | null
    while ((node = walker.nextNode())) {
      const text = node as Text
      if (text.parentElement?.closest('pre, code') && want !== q) continue
      const i = text.data.toLowerCase().indexOf(want)
      if (i < 0) continue
      const hit = text.splitText(i)
      hit.splitText(want.length)
      const mark = document.createElement('mark')
      mark.className = 'hit-mark'
      hit.replaceWith(mark)
      mark.append(hit)
      if (scroll) mark.scrollIntoView({ block: 'center' })
      return true
    }
  }
  return false
}

/** Lines taken by a frontmatter block at the top of the text, 0 when
 * there is none. Mirrors the server's rule: the first line is exactly
 * `---`, the block ends at `---` or `...`, and an unterminated block is
 * body. */
function headLines(text: string): number {
  if (!/^---\r?\n/.test(text)) return 0
  const lines = text.split('\n')
  for (let i = 1; i < lines.length; i++) {
    const l = (lines[i] ?? '').replace(/\r$/, '')
    if (l === '---' || l === '...') return i + 1
  }
  return 0
}

// Enables the task boxes in a render and flips the `[ ]` on the line each
// one points at. The line is checked before it is written: if the text has
// moved under the render (another client typed above), nothing is
// changed and the view is re-rendered instead.
function wireTasks(el: HTMLElement, sync: SyncClient, rerender: () => void): void {
  for (const box of el.querySelectorAll<HTMLInputElement>('input[type="checkbox"][data-line]')) {
    box.disabled = false
    box.addEventListener('change', () => {
      const line = Number(box.dataset['line'])
      const text = sync.text.toString()
      const n = headLines(text) + line
      let pos = 0
      for (let i = 0; i < n; i++) {
        const nl = text.indexOf('\n', pos)
        if (nl < 0) {
          rerender()
          return
        }
        pos = nl + 1
      }
      const end = text.indexOf('\n', pos)
      const lineText = text.slice(pos, end < 0 ? text.length : end)
      const m = /\[([ xX])\]/.exec(lineText)
      const was = m?.[1] !== undefined && m[1] !== ' '
      if (!m || was === box.checked) {
        rerender()
        return
      }
      const at = pos + m.index + 1
      sync.doc.transact(() => {
        sync.text.delete(at, 1)
        sync.text.insert(at, box.checked ? 'x' : ' ')
      })
    })
  }
}

interface DetailsProps {
  note: Note
  onOpen: (id: string) => void
  overlay: boolean
  onClose: () => void
  /** The path is a button: rename or move by path. */
  onRename: () => void
}

function Details({ note, onOpen, overlay, onClose, onRename }: DetailsProps) {
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
        <dd class="mono">
          <button type="button" class="meta-path" title="Rename or move by path" onClick={onRename}>
            {note.path}
          </button>
        </dd>
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
