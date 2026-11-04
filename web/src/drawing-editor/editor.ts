// The Excalidraw editor and renderer, built by build.mjs into its own
// bundle under assets/drawing/ and loaded through drawing-load.ts the
// first time a drawing is opened. React lives only here; the app talks to
// it through mountEditor and renderSvg. No JSX: the app's jsxImportSource
// is preact.
import '@excalidraw/excalidraw/index.css'
import {
  Excalidraw,
  exportToSvg,
  getNonDeletedElements,
  getSceneVersion,
  restore,
  serializeAsJSON,
} from '@excalidraw/excalidraw'
import { createElement } from 'react'
import { createRoot } from 'react-dom/client'

export interface EditorOptions {
  sceneJSON: string
  dark: boolean
  viewOnly: boolean
  /** Fires with serializeAsJSON(elements, appState, files, 'local') only when getSceneVersion or the background colour changed. */
  onChange: (sceneJSON: string) => void
  /** A clicked element link; the editor has already prevented Excalidraw's default for [[…]] links. */
  onLink: (link: string, ev: MouseEvent | undefined) => void
  /** Where the canvas was left; without one it scrolls to the content. */
  view?: View | null
  /** Fires when the scroll position, zoom or tool changes. Never saved to the file. */
  onView?: (view: View) => void
}

/** Where the canvas was left: scroll offsets, zoom factor, and the tool in hand. */
export interface View {
  scrollX: number
  scrollY: number
  zoom: number
  tool?: string
  locked?: boolean
}

// Tools that are one-off actions rather than a mode to come back to:
// the image tool opens a file picker, the rest need context to exist.
const TRANSIENT_TOOLS = new Set(['image', 'custom', 'embeddable', 'eraser', 'laser', 'magicframe'])

export interface EditorHandle {
  setDark(dark: boolean): void
  unmount(): void
}

// Throws on a scene that is not JSON, so the caller never opens (and
// later saves over) a drawing it could not read.
function parse(sceneJSON: string) {
  return restore(JSON.parse(sceneJSON), null, null)
}

export function mountEditor(host: HTMLElement, opts: EditorOptions): EditorHandle {
  const scene = parse(opts.sceneJSON)
  // Scroll and zoom change appState only; saving on those would spam. The
  // background colour is the one appState change worth a save.
  let version = getSceneVersion(scene.elements)
  let background = scene.appState.viewBackgroundColor
  let view = opts.view ?? null
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
          opts.onChange(serializeAsJSON(elements, appState, files, 'local'))
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
  return { setDark: render, unmount: () => root.unmount() }
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
