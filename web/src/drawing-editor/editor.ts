// The Excalidraw editor and renderer, built by build.mjs into its own
// bundle under assets/drawing/ and loaded through drawing-load.ts the
// first time a drawing is opened. React lives only here; the app talks to
// it through mountEditor and renderSvg. No JSX: the app's jsxImportSource
// is preact.
import '@excalidraw/excalidraw/index.css'
import {
  CaptureUpdateAction,
  Excalidraw,
  exportToSvg,
  getNonDeletedElements,
  getSceneVersion,
  reconcileElements,
  restore,
  restoreElements,
  serializeAsJSON,
} from '@excalidraw/excalidraw'
import type { RemoteExcalidrawElement } from '@excalidraw/excalidraw/data/reconcile'
import type { OrderedExcalidrawElement } from '@excalidraw/excalidraw/element/types'
import type { AppState, BinaryFileData, BinaryFiles, Collaborator, ExcalidrawImperativeAPI, SocketId } from '@excalidraw/excalidraw/types'
import { createElement } from 'react'
import { createRoot } from 'react-dom/client'

export interface EditorOptions {
  sceneJSON: string
  dark: boolean
  viewOnly: boolean
  /** Fires with the scene, deleted elements kept as tombstones, only when getSceneVersion or the background colour changed by a local edit. */
  onChange: (sceneJSON: string) => void
  /** A clicked element link; the editor has already prevented Excalidraw's default for [[…]] links. */
  onLink: (link: string, ev: MouseEvent | undefined) => void
  /** Where the canvas was left; without one it scrolls to the content. */
  view?: View | null
  /** Fires when the scroll position, zoom or tool changes. Never saved to the file. */
  onView?: (view: View) => void
  /** The pointer moved, in scene coordinates, with the selection at that moment. */
  onPointer?: (x: number, y: number, button: 'up' | 'down', selected: Record<string, true>) => void
}

/** Where the canvas was left: scroll offsets, zoom factor, and the tool in hand. */
export interface View {
  scrollX: number
  scrollY: number
  zoom: number
  tool?: string
  locked?: boolean
}

/** Another page's pointer, drawn by Excalidraw as a named cursor. */
export interface Peer {
  key: string
  name: string
  x: number
  y: number
  button: 'up' | 'down'
  selected: Record<string, true>
}

// Tools that are one-off actions rather than a mode to come back to:
// the image tool opens a file picker, the rest need context to exist.
const TRANSIENT_TOOLS = new Set(['image', 'custom', 'embeddable', 'eraser', 'laser', 'magicframe'])

export interface EditorHandle {
  setDark(dark: boolean): void
  unmount(): void
  /** Resolves once the canvas is up; the calls below wait for it. */
  ready: Promise<void>
  /** Every element, deleted ones included, in scene order. */
  elements(): readonly OrderedExcalidrawElement[]
  files(): BinaryFiles
  /**
   * Merges elements and files from elsewhere (the room, the server) by
   * Excalidraw's own reconciliation, out of the undo history and without
   * counting as a local change. Returns whether an image is still missing.
   */
  merge(elements: readonly unknown[], files?: readonly unknown[]): boolean
  setPeers(peers: readonly Peer[]): void
}

// Throws on a scene that is not JSON, so the caller never opens (and
// later saves over) a drawing it could not read.
function parse(sceneJSON: string) {
  return restore(JSON.parse(sceneJSON), null, null)
}

// serializeAsJSON leaves deleted elements out. The server merges a save
// with the file element by element, so a delete must travel as the
// tombstone it is, or the copy on disk would bring the element back.
function serialize(elements: readonly OrderedExcalidrawElement[], appState: AppState, files: BinaryFiles): string {
  const data = JSON.parse(serializeAsJSON([], appState, {}, 'local')) as Record<string, unknown>
  const used: BinaryFiles = {}
  for (const e of elements) {
    if (!e.isDeleted && 'fileId' in e && e.fileId && files[e.fileId]) used[e.fileId] = files[e.fileId]!
  }
  data['elements'] = elements.map((e) => (e.type === 'arrow' || e.type === 'line' ? { ...e, lastCommittedPoint: null } : e))
  data['files'] = used
  return JSON.stringify(data, null, 2)
}

export function mountEditor(host: HTMLElement, opts: EditorOptions): EditorHandle {
  const scene = parse(opts.sceneJSON)
  // Scroll and zoom change appState only; saving on those would spam. The
  // background colour is the one appState change worth a save.
  let version = getSceneVersion(scene.elements)
  let background = scene.appState.viewBackgroundColor
  let view = opts.view ?? null
  let api: ExcalidrawImperativeAPI | null = null
  let isReady = () => {}
  const ready = new Promise<void>((r) => (isReady = r))
  const initialData = view
    ? {
        ...scene,
        appState: {
          ...scene.appState,
          scrollX: view.scrollX,
          scrollY: view.scrollY,
          zoom: { value: view.zoom as typeof scene.appState.zoom.value },
          ...(view.tool && !opts.viewOnly
            ? {
                activeTool: {
                  ...scene.appState.activeTool,
                  type: view.tool,
                  customType: null,
                  locked: view.locked ?? false,
                } as typeof scene.appState.activeTool,
              }
            : {}),
        },
      }
    : { ...scene, scrollToContent: true }
  const root = createRoot(host)
  const render = (dark: boolean) =>
    root.render(
      createElement(Excalidraw, {
        initialData,
        theme: dark ? 'dark' : 'light',
        viewModeEnabled: opts.viewOnly,
        excalidrawAPI: (a) => {
          api = a
          // For debugging and end-to-end checks: the canvas's API on its host.
          Object.assign(host, { excalidrawAPI: a })
          isReady()
        },
        onPointerUpdate: ({ pointer, button }) => {
          const selected = (api?.getAppState().selectedElementIds ?? {}) as Record<string, true>
          opts.onPointer?.(pointer.x, pointer.y, button, selected)
        },
        onChange: (elements, appState, files) => {
          const { scrollX, scrollY } = appState
          const zoom = appState.zoom.value
          // A viewer's canvas is always on the hand; keep whatever tool the
          // view already remembers rather than recording that.
          const { type, locked } = appState.activeTool
          const keepTool = opts.viewOnly || TRANSIENT_TOOLS.has(type)
          const tool = keepTool ? view?.tool : type
          const lock = keepTool ? view?.locked : locked
          if (
            !view ||
            view.scrollX !== scrollX ||
            view.scrollY !== scrollY ||
            view.zoom !== zoom ||
            view.tool !== tool ||
            view.locked !== lock
          ) {
            view = { scrollX, scrollY, zoom, tool, locked: lock }
            opts.onView?.(view)
          }
          const next = getSceneVersion(elements)
          if (next === version && appState.viewBackgroundColor === background) return
          version = next
          background = appState.viewBackgroundColor
          opts.onChange(serialize(elements, appState, files))
        },
        onLinkOpen: (element, event) => {
          const link = element.link ?? ''
          if (!link.startsWith('[[')) return
          event.preventDefault()
          const native = event.detail.nativeEvent
          opts.onLink(link, native instanceof MouseEvent ? native : native?.nativeEvent)
        },
      }),
    )
  render(opts.dark)

  const missing = () => {
    if (!api) return false
    const have = api.getFiles()
    return api.getSceneElements().some((e) => e.type === 'image' && e.fileId !== null && !have[e.fileId])
  }
  return {
    setDark: render,
    unmount: () => root.unmount(),
    ready,
    elements: () => api?.getSceneElementsIncludingDeleted() ?? [],
    files: () => api?.getFiles() ?? {},
    merge(elements, files) {
      if (!api) return false
      if (files && files.length > 0) api.addFiles(files as BinaryFileData[])
      if (elements.length > 0) {
        // As Excalidraw's own collaboration does: restore, then reconcile.
        const remote = restoreElements(elements as OrderedExcalidrawElement[], null) as RemoteExcalidrawElement[]
        const merged = reconcileElements(api.getSceneElementsIncludingDeleted(), remote, api.getAppState())
        api.updateScene({ elements: merged, captureUpdate: CaptureUpdateAction.NEVER })
        // Not this page's edit: no save and no delta for it.
        version = getSceneVersion(api.getSceneElementsIncludingDeleted())
      }
      return missing()
    },
    setPeers(peers) {
      if (!api) return
      const collaborators = new Map<SocketId, Collaborator>()
      for (const p of peers) {
        collaborators.set(p.key as SocketId, {
          id: p.name, // the cursor's colour follows the name
          username: p.name,
          pointer: { x: p.x, y: p.y, tool: 'pointer' },
          button: p.button,
          selectedElementIds: p.selected,
        })
      }
      api.updateScene({ collaborators, captureUpdate: CaptureUpdateAction.NEVER })
    },
  }
}

export async function renderSvg(sceneJSON: string, dark: boolean): Promise<SVGSVGElement> {
  const scene = parse(sceneJSON)
  const svg = await exportToSvg({
    elements: getNonDeletedElements(scene.elements),
    appState: { ...scene.appState, exportWithDarkMode: dark },
    files: scene.files,
  })
  // exportToSvg wraps a linked element in <a href="[[Note]]">, which the
  // browser would follow as a relative URL. The picture links nowhere; a
  // click on it opens the drawing.
  for (const a of svg.querySelectorAll('a')) a.replaceWith(...a.childNodes)
  return svg
}
