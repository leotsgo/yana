// The drawing page: an Excalidraw canvas for a .excalidraw.md note, from
// the bundle drawing-load.ts fetches on first use. Saves are whole-file,
// as for HTML notes: a second after the last change, when the tab hides,
// and on leaving. Until the server confirms a save the scene is kept on
// the device as a draft, so going offline or closing the tab loses
// nothing; a draft left behind is saved first the next time the drawing
// opens. A drawing whose body could not be read is never saved over.

import type { ComponentChildren } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { Note } from './api'
import * as cache from './cache'
import { markLocalConflict } from './conflict'
import type { EditorHandle } from './drawing-editor/editor'
import { loadDrawing } from './drawing-load'
import { Icon } from './icons'
import { createPathFor } from './panels'
import type { OpenNote } from './panels'
import { isDark } from './rich-load'
import { howFrom } from './workspace'

type SaveState = 'saved' | 'saving' | 'offline' | 'error'

// The sync chip's vocabulary and classes (app.css .sync-status).
const LABEL: Record<SaveState, string> = { saved: 'Saved', saving: 'Saving', offline: 'Offline', error: 'Not saved' }
const CLASS: Record<SaveState, string> = { saved: 'synced', saving: 'connecting', offline: 'offline', error: 'offline' }
const TITLE: Record<SaveState, string> = {
  saved: 'The file on the server has this drawing.',
  saving: 'Saving a second after the last change.',
  offline: 'Kept on this device; saved when the server is back.',
  error: 'The server refused the save; the drawing is kept on this device.',
}

export interface DrawingNoteProps {
  note: Note
  onOpen: OpenNote
  onToast: (m: string) => void
  /** The page's own buttons (details, overflow), at the end of the toolbar. */
  children?: ComponentChildren
}

export function DrawingNote({ note, onOpen, onToast, children }: DrawingNoteProps) {
  const host = useRef<HTMLDivElement>(null)
  const [state, setState] = useState<SaveState>('saved')
  const [notice, setNotice] = useState<string | null>(null)
  // The effect below runs once per drawing; these follow the latest props
  // (a rename changes the note's path, and with it where links create).
  const latest = useRef({ note, onOpen, onToast })
  latest.current = { note, onOpen, onToast }
  const readable = note.scene !== undefined && !note.scene_error
  const viewOnly = note.role === 'viewer'

  useEffect(() => {
    const el = host.current
    if (!el || note.scene === undefined || note.scene_error) return
    const id = note.id
    let alive = true
    let editor: EditorHandle | null = null
    let base = note.content_hash
    let saved = note.scene // what the server holds, as far as this page knows
    let pending: string | null = null // a scene the server has not confirmed
    let busy = false
    let timer = 0
    let links = note.links
    let status: SaveState = 'saved'
    const show = (s: SaveState) => {
      status = s
      if (alive) setState(s)
    }
    const toast = (m: string) => latest.current.onToast(m)

    const schedule = () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => void save(), 1000)
    }

    async function save(): Promise<void> {
      window.clearTimeout(timer)
      if (pending === null || busy) return
      const scene = pending
      if (sameElements(scene, saved)) {
        pending = null
        void cache.deleteDraft(id)
        show('saved')
        return
      }
      busy = true
      show('saving')
      try {
        const res = await api.saveSource(id, scene, base)
        base = res.hash
        saved = scene
        void cache.putNote({ ...latest.current.note, scene, content_hash: base })
        if (res.conflict_copy) {
          markLocalConflict(res.conflict_copy)
          toast(`Saved. The version that was on disk moved to ${res.conflict_copy}.`)
        }
        if (pending === scene) {
          pending = null
          void cache.deleteDraft(id)
          show('saved')
        } else if (pending !== null) {
          // Drawn on while this save was out: the draft now builds on it.
          void cache.putDraft(id, { scene: pending, baseHash: base, at: Date.now() })
          schedule()
        }
      } catch (err) {
        // The draft stays either way. A refusal (too large, not a scene)
        // is said; a network failure waits for the connection.
        if (err instanceof ApiError && err.status < 500) {
          show('error')
          toast(err.message)
        } else {
          show('offline')
        }
      } finally {
        busy = false
      }
    }

    const onChange = (scene: string) => {
      if (viewOnly) return
      pending = scene
      void cache.putDraft(id, { scene, baseHash: base, at: Date.now() })
      if (status !== 'offline') show('saving')
      schedule()
    }

    // A [[target]] on an element: the note's links say where it goes. One
    // drawn since the load is not among them until it is saved and indexed.
    const onLink = async (link: string, ev: MouseEvent | undefined) => {
      const target = (link.trim().slice(2, -2).split('|')[0] ?? '').trim()
      const how = ev ? howFrom(ev) : undefined
      let hit = links.find((l) => l.raw_target === target)
      if (!hit) {
        await save()
        try {
          links = (await api.note(id)).links
        } catch {
          // Offline: the lookup below says so.
        }
        hit = links.find((l) => l.raw_target === target)
      }
      if (hit?.resolved && hit.to_id) {
        latest.current.onOpen(hit.to_id, how)
        return
      }
      if (!hit) {
        toast(`“${target}” is not saved yet; try again in a moment.`)
        return
      }
      api
        .createNote(createPathFor(latest.current.note, target), '')
        .then((res) => latest.current.onOpen(res.id, how))
        .catch((err: unknown) => toast(err instanceof ApiError ? err.message : 'Could not create the note.'))
    }

    const open = async () => {
      let scene = saved
      // A draft is a scene no save confirmed: save it before anything else,
      // from the hash it was edited from, so a file that moved on since is
      // kept as a conflict copy instead of lost.
      const draft = viewOnly ? null : await cache.getDraft(id)
      if (draft && sameElements(draft.scene, saved)) void cache.deleteDraft(id)
      else if (draft) {
        pending = draft.scene
        base = draft.baseHash
        scene = draft.scene
        await save()
      }
      if (!alive) return
      let mod
      try {
        mod = await loadDrawing()
      } catch {
        if (alive) setNotice('The drawing editor could not be loaded. It loads once the connection is back.')
        return
      }
      if (!alive) return
      try {
        editor = mod.mountEditor(el, { sceneJSON: scene, dark: isDark(), viewOnly, onChange, onLink: (l, ev) => void onLink(l, ev) })
      } catch {
        setNotice('This drawing could not be read. It is left as it is.')
      }
    }
    void open()

    // The canvas follows the app's theme (the same watch rich-load.ts keeps).
    const theme = new MutationObserver(() => editor?.setDark(isDark()))
    theme.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    const onHide = () => {
      if (document.visibilityState === 'hidden') void save()
    }
    const onUnload = (ev: BeforeUnloadEvent) => {
      if (pending === null) return
      ev.preventDefault()
      ev.returnValue = ''
    }
    const onOnline = () => void save()
    document.addEventListener('visibilitychange', onHide)
    window.addEventListener('beforeunload', onUnload)
    window.addEventListener('online', onOnline)
    return () => {
      alive = false
      theme.disconnect()
      document.removeEventListener('visibilitychange', onHide)
      window.removeEventListener('beforeunload', onUnload)
      window.removeEventListener('online', onOnline)
      void save()
      editor?.unmount()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [note.id])

  return (
    <section class="drawing-note">
      <div class="editor-toolbar">
        {viewOnly ? (
          <span class="role-badge" title="Your account reads this space">
            <Icon name="eye" />
            Viewer
          </span>
        ) : (
          readable && (
            <span class={'sync-status ' + CLASS[state]} title={TITLE[state]}>
              <span class="sync-dot" />
              <span class="sync-label">{LABEL[state]}</span>
            </span>
          )
        )}
        {notice && <span class="editor-notice">{notice}</span>}
        <span class="spacer" />
        {children}
      </div>
      {readable ? (
        <div class="drawing-canvas">
          <div class="drawing-host" ref={host} />
        </div>
      ) : (
        <p class="drawing-unreadable" title={note.scene_error}>
          This drawing could not be read. It is left as it is.
        </p>
      )}
    </section>
  )
}

/**
 * Draws the drawings embedded in a rendered note: the server leaves a
 * .drawing-embed block holding a link, and the image goes before the
 * link. A click on the block opens the drawing. The editor bundle is only
 * fetched when the note has such a block; a drawing that cannot be drawn
 * keeps the plain link.
 */
export function renderDrawingEmbeds(root: HTMLElement, onOpen: OpenNote): void {
  for (const block of root.querySelectorAll<HTMLElement>('.drawing-embed[data-id]')) {
    if (block.dataset['drawn']) continue
    block.dataset['drawn'] = '1'
    const id = block.dataset['id'] ?? ''
    // ponytail: fetched again on every render (each live re-render while
    // typing in split); keep the SVG per id and content_hash if that shows.
    api
      .note(id)
      .then(async (n) => {
        if (n.scene === undefined || n.scene_error) return
        const svg = await (await loadDrawing()).renderSvg(n.scene, isDark())
        block.prepend(svg)
        block.classList.add('drawn')
        block.title = `Open ${n.title}`
        block.addEventListener('click', (ev) => {
          // The link inside opens it already.
          if ((ev.target as Element).closest('a')) return
          onOpen(id, howFrom(ev))
        })
      })
      .catch(() => {
        // Offline the first time, or not a scene after all: the link stays.
      })
  }
}

// Two scenes with the same elements but for their version stamps.
// Excalidraw can bump versions with nothing changed (text re-measured
// once its fonts load, right after mount); that is not worth a save.
function sameElements(a: string, b: string): boolean {
  const strip = (s: string): string => {
    try {
      const elements = (JSON.parse(s) as { elements?: unknown }).elements
      return JSON.stringify(elements, (k, v: unknown) => (k === 'version' || k === 'versionNonce' || k === 'updated' ? undefined : v))
    } catch {
      return s
    }
  }
  return strip(a) === strip(b)
}
