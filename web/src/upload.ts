// Drag-and-drop and paste uploads. Each file goes to the note's sibling
// _assets/ directory through PUT /api/files; while it is in flight the
// document holds a placeholder link, which becomes the real image link
// once the server has answered with the name it actually wrote.

import type { EditorView } from '@codemirror/view'

import { api, ApiError, join } from './api'
import type { Note } from './api'

function safeName(name: string, type: string): string {
  let n = name.trim().replace(/[\\/:*?"<>| ]/g, '-').replace(/\s+/g, '-').replace(/-+/g, '-')
  n = n.replace(/^[.-]+/, '')
  if (n === '' || n === '.' || n === '..') {
    const stamp = new Date().toISOString().replace(/[-:]/g, '').replace(/\..+$/, '').replace('T', '-')
    n = `paste-${stamp}${extFor(type)}`
  }
  return n
}

function extFor(type: string): string {
  switch (type) {
    case 'image/png':
      return '.png'
    case 'image/jpeg':
      return '.jpg'
    case 'image/gif':
      return '.gif'
    case 'image/webp':
      return '.webp'
    case 'image/svg+xml':
      return '.svg'
    default:
      return ''
  }
}

function altFor(name: string): string {
  return name.replace(/\.[^.]+$/, '').replace(/[-_]+/g, ' ')
}

export async function uploadInto(
  view: EditorView,
  note: Note,
  files: File[],
  at: number,
  onToast: (msg: string) => void,
): Promise<void> {
  let pos = at
  for (const file of files) {
    // Pasted screenshots all arrive as "image.png"; give them a stamp.
    const pasted = /^image\.(png|jpe?g|gif|webp)$/i.test(file.name)
    const name = safeName(pasted ? '' : file.name, file.type)
    const isImage = file.type.startsWith('image/')
    const marker = `${isImage ? '!' : ''}[uploading ${name}](...)`
    view.dispatch({ changes: { from: pos, insert: marker } })
    pos += marker.length
    try {
      const res = await api.upload(join(note.base, '_assets/' + name), file)
      const link = `${isImage ? '!' : ''}[${isImage ? altFor(res.name) : res.name}](_assets/${res.name})`
      replaceMarker(view, marker, link)
      pos += link.length - marker.length
    } catch (err) {
      replaceMarker(view, marker, '')
      pos -= marker.length
      onToast(err instanceof ApiError ? err.message : `Could not upload ${name}.`)
    }
  }
}

// The marker may have moved (or gone) while the upload ran; find it by
// text rather than by remembered position.
function replaceMarker(view: EditorView, marker: string, replacement: string): void {
  const text = view.state.doc.toString()
  const idx = text.indexOf(marker)
  if (idx < 0) {
    if (replacement) view.dispatch({ changes: { from: view.state.selection.main.head, insert: replacement } })
    return
  }
  view.dispatch({ changes: { from: idx, to: idx + marker.length, insert: replacement } })
}
