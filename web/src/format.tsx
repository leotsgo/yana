// The formatting bar that sits above the on-screen keyboard while a note
// is edited on a phone. Each button is a small markdown command on the
// CodeMirror view: wrap the selection, toggle a line prefix, insert a
// link. Buttons take no focus, so the keyboard stays up and the caret
// stays where it was.

import { useRef } from 'preact/hooks'
import { EditorSelection } from '@codemirror/state'
import type { EditorView } from '@codemirror/view'
import { yUndoManagerKeymap } from 'y-codemirror.next'

import type { Note } from './api'
import { Icon } from './icons'
import type { IconName } from './icons'
import { uploadInto } from './upload'

export interface FormatBarProps {
  view: EditorView | null
  note: Note
  onToast: (msg: string) => void
}

type Command = (view: EditorView) => void

const undo = yUndoManagerKeymap.find((k) => k.key === 'Mod-z')?.run
const redo = yUndoManagerKeymap.find((k) => k.key === 'Mod-Shift-z')?.run

/** Wraps each selection range in marks; an empty selection gets the
 * marks with the caret between them. Selecting text that is already
 * wrapped unwraps it. */
function wrap(marks: string): Command {
  return (view) => {
    const n = marks.length
    view.dispatch(
      view.state.changeByRange((range) => {
        const { from, to } = range
        const doc = view.state.doc
        const before = doc.sliceString(Math.max(0, from - n), from)
        const after = doc.sliceString(to, Math.min(doc.length, to + n))
        if (before === marks && after === marks) {
          return {
            changes: [
              { from: from - n, to: from },
              { from: to, to: to + n },
            ],
            range: EditorSelection.range(from - n, to - n),
          }
        }
        return {
          changes: [
            { from, insert: marks },
            { from: to, insert: marks },
          ],
          range: EditorSelection.range(from + n, to + n),
        }
      }),
    )
  }
}

/** The lines the selection touches, as one range of line numbers. */
function lineSpan(view: EditorView): { first: number; last: number } {
  const { from, to } = view.state.selection.main
  return { first: view.state.doc.lineAt(from).number, last: view.state.doc.lineAt(to).number }
}

/** Rewrites the start of every selected line. `next` sees the line's
 * text past any indentation and returns what should replace its marker. */
function prefixLines(next: (body: string) => { strip: number; add: string }): Command {
  return (view) => {
    const { first, last } = lineSpan(view)
    const changes = []
    for (let i = first; i <= last; i++) {
      const line = view.state.doc.line(i)
      const indent = /^\s*/.exec(line.text)?.[0].length ?? 0
      const { strip, add } = next(line.text.slice(indent))
      changes.push({ from: line.from + indent, to: line.from + indent + strip, insert: add })
    }
    view.dispatch({ changes, scrollIntoView: true })
  }
}

const bullet = /^([-*+]|\d+[.)])\s+/
const task = /^([-*+]|\d+[.)])\s+\[[ xX]\]\s*/
const heading = /^(#{1,6})\s+/
const quote = /^>\s?/

const commands: Record<string, Command> = {
  bold: wrap('**'),
  italic: wrap('_'),
  code: (view) => {
    const { from, to } = view.state.selection.main
    const text = view.state.doc.sliceString(from, to)
    if (text.includes('\n')) {
      // A block: fence it on its own lines.
      const start = view.state.doc.lineAt(from).from
      const end = view.state.doc.lineAt(to).to
      view.dispatch({
        changes: [
          { from: start, insert: '```\n' },
          { from: end, insert: '\n```' },
        ],
        selection: { anchor: start + 3 },
      })
      return
    }
    wrap('`')(view)
  },
  heading: prefixLines((body) => {
    const m = heading.exec(body)
    if (!m || !m[1]) return { strip: 0, add: '# ' }
    if (m[1].length >= 3) return { strip: m[0].length, add: '' }
    return { strip: m[0].length, add: '#'.repeat(m[1].length + 1) + ' ' }
  }),
  list: prefixLines((body) => {
    const t = task.exec(body)
    if (t) return { strip: t[0].length, add: '- ' }
    const b = bullet.exec(body)
    if (b) return { strip: b[0].length, add: '' }
    return { strip: 0, add: '- ' }
  }),
  task: prefixLines((body) => {
    const t = task.exec(body)
    if (t) return { strip: t[0].length, add: '' }
    const b = bullet.exec(body)
    if (b) return { strip: b[0].length, add: `${b[1]} [ ] ` }
    return { strip: 0, add: '- [ ] ' }
  }),
  quote: prefixLines((body) => {
    const q = quote.exec(body)
    return q ? { strip: q[0].length, add: '' } : { strip: 0, add: '> ' }
  }),
  link: (view) => {
    const { from, to } = view.state.selection.main
    const text = view.state.doc.sliceString(from, to)
    if (/^https?:\/\/\S+$/.test(text)) {
      // A bare URL: give it a label to fill in.
      view.dispatch({ changes: { from, to, insert: `[](${text})` }, selection: { anchor: from + 1 } })
      return
    }
    const insert = `[${text}](url)`
    view.dispatch({
      changes: { from, to, insert },
      selection: text ? EditorSelection.range(from + text.length + 3, from + text.length + 6) : { anchor: from + 1 },
    })
  },
  undo: (view) => {
    undo?.(view)
  },
  redo: (view) => {
    redo?.(view)
  },
}

const buttons: Array<{ id: string; icon: IconName; label: string }> = [
  { id: 'bold', icon: 'bold', label: 'Bold' },
  { id: 'italic', icon: 'italic', label: 'Italic' },
  { id: 'heading', icon: 'heading', label: 'Heading' },
  { id: 'list', icon: 'list', label: 'List' },
  { id: 'task', icon: 'check-square', label: 'Task' },
  { id: 'quote', icon: 'quote', label: 'Quote' },
  { id: 'code', icon: 'code', label: 'Code' },
  { id: 'link', icon: 'link', label: 'Link' },
  { id: 'image', icon: 'image', label: 'Image' },
  { id: 'undo', icon: 'undo', label: 'Undo' },
  { id: 'redo', icon: 'redo', label: 'Redo' },
]

export function FormatBar({ view, note, onToast }: FormatBarProps) {
  const file = useRef<HTMLInputElement>(null)

  const run = (id: string) => {
    if (!view) return
    if (id === 'image') {
      file.current?.click()
      return
    }
    commands[id]?.(view)
    view.focus()
  }

  return (
    <div class="format-bar" role="toolbar" aria-label="formatting">
      {buttons.map((b) => (
        <button
          key={b.id}
          type="button"
          class="format-btn"
          aria-label={b.label}
          title={b.label}
          tabIndex={-1}
          disabled={!view}
          // No focus change: the keyboard stays open and the caret stays put.
          onPointerDown={(ev) => ev.preventDefault()}
          onMouseDown={(ev) => ev.preventDefault()}
          onClick={() => run(b.id)}
        >
          <Icon name={b.icon} size={20} />
        </button>
      ))}
      <input
        ref={file}
        type="file"
        accept="image/*"
        multiple
        hidden
        onChange={(ev) => {
          const input = ev.currentTarget as HTMLInputElement
          const files = [...(input.files ?? [])]
          input.value = ''
          if (!view || files.length === 0) return
          void uploadInto(view, note, files, view.state.selection.main.head, onToast)
          view.focus()
        }}
      />
    </div>
  )
}
