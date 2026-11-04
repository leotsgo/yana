// The drawing page: an Excalidraw canvas for a .excalidraw.md note, from
// the bundle drawing-load.ts fetches on first use. Saves are whole-file:
// a second after the last change, when the tab hides, and on leaving. The
// server merges each save with the file element by element, so saves from
// several devices, or a draft from an offline spell, keep every stroke.
// Until the server confirms a save the scene is kept on the device as a
// draft, so going offline or closing the tab loses nothing; a draft left
// behind is saved first the next time the drawing opens. A drawing whose
// body could not be read is never saved over.
//
// While the drawing is open the page is in its room on the relay
// (drawing-live.ts): changed elements go to the others as they happen,
// theirs merge in out of the undo history, and each page shows the
// others' pointers.

import type { ComponentChildren } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'

import { api, ApiError } from './api'
import type { Note } from './api'
import * as cache from './cache'
import { markLocalConflict } from './conflict'
import { changedSince, markSent, pack, packFile } from './drawing-delta'
import type { Delta } from './drawing-delta'
import type { EditorHandle } from './drawing-editor/editor'
import { DrawingRoom, MAX_FRAME } from './drawing-live'
import { loadDrawing } from './drawing-load'
import { Icon } from './icons'
import { createPathFor } from './panels'
import type { OpenNote } from './panels'
import * as prefs from './prefs'
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

// Per drawing, across mounts of its page: the save on its way, and the
// last scene and hash the server confirmed. A page opened again while the
// one before is still saving waits for that save and starts from it.
const inflight = new Map<string, Promise<void>>()
const confirmed = new Map<string, { scene: string; hash: string }>()

export interface DrawingNoteProps {
  note: Note
  onOpen: OpenNote
  onToast: (m: string) => void
  /** The page's own buttons (details, overflow), at the end of the toolbar. */
  children?: ComponentChildren
  /** The page's header, given this drawing's toolbar to carry. */
  head: (actions: ComponentChildren, status?: ComponentChildren) => ComponentChildren
  /** The details pane, beside the canvas. */
  aside: ComponentChildren
}

export function DrawingNote({ note, onOpen, onToast, children, head, aside }: DrawingNoteProps) {
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
    // Scroll and zoom are this browser's, kept apart from the file so
    // panning never saves; written a moment after the canvas settles.
    let view: prefs.DrawingView | null = null
    let viewTimer: ReturnType<typeof setTimeout> | undefined
    const keepView = () => {
      clearTimeout(viewTimer)
      if (view) prefs.setDrawingView(id, view)
    }
    const onView = (v: prefs.DrawingView) => {
      view = v
      clearTimeout(viewTimer)
      viewTimer = setTimeout(keepView, 400)
    }
    let timer = 0
    let links = note.links
    let status: SaveState = 'saved'
    const show = (s: SaveState) => {
      status = s
      if (alive) setState(s)
    }
    const toast = (m: string) => latest.current.onToast(m)

    // Live: the room, which element versions it has from this page, the
    // images it has been sent, and whether an image never arrived (the
    // next file change fetches it with the saved scene).
    let room: DrawingRoom | null = null
    const sent = new Map<string, number>()
    const sentFiles = new Set<string>()
    let needFiles = false
    let deltaTimer = 0
    let fetchTimer = 0

    // Merges a whole scene from the server into the canvas.
    function takeScene(json: string): void {
      if (!editor) return
      try {
        const s = JSON.parse(json) as { elements?: { id: string; version: number }[]; files?: Record<string, unknown> }
        const els = s.elements ?? []
        needFiles = editor.merge(els, Object.values(s.files ?? {}))
        markSent(els, sent)
      } catch {
        // Not a scene: the page keeps what it has.
      }
    }

    const sendFiles = (els: readonly unknown[], all: boolean) => {
      if (!room || !editor) return
      const files = editor.files()
      for (const e of els as { fileId?: string | null; isDeleted?: boolean }[]) {
        const f = e.fileId ? files[e.fileId] : undefined
        if (!f || e.isDeleted || (!all && sentFiles.has(f.id))) continue
        sentFiles.add(f.id)
        const p = packFile(f, MAX_FRAME)
        if (p) room.send([p])
      }
    }

    // The elements changed since the last send, about ten times a second.
    const flushDelta = () => {
      deltaTimer = 0
      if (!room || !editor || viewOnly) return
      const els = changedSince(editor.elements(), sent)
      if (els.length === 0) return
      room.send(pack(els, MAX_FRAME))
      sendFiles(els, false)
    }
    const scheduleDelta = () => {
      if (!deltaTimer) deltaTimer = window.setTimeout(flushDelta, 100)
    }

    // Joined: say what this page has, and ask the others for theirs.
    const hello = () => {
      if (!room || !editor || viewOnly) return
      sent.clear()
      sentFiles.clear()
      const els = editor.elements()
      markSent(els, sent)
      room.send(pack(els, MAX_FRAME, true))
      sendFiles(els, true)
    }

    const onDelta = (d: Delta) => {
      if (!editor) return
      const els = (d.els ?? []) as { id: string; version: number }[]
      needFiles = editor.merge(els, d.files ?? [])
      markSent(els, sent)
      if (d.hello && !viewOnly && room) {
        // A page joined: it gets the whole scene, images included.
        const all = editor.elements()
        room.send(pack(all, MAX_FRAME))
        sendFiles(all, true)
      }
    }

    // The file changed on disk: unless it is this page's own save, fetch
    // it and merge (an agent's write, a restore, a save from a page that
    // was not in the room, an image too large to travel live).
    const onChanged = (hash: string | undefined) => {
      const own = () => hash !== undefined && hash === base && !needFiles
      if (own()) return
      window.clearTimeout(fetchTimer)
      fetchTimer = window.setTimeout(() => {
        // The notice can beat this page's own save reply; by now it is in.
        if (own()) return
        api.note(id).then(
          (fresh) => {
            if (!alive || fresh.scene === undefined || fresh.scene_error) return
            saved = fresh.scene
            base = fresh.content_hash
            links = fresh.links
            confirmed.set(id, { scene: saved, hash: base })
            forgetEmbed(id)
            takeScene(fresh.scene)
          },
          () => {}, // offline: the next change or join tries again
        )
      }, 300)
    }

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
      let release = () => {}
      const done = new Promise<void>((r) => (release = r))
      inflight.set(id, done)
      let again = false
      try {
        const res = await api.saveSource(id, scene, base)
        base = res.hash
        // The file holds more than this page had (another device's save):
        // the server sent the merge, and the canvas takes it in.
        saved = res.scene ?? scene
        if (res.scene) takeScene(res.scene)
        confirmed.set(id, { scene: saved, hash: base })
        forgetEmbed(id)
        void cache.putNote({ ...latest.current.note, scene: saved, content_hash: base })
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
          again = true
        }
      } catch (err) {
        // The draft stays either way. A refusal (too large, not a scene)
        // is said; a network failure is tried again in a while and when
        // the connection returns. A closed page leaves it to the draft.
        if (err instanceof ApiError && err.status < 500) {
          show('error')
          toast(err.message)
        } else {
          show('offline')
          if (alive) timer = window.setTimeout(() => void save(), 10_000)
        }
      } finally {
        busy = false
        if (inflight.get(id) === done) inflight.delete(id)
        // A closed page saves the rest at once, before release, so a page
        // opened meanwhile waits for it too; no timer outlives the page.
        if (again) {
          if (alive) schedule()
          else void save()
        }
        release()
      }
    }

    const onChange = (scene: string) => {
      if (viewOnly) return
      pending = scene
      void cache.putDraft(id, { scene, baseHash: base, at: Date.now() })
      if (status !== 'offline') show('saving')
      schedule()
      scheduleDelta()
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
      // The note this page was given may predate a save the page before
      // it made: wait for that save, then take the server's copy. A save
      // that hangs is waited for 15 s at most.
      let late = false
      const deadline = new Promise<void>((r) =>
        window.setTimeout(() => {
          late = true
          r()
        }, 15_000),
      )
      for (let p = inflight.get(id); p && !late; p = inflight.get(id)) await Promise.race([p, deadline])
      const last = confirmed.get(id)
      if (last && last.hash !== base) {
        try {
          const fresh = await api.note(id)
          if (fresh.scene === undefined || fresh.scene_error) {
            if (alive) setNotice('This drawing could not be read. It is left as it is.')
            return
          }
          saved = fresh.scene
          base = fresh.content_hash
          links = fresh.links
          // Newer than this tab's last save: an offline open falls back to it.
          confirmed.set(id, { scene: saved, hash: base })
        } catch {
          saved = last.scene
          base = last.hash
        }
      }
      if (!alive) return
      let scene = saved
      // A draft is a scene no save confirmed: save it before anything else.
      // The server merges it with a file that moved on since, and the
      // canvas opens on the merge.
      const draft = viewOnly ? null : await cache.getDraft(id)
      if (draft && sameElements(draft.scene, saved)) void cache.deleteDraft(id)
      else if (draft) {
        pending = draft.scene
        base = draft.baseHash
        scene = draft.scene
        await save()
        if (pending === null) scene = saved
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
        editor = mod.mountEditor(el, {
          sceneJSON: scene,
          dark: isDark(),
          viewOnly,
          onChange,
          onLink: (l, ev) => void onLink(l, ev),
          view: prefs.drawingView(id),
          onView,
          onPointer: (x, y, button, selected) => room?.point(x, y, button, selected),
        })
      } catch {
        setNotice('This drawing could not be read. It is left as it is.')
        return
      }
      await editor.ready
      if (!alive) return
      room = new DrawingRoom(id, {
        onJoined: hello,
        onDelta,
        onChanged,
        onPointers: (peers) => editor?.setPeers([...peers].map(([key, p]) => ({ key, ...p }))),
      })
      if (document.visibilityState === 'hidden') room.pause()
    }
    void open()

    // The canvas follows the app's theme (the same watch rich-load.ts keeps).
    const theme = new MutationObserver(() => editor?.setDark(isDark()))
    theme.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
    const onHide = () => {
      // A hidden page leaves the room, so its cursor goes; it joins again,
      // and catches up, when it shows.
      if (document.visibilityState !== 'hidden') {
        room?.resume()
        return
      }
      keepView()
      flushDelta()
      room?.pause()
      void save()
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
      keepView()
      flushDelta()
      window.clearTimeout(deltaTimer)
      window.clearTimeout(fetchTimer)
      room?.destroy()
      void save()
      editor?.unmount()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [note.id])

  return (
    <>
      {head(
        <div class="editor-toolbar">
          <span class="spacer" />
          {children}
        </div>,
        <>
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
        </>,
      )}
      <div class="page-body">
        <section class="drawing-note">
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
        {aside}
      </div>
    </>
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
    const put = (got: Embed | null) => {
      if (!got) return
      block.prepend(got.svg.cloneNode(true))
      block.classList.add('drawn')
      block.title = `Open ${got.title}`
      block.addEventListener('click', (ev) => {
        // The link inside opens it already.
        if ((ev.target as Element).closest('a')) return
        onOpen(id, howFrom(ev))
      })
    }
    const e = embedSvg(id, isDark())
    // Drawn before: in at once, so a live re-render does not jump.
    if (e.got !== undefined) put(e.got)
    else e.ready.then(put, () => {})
  }
}

// One SVG per drawing and theme, cloned into each render. A save from
// this tab drops it; a drawing that cannot be drawn is null (the link
// stays), and a failed fetch is forgotten so the next render tries again.
// ponytail: a save from another device shows after a reload; drop the
// entry on the relay's change notice if that matters.
interface Embed {
  svg: SVGSVGElement
  title: string
}
const embeds = new Map<string, { ready: Promise<Embed | null>; got?: Embed | null }>()

function embedSvg(id: string, dark: boolean) {
  const key = `${id}:${dark}`
  let e = embeds.get(key)
  if (!e) {
    const entry: { ready: Promise<Embed | null>; got?: Embed | null } = {
      ready: api.note(id).then(async (n) => {
        if (n.scene === undefined || n.scene_error) return null
        return { svg: await (await loadDrawing()).renderSvg(n.scene, dark), title: n.title }
      }),
    }
    entry.ready.then(
      (got) => (entry.got = got),
      () => embeds.delete(key),
    )
    embeds.set(key, entry)
    e = entry
  }
  return e
}

function forgetEmbed(id: string): void {
  embeds.delete(`${id}:true`)
  embeds.delete(`${id}:false`)
}

// Two scenes with the same elements, but for their version stamps, and the
// same background colour. Excalidraw can bump versions with nothing changed
// (text re-measured once its fonts load, right after mount); that is not
// worth a save.
function sameElements(a: string, b: string): boolean {
  const strip = (s: string): string => {
    try {
      const { elements, appState } = JSON.parse(s) as { elements?: unknown; appState?: { viewBackgroundColor?: unknown } }
      return JSON.stringify([elements, appState?.viewBackgroundColor], (k, v: unknown) =>
        k === 'version' || k === 'versionNonce' || k === 'updated' ? undefined : v,
      )
    } catch {
      return s
    }
  }
  return strip(a) === strip(b)
}
