// The formatting buttons: a bar above the on-screen keyboard while a
// note is edited on a phone, and a compact row in the toolbar on a
// desktop, so nobody has to know the markdown to use it. Each button is
// a small command on the CodeMirror view: wrap the selection, toggle a
// line prefix, start a link. Buttons take no focus, so the keyboard
// stays up and the caret stays where it was.

import { useRef } from 'preact/hooks'
import { startCompletion } from '@codemirror/autocomplete'
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
  /** The toolbar row on a desktop: icons only, no undo and redo. */
  compact?: boolean
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
    if (text) {
      // Selected words become the name of the note to link to.
      view.dispatch({ changes: { from, to, insert: `[[${text}]]` }, selection: { anchor: from + text.length + 4 } })
      return
    }
    // Nothing selected: open the brackets and offer the notes to pick from.
    view.dispatch({ changes: { from, insert: '[[' }, selection: { anchor: from + 2 } })
    startCompletion(view)
  },
  tag: (view) => {
    const { from, to } = view.state.selection.main
    const text = view.state.doc.sliceString(from, to)
    const prev = from > 0 ? view.state.doc.sliceString(from - 1, from) : ''
    const lead = prev === '' || /\s/.test(prev) ? '' : ' '
    if (text) {
      const tag = text.trim().replace(/\s+/g, '-')
      view.dispatch({ changes: { from, to, insert: `${lead}#${tag}` }, selection: { anchor: from + lead.length + tag.length + 1 } })
      return
    }
    view.dispatch({ changes: { from, insert: `${lead}#` }, selection: { anchor: from + lead.length + 1 } })
    startCompletion(view)
  },
  undo: (view) => {
    undo?.(view)
  },
  redo: (view) => {
    redo?.(view)
  },
}

const buttons: Array<{ id: string; icon: IconName; label: string; phoneOnly?: boolean }> = [
  { id: 'bold', icon: 'bold', label: 'Bold' },
  { id: 'italic', icon: 'italic', label: 'Italic' },
  { id: 'heading', icon: 'heading', label: 'Heading' },
  { id: 'list', icon: 'list', label: 'List' },
  { id: 'task', icon: 'check-square', label: 'Task: a box to tick' },
  { id: 'quote', icon: 'quote', label: 'Quote' },
  { id: 'code', icon: 'code', label: 'Code' },
  { id: 'link', icon: 'link', label: 'Link to a note' },
  { id: 'image', icon: 'paperclip', label: 'Attach a file' },
  { id: 'tag', icon: 'tag', label: 'Tag' },
  { id: 'undo', icon: 'undo', label: 'Undo', phoneOnly: true },
  { id: 'redo', icon: 'redo', label: 'Redo', phoneOnly: true },
]

export function FormatBar({ view, note, onToast, compact }: FormatBarProps) {
  const file = useRef<HTMLInputElement>(null)
  const shown = compact ? buttons.filter((b) => !b.phoneOnly) : buttons

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
    <div class={compact ? 'format-bar compact' : 'format-bar'} role="toolbar" aria-label="formatting">
      {shown.map((b) => (
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
