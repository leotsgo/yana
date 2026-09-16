// Hide-the-syntax editing, behind a preference. On lines the caret is not
// on, the marks that shape the text (`#`, `**`, `_`, backticks, `~~`, the
// brackets and target of a link) are collapsed so the source reads close
// to the render. Move the caret onto a line and it shows its marks again.
// Block-level marks (list bullets, quotes, fences) stay: they carry
// structure the reader needs to see.

import { syntaxTree } from '@codemirror/language'
import { RangeSetBuilder } from '@codemirror/state'
import { Decoration, EditorView, ViewPlugin } from '@codemirror/view'
import type { DecorationSet, ViewUpdate } from '@codemirror/view'

const hide = Decoration.replace({})
const marks = new Set(['HeaderMark', 'EmphasisMark', 'CodeMark', 'StrikethroughMark', 'LinkMark', 'URL'])
const blocks = new Set(['FencedCode', 'CodeBlock', 'HTMLBlock'])

function build(view: EditorView): DecorationSet {
  const doc = view.state.doc
  const active = new Set<number>()
  for (const r of view.state.selection.ranges) {
    const last = doc.lineAt(r.to).number
    for (let l = doc.lineAt(r.from).number; l <= last; l++) active.add(l)
  }
  const found: Array<{ from: number; to: number }> = []
  for (const { from, to } of view.visibleRanges) {
    syntaxTree(view.state).iterate({
      from,
      to,
      enter(node) {
        if (blocks.has(node.name)) return false
        if (!marks.has(node.name)) return undefined
        if (active.has(doc.lineAt(node.from).number)) return undefined
        const parent = node.node.parent
        // Backticks only around inline code; fences keep theirs.
        if (node.name === 'CodeMark' && parent?.name !== 'InlineCode') return undefined
        // Only a link that carries its target inline collapses to its
        // text. A bare [reference] and a [[wikilink]] keep their brackets.
        if ((node.name === 'URL' || node.name === 'LinkMark') && !(parent && (parent.name === 'Link' || parent.name === 'Image') && parent.getChild('URL'))) {
          return undefined
        }
        let end = node.to
        // A heading's marks take the space after them along.
        if (node.name === 'HeaderMark' && doc.sliceString(end, end + 1) === ' ') end++
        if (end > node.from) found.push({ from: node.from, to: end })
        return undefined
      },
    })
  }
  found.sort((a, b) => a.from - b.from)
  const builder = new RangeSetBuilder<Decoration>()
  let last = -1
  for (const r of found) {
    if (r.from < last) continue
    builder.add(r.from, r.to, hide)
    last = r.to
  }
  return builder.finish()
}

export function livePreview() {
  return ViewPlugin.fromClass(
    class {
      decorations: DecorationSet
      constructor(view: EditorView) {
        this.decorations = build(view)
      }
      update(u: ViewUpdate) {
        if (u.docChanged || u.selectionSet || u.viewportChanged || syntaxTree(u.startState) !== syntaxTree(u.state)) {
          this.decorations = build(u.view)
        }
      }
    },
    { decorations: (v) => v.decorations },
  )
}
