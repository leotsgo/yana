// The HTML note page: a sandboxed frame on the content origin for the
// rendered note, a plain-text source editor, and the trust switch. There
// is no live editing here — HTML does not merge, so saves are whole-file
// and last-write-wins, with the server keeping a conflict copy when a
// save lands on a file that changed underneath it.

import { useEffect, useRef, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { Note } from './api'
import { Icon } from './icons'

export interface HtmlNoteProps {
  note: Note
  onOpen: (id: string) => void
  onToast: (msg: string) => void
  /** Opens the note's overflow menu (rename, export, delete) under the button. */
  onMore: (anchor: HTMLElement) => void
  details: boolean
  onToggleDetails: () => void
}

export function HtmlNote({ note, onOpen, onToast, onMore, details, onToggleDetails }: HtmlNoteProps) {
  const [frameURL, setFrameURL] = useState<string | null>(null)
  const [trusted, setTrusted] = useState(note.trusted)
  const [editing, setEditing] = useState(false)
  const [source, setSource] = useState(note.source ?? '')
  const [baseHash, setBaseHash] = useState(note.content_hash)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const originRef = useRef<string | null>(null)
  const reloadRef = useRef<(() => void) | null>(null)

  const loadView = () => {
    api
      .noteView(note.id)
      .then(({ url }) => {
        originRef.current = new URL(url).origin
        setFrameURL(url)
      })
      .catch((err: unknown) => {
        setNotice(err instanceof ApiError && err.status === 501
          ? 'This server runs without the content origin; the source is shown as text.'
          : 'The frame could not be loaded; the source is shown as text.')
      })
  }

  useEffect(() => {
    setTrusted(note.trusted)
    setSource(note.source ?? '')
    setBaseHash(note.content_hash)
    setDirty(false)
    setNotice(null)
    loadView()
    // A reload key the trust switch and saves can bump.
    reloadRef.current = loadView
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [note.id])

  // Wikilink clicks come from the frame as messages; only the content
  // origin's are heard.
  useEffect(() => {
    const onMessage = (ev: MessageEvent) => {
      if (!originRef.current || ev.origin !== originRef.current) return
      const data = ev.data as { yana?: string; id?: string | null; target?: string } | null
      if (!data || data.yana !== 'wikilink') return
      if (data.id) onOpen(data.id)
      else onToast(`No note matches “${data.target ?? ''}” yet.`)
    }
    window.addEventListener('message', onMessage)
    return () => window.removeEventListener('message', onMessage)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [note.id])

  const save = () => {
    if (!dirty || saving) return
    setSaving(true)
    api
      .saveSource(note.id, source, baseHash)
      .then((res) => {
        setBaseHash(res.hash)
        setDirty(false)
        setSaving(false)
        if (res.conflict_copy) {
          setNotice(`Saved. The version that was on disk moved to ${res.conflict_copy}.`)
        } else {
          setNotice(null)
        }
        reloadRef.current?.()
      })
      .catch((err: unknown) => {
        setSaving(false)
        onToast(err instanceof ApiError ? err.message : 'Could not save.')
      })
  }

  const toggleTrust = () => {
    const next = !trusted
    api
      .setTrusted(note.id, next)
      .then(() => {
        setTrusted(next)
        reloadRef.current?.()
        onToast(next ? 'This note now renders unsanitized.' : 'This note is sanitized again.')
      })
      .catch((err: unknown) => {
        onToast(err instanceof ApiError ? err.message : 'Could not change trust.')
      })
  }

  return (
    <section class={'html-note' + (editing ? ' editing' : '')}>
      <div class="editor-toolbar">
        <span class={'trust-badge ' + (trusted ? 'trusted' : 'sandboxed')} title={trusted
          ? 'Rendered as written, scripts and all, on the content origin.'
          : 'Sanitized: scripts, event handlers and external loads are stripped before rendering.'}>
          <Icon name={trusted ? 'shield-off' : 'shield'} />
          {trusted ? 'Unsanitized' : 'Sanitized'}
        </span>
        {notice && <span class="editor-notice">{notice}</span>}
        <span class="spacer" />
        {editing && (
          <>
            <span class={'sync-status ' + (dirty ? 'offline' : 'synced')}>
              <span class="sync-dot" />
              <span class="sync-label">{dirty ? 'Unsaved' : 'Saved'}</span>
            </span>
            <button type="button" class="btn primary" disabled={!dirty || saving} onClick={save}>
              <Icon name="save" />
              Save
            </button>
          </>
        )}
        <button type="button" class={'btn' + (editing ? ' on' : '')} onClick={() => setEditing((e) => !e)} title="Edit the HTML source" aria-pressed={editing}>
          <Icon name="code" />
          Source
        </button>
        <button
          type="button"
          class={'btn trust-toggle' + (trusted ? ' on' : '')}
          onClick={toggleTrust}
          title={trusted ? 'Sanitize this note again' : 'Render this note unsanitized (scripts run)'}
          aria-pressed={trusted}
        >
          <Icon name={trusted ? 'shield-off' : 'shield'} />
          Trust
        </button>
        <button type="button" class={'btn' + (details ? ' on' : '')} onClick={onToggleDetails} title="Path, backlinks and history" aria-pressed={details}>
          <Icon name="panel-right" />
        </button>
        <button type="button" class="icon-btn" title="More" aria-label="More actions" onClick={(ev) => onMore(ev.currentTarget as HTMLElement)}>
          <Icon name="more" size={18} />
        </button>
      </div>
      {editing ? (
        <textarea
          class="source-editor"
          spellcheck={false}
          value={source}
          onKeyDown={(ev: KeyboardEvent) => {
            if ((ev.metaKey || ev.ctrlKey) && ev.key === 's') {
              ev.preventDefault()
              save()
            }
          }}
          onInput={(ev: Event) => {
            setSource((ev.target as HTMLTextAreaElement).value)
            setDirty(true)
          }}
        />
      ) : frameURL ? (
        <iframe
          class="html-frame"
          title={note.title || note.path}
          // allow-scripts only: the frame gets an opaque origin, so it
          // cannot reach this app's DOM, storage, or API even though its
          // markup runs.
          sandbox="allow-scripts"
          referrerpolicy="no-referrer"
          src={frameURL}
        />
      ) : (
        <div class="html-source-fallback">
          <pre class="note-source">
            <code>{source}</code>
          </pre>
        </div>
      )}
    </section>
  )
}
