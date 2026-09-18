// The editing surface: CodeMirror 6 bound to the note's Yjs text through
// y-codemirror.next. Local edits are transactions tagged with the binding's
// own origin; remote updates arrive tagged 'remote'. The undo manager only
// tracks the former, which is what keeps undo in one tab from reverting
// text typed in another. Files dropped or pasted in upload to the note's
// sibling _assets/ directory and become image links. Typing `[[` offers
// the notes of the space to link to; `#` offers the tags in use.

import { useEffect, useMemo, useRef } from 'preact/hooks'
import { autocompletion, startCompletion } from '@codemirror/autocomplete'
import type { Completion, CompletionContext, CompletionResult } from '@codemirror/autocomplete'
import { Compartment, EditorState, Prec } from '@codemirror/state'
import { EditorView, drawSelection, highlightActiveLine, keymap, placeholder } from '@codemirror/view'
import { defaultKeymap, indentWithTab } from '@codemirror/commands'
import { markdown, markdownKeymap, markdownLanguage } from '@codemirror/lang-markdown'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { highlightSelectionMatches, searchKeymap } from '@codemirror/search'
import { tags } from '@lezer/highlight'
import { yCollab, yUndoManagerKeymap } from 'y-codemirror.next'

import type { Note } from './api'
import { livePreview } from './live'
import type { SyncClient } from './sync'
import { uploadInto } from './upload'

/** A note the editor can link to: the wikilink target that resolves to
 * it, the title to show, and the path to tell twins apart. */
export interface LinkTarget {
  target: string
  title: string
  path: string
}

export interface Completions {
  notes: LinkTarget[]
  tags: string[]
}

export interface EditorProps {
  sync: SyncClient
  note: Note
  /** What `[[` and `#` can complete to, read when the popup opens. */
  lookup: () => Completions
  readOnly: boolean
  /** Focus the editor when it mounts. */
  autofocus: boolean
  /** Put the caret at the end of the document (a new note, ready to type). */
  atEnd: boolean
  /** True on a phone: no autocorrect fighting the markdown, larger caret room. */
  phone: boolean
  /** Hide the marks on lines the caret is not on. */
  live: boolean
  onToast: (msg: string) => void
  /** Escape with nothing else to close: the page leaves edit mode. */
  onDone: () => void
  /** The view, once built, for the formatting bar; null when it goes. */
  onView: (view: EditorView | null) => void
}

// Markdown styling in the warm palette: structure is visible, the marks
// themselves fade back, code sits in mono.
const markdownStyle = HighlightStyle.define([
  { tag: tags.heading1, fontSize: '1.55em', fontWeight: '650', lineHeight: '1.3' },
  { tag: tags.heading2, fontSize: '1.3em', fontWeight: '650', lineHeight: '1.3' },
  { tag: tags.heading3, fontSize: '1.12em', fontWeight: '650' },
  { tag: [tags.heading4, tags.heading5, tags.heading6], fontWeight: '650' },
  { tag: tags.emphasis, fontStyle: 'italic' },
  { tag: tags.strong, fontWeight: '650' },
  { tag: tags.strikethrough, textDecoration: 'line-through', color: 'var(--ink-3)' },
  { tag: [tags.link, tags.url], color: 'var(--accent)' },
  { tag: tags.monospace, fontFamily: 'var(--mono)', fontSize: '0.92em', background: 'var(--bg-3)', borderRadius: 'var(--r-sm)' },
  { tag: tags.quote, color: 'var(--ink-2)' },
  { tag: [tags.processingInstruction, tags.meta, tags.labelName, tags.contentSeparator], color: 'var(--ink-3)' },
  { tag: tags.list, color: 'var(--accent)' },
  { tag: tags.escape, color: 'var(--ink-3)' },
])

const theme = EditorView.theme({
  '&': { height: '100%', background: 'transparent', color: 'var(--ink)' },
  '.cm-scroller': { fontFamily: 'var(--sans)', fontSize: 'var(--editor-size)', lineHeight: '1.65', overflow: 'auto' },
  '.cm-content': { padding: '18px 0 40vh', caretColor: 'var(--ink)', maxWidth: 'var(--measure)' },
  '.cm-line': { padding: '0 var(--page-x)', overflowWrap: 'anywhere' },
  '&.cm-focused': { outline: 'none' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--ink)', borderLeftWidth: '2px' },
  '.cm-activeLine': { background: 'var(--hover)' },
  '&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground': {
    background: 'var(--accent-soft)',
  },
  '.cm-selectionMatch': { background: 'var(--mark)' },
  '.cm-placeholder': { color: 'var(--ink-3)', fontStyle: 'normal' },
  '.cm-panels': { background: 'var(--bg-2)', color: 'var(--ink)', borderColor: 'var(--line)' },
  '.cm-panels.cm-panels-top': { borderBottom: '1px solid var(--line)' },
  '.cm-panel input, .cm-panel button': { font: '13px var(--sans)', color: 'var(--ink)' },
  '.cm-panel input': { background: 'var(--bg)', border: '1px solid var(--line)', borderRadius: 'var(--r-sm)' },
  '.cm-searchMatch': { background: 'var(--mark)' },
  '.cm-searchMatch.cm-searchMatch-selected': { background: 'var(--accent-soft)', outline: '1px solid var(--accent)' },
  '.cm-ySelectionInfo': { fontFamily: 'var(--mono)', fontSize: '10px', padding: '1px 4px', borderRadius: '2px', opacity: '1' },
  '.cm-ySelectionCaret': { marginLeft: '-1px' },
  '.cm-tooltip': { background: 'var(--bg)', color: 'var(--ink)', border: '1px solid var(--line)', borderRadius: 'var(--r-sm)', boxShadow: 'var(--shadow-2)' },
  '.cm-tooltip.cm-tooltip-autocomplete > ul': { fontFamily: 'var(--sans)', fontSize: '13.5px', maxHeight: '14em', minWidth: '16em' },
  '.cm-tooltip.cm-tooltip-autocomplete > ul > li': { padding: '5px 10px', lineHeight: '1.3' },
  '.cm-tooltip.cm-tooltip-autocomplete > ul > li[aria-selected]': { background: 'var(--accent-soft)', color: 'var(--ink)' },
  '.cm-completionLabel': { color: 'var(--ink)' },
  '.cm-completionMatchedText': { textDecoration: 'none', fontWeight: '650' },
  '.cm-completionDetail': { fontFamily: 'var(--mono)', fontSize: '11px', color: 'var(--ink-3)', marginLeft: '10px', fontStyle: 'normal' },
})

// `[[` up to the caret, with no closing bracket or bar since, is a
// wikilink being typed. The options come from the lookup, so a note
// made a moment ago is offered too.
function wikilinks(lookup: () => Completions) {
  return (ctx: CompletionContext): CompletionResult | null => {
    const line = ctx.state.doc.lineAt(ctx.pos)
    const before = line.text.slice(0, ctx.pos - line.from)
    const m = /\[\[([^\[\]|]*)$/.exec(before)
    if (!m) return null
    const typed = m[1] ?? ''
    const after = line.text.slice(ctx.pos - line.from)
    const closed = after.startsWith(']]')
    const options: Completion[] = lookup().notes.map((n) => ({
      label: n.title,
      detail: n.path,
      apply: (view, _c, from, to) => {
        const insert = closed ? n.target : n.target + ']]'
        const anchor = from + n.target.length + 2
        view.dispatch({ changes: { from, to, insert }, selection: { anchor: closed ? from + n.target.length : anchor } })
      },
    }))
    if (options.length === 0) return null
    return { from: ctx.pos - typed.length, options, validFor: /^[^\[\]|]*$/ }
  }
}

// `#` at the start of a word is a tag; offer the ones already in use.
function hashtags(lookup: () => Completions) {
  return (ctx: CompletionContext): CompletionResult | null => {
    const line = ctx.state.doc.lineAt(ctx.pos)
    const before = line.text.slice(0, ctx.pos - line.from)
    const m = /(^|\s)#([\w-]*)$/.exec(before)
    if (!m) return null
    const typed = m[2] ?? ''
    const options: Completion[] = lookup().tags.map((t) => ({
      label: t,
      apply: (view, _c, from, to) => view.dispatch({ changes: { from, to, insert: t }, selection: { anchor: from + t.length } }),
    }))
    if (options.length === 0) return null
    return { from: ctx.pos - typed.length, options, validFor: /^[\w-]*$/ }
  }
}

export function Editor({ sync, note, lookup, readOnly, autofocus, atEnd, phone, live, onToast, onDone, onView }: EditorProps) {
  const host = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const look = useRef(lookup)
  look.current = lookup
  const readOnlyConf = useMemo(() => new Compartment(), [])
  const liveConf = useMemo(() => new Compartment(), [])
  const done = useRef(onDone)
  done.current = onDone

  useEffect(() => {
    const el = host.current
    if (!el) return
    const uploads = EditorView.domEventHandlers({
      drop(ev, view) {
        const dt = ev.dataTransfer
        if (!dt) return false
        const pos = view.posAtCoords({ x: ev.clientX, y: ev.clientY }) ?? view.state.selection.main.head
        const link = dt.getData('text/yana-note')
        if (link) {
          // A note dragged out of the sidebar becomes a wikilink.
          ev.preventDefault()
          try {
            const { path } = JSON.parse(link) as { path: string }
            const name = path.slice(path.lastIndexOf('/') + 1).replace(/\.(md|markdown|html?)$/i, '')
            view.dispatch({ changes: { from: pos, insert: `[[${name}]]` }, selection: { anchor: pos + name.length + 4 } })
          } catch {
            // not ours
          }
          return true
        }
        if (dt.files.length === 0) return false
        ev.preventDefault()
        void uploadInto(view, note, [...dt.files], pos, onToast)
        return true
      },
      paste(ev, view) {
        const files = [...(ev.clipboardData?.files ?? [])].filter((f) => f.type.startsWith('image/'))
        if (files.length === 0) return false
        ev.preventDefault()
        void uploadInto(view, note, files, view.state.selection.main.head, onToast)
        return true
      },
    })
    const state = EditorState.create({
      doc: sync.text.toString(),
      extensions: [
        readOnlyConf.of([EditorState.readOnly.of(readOnly), EditorView.editable.of(!readOnly)]),
        liveConf.of(live ? livePreview() : []),
        autocompletion({
          override: [wikilinks(() => look.current()), hashtags(() => look.current())],
          icons: false,
          activateOnTyping: true,
          // The popup follows the text; nothing else is completed.
          defaultKeymap: true,
        }),
        keymap.of([{ key: 'Mod-Space', run: startCompletion }]),
        keymap.of([...yUndoManagerKeymap, ...markdownKeymap, ...searchKeymap, indentWithTab, ...defaultKeymap]),
        // After the search panel and the default keymap have had their
        // turn, a bare Escape hands the page back to the read view.
        Prec.lowest(
          keymap.of([
            {
              key: 'Escape',
              run: () => {
                done.current()
                return true
              },
            },
          ]),
        ),
        markdown({ base: markdownLanguage }),
        syntaxHighlighting(markdownStyle),
        EditorView.lineWrapping,
        drawSelection(),
        highlightActiveLine(),
        highlightSelectionMatches(),
        placeholder('Start writing.'),
        // Autocorrect rewrites paths, code and link targets on a phone;
        // spellcheck only underlines, so it stays on everywhere.
        EditorView.contentAttributes.of({
          spellcheck: 'true',
          autocorrect: phone ? 'off' : 'on',
          autocapitalize: 'sentences',
        }),
        theme,
        uploads,
        yCollab(sync.text, sync.awareness),
      ],
    })
    const view = new EditorView({ state, parent: el })
    viewRef.current = view
    onView(view)
    if (atEnd) {
      // A new note opens with the caret after its heading, ready to type.
      const end = view.state.doc.length
      view.dispatch({ selection: { anchor: end }, scrollIntoView: true })
    }
    if (autofocus) view.focus()
    return () => {
      viewRef.current = null
      onView(null)
      view.destroy()
    }
    // The editor is built once per document; the props that could change
    // (readOnly, live) are pushed through compartments below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sync])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    view.dispatch({
      effects: readOnlyConf.reconfigure([EditorState.readOnly.of(readOnly), EditorView.editable.of(!readOnly)]),
    })
  }, [readOnly])

  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    view.dispatch({ effects: liveConf.reconfigure(live ? livePreview() : []) })
  }, [live])

  // Switching from split to edit keeps the editor; it takes focus then.
  useEffect(() => {
    if (autofocus) viewRef.current?.focus()
  }, [autofocus])

  return <div class="editor" ref={host} />
}
