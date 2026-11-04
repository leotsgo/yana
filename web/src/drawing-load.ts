// Loads the Excalidraw bundle (React included) the first time a drawing
// needs it. It is built apart from the app under assets/drawing/ and is
// not precached: the service worker keeps it once fetched, through its
// cacheFirst rule for /assets/. Excalidraw reads its fonts from
// EXCALIDRAW_ASSET_PATH, which points at the copies beside the bundle.
type Editor = typeof import('./drawing-editor/editor')

// The hashed bundle paths, filled in by build.mjs.
declare const __DRAWING_JS__: string
declare const __DRAWING_CSS__: string

declare global {
  interface Window {
    EXCALIDRAW_ASSET_PATH?: string | string[]
  }
}

let load: Promise<Editor> | undefined

export function loadDrawing(): Promise<Editor> {
  if (load) return load
  window.EXCALIDRAW_ASSET_PATH = '/assets/drawing/'
  if (!document.querySelector(`link[href="${__DRAWING_CSS__}"]`)) {
    const link = document.createElement('link')
    link.rel = 'stylesheet'
    link.href = __DRAWING_CSS__
    document.head.append(link)
  }
  // A variable, so esbuild leaves the import to the browser instead of
  // bundling the editor into the app.
  const url = __DRAWING_JS__
  load = (import(url) as Promise<Editor>).catch((err: unknown) => {
    load = undefined // offline the first time: let the next open retry
    throw err
  })
  return load
}
