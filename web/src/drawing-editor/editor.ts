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
  /** Fires with serializeAsJSON(elements, appState, files, 'local') only when getSceneVersion changed. */
  onChange: (sceneJSON: string) => void
  /** A clicked element link; the editor has already prevented Excalidraw's default for [[…]] links. */
  onLink: (link: string, ev: MouseEvent | undefined) => void
}

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
  // Scroll and zoom change appState only; saving on those would spam.
  let version = getSceneVersion(scene.elements)
  const root = createRoot(host)
  const render = (dark: boolean) =>
    root.render(
      createElement(Excalidraw, {
        initialData: { ...scene, scrollToContent: true },
        theme: dark ? 'dark' : 'light',
        viewModeEnabled: opts.viewOnly,
        onChange: (elements, appState, files) => {
          const next = getSceneVersion(elements)
          if (next === version) return
          version = next
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

export function renderSvg(sceneJSON: string, dark: boolean): Promise<SVGSVGElement> {
  const scene = parse(sceneJSON)
  return exportToSvg({
    elements: getNonDeletedElements(scene.elements),
    appState: { ...scene.appState, exportWithDarkMode: dark },
    files: scene.files,
  })
}
