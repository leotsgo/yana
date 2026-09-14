// The editing surface for a note: a plain textarea two-way bound to the
// note's Yjs text, plus a presence bar fed by awareness. Editing is live:
// every batched keystroke round-trips through the relay, remote edits splice
// into the textarea while you type, and remote writers show up with their
// name, colour, and cursor position. Phase 6 replaces this surface with a
// proper editor; the binding here is the honest minimum.

import type { Note } from './api'
import { h, clear } from './dom'
import { SyncClient, type PresenceState } from './sync'

export interface EditorHandle {
  destroy(): void
}

export function createEditor(
  container: HTMLElement,
  note: Note,
  onDone: () => void,
): EditorHandle {
  const sync = new SyncClient(note.id, {
    onStatus: renderStatus,
    onError(code) {
      if (code === 'rate_limited') {
        statusEl.textContent = 'Typing faster than the server allows; edits are kept and retried.'
      } else if (code === 'forbidden') {
        statusEl.textContent = 'This space is read-only for your account.'
        doneBtn.disabled = false
      }
    },
    onGone() {
      statusEl.textContent = 'This note moved or was deleted on disk. The view refreshes when you finish editing.'
    },
    onPresence: renderPresence,
  })

  const presenceEl = h('div', { class: 'presence', 'aria-label': 'who is editing' })
  const statusEl = h('span', { class: 'sync-status' }, 'connecting…')

  let tornDown = false
  function teardown(): void {
    if (tornDown) return
    tornDown = true
    sync.text.unobserve(onRemote)
    sync.destroy()
  }

  const doneBtn = h('button', { class: 'btn', onClick: () => { teardown(); onDone() } }, 'Done')
  doneBtn.disabled = true // until the first sync lands

  const textarea = h('textarea', {
    class: 'editor',
    spellcheck: 'true',
    'aria-label': `editing ${note.title}`,
  }) as HTMLTextAreaElement

  let lastSynced = sync.text.toString()
  textarea.value = lastSynced

  // --- local edits → CRDT ------------------------------------------------

  textarea.addEventListener('compositionstart', () => sync.setComposing(true))
  textarea.addEventListener('compositionend', () => {
    sync.setComposing(false)
    onInput()
  })

  function onInput(): void {
    if (sync.isComposing) return
    const value = textarea.value
    // Express the change against the last synced text as one delete plus
    // one insert: find the common prefix and suffix.
    let p = 0
    const maxP = Math.min(lastSynced.length, value.length)
    while (p < maxP && lastSynced[p] === value[p]) p++
    let s = 0
    const maxS = Math.min(lastSynced.length - p, value.length - p)
    while (s < maxS && lastSynced[lastSynced.length - 1 - s] === value[value.length - 1 - s]) s++
    const removed = lastSynced.length - p - s
    const inserted = value.slice(p, value.length - s)
    if (removed === 0 && inserted === '') {
      lastSynced = value
      return
    }
    sync.doc.transact(() => {
      if (removed > 0) sync.text.delete(p, removed)
      if (inserted !== '') sync.text.insert(p, inserted)
    })
    lastSynced = sync.text.toString()
  }
  textarea.addEventListener('input', onInput)

  // --- remote edits → textarea -------------------------------------------

  function onRemote(): void {
    const want = sync.text.toString()
    if (want === textarea.value || sync.isComposing) {
      lastSynced = want
      return
    }
    // Splice the remote change into what the textarea shows, keeping the
    // local caret sensible when the change is before it.
    const old = textarea.value
    let p = 0
    const maxP = Math.min(old.length, want.length)
    while (p < maxP && old[p] === want[p]) p++
    let s = 0
    const maxS = Math.min(old.length - p, want.length - p)
    while (s < maxS && old[old.length - 1 - s] === want[want.length - 1 - s]) s++
    const start = p
    const end = old.length - s
    const removed = end - start
    const inserted = want.slice(p, want.length - s)
    const selStart = textarea.selectionStart
    const selEnd = textarea.selectionEnd
    textarea.setRangeText(inserted, start, end, 'end')
    if (document.activeElement === textarea) {
      const adj = (pos: number): number => {
        if (pos <= start) return pos
        if (pos >= end) return pos - removed + inserted.length
        return start + inserted.length
      }
      textarea.setSelectionRange(adj(selStart), adj(selEnd))
    }
    lastSynced = want
  }
  sync.text.observe(onRemote)

  // --- presence ----------------------------------------------------------

  function lineCol(value: string, index: number): { line: number; col: number } {
    const before = value.slice(0, Math.min(index, value.length))
    const lines = before.split('\n')
    return { line: lines.length, col: (lines[lines.length - 1] ?? '').length + 1 }
  }

  function pushCursor(): void {
    const state = sync.awareness.getLocalState() as PresenceState | null
    const user = (state?.user) ?? { name: 'me', color: '#666' }
    if (document.activeElement !== textarea) {
      sync.awareness.setLocalState({ user, cursor: null })
      return
    }
    const index = textarea.selectionStart
    const { line, col } = lineCol(textarea.value, index)
    sync.awareness.setLocalState({
      user,
      cursor: { line, col, index, length: textarea.selectionEnd - textarea.selectionStart },
    })
  }
  for (const ev of ['keyup', 'click', 'select', 'focus', 'blur'] as const) {
    textarea.addEventListener(ev, pushCursor)
  }

  function renderPresence(): void {
    clear(presenceEl)
    const mine = (sync.awareness.getLocalState() as PresenceState | null)?.user
    const entries: Array<{ key: string; name: string; color: string; where: string }> = []
    if (mine) entries.push({ key: 'me', name: mine.name, color: mine.color, where: 'you' })
    for (const [clientID, raw] of sync.awareness.getStates()) {
      if (clientID === sync.awareness.clientID) continue
      const state = raw as PresenceState
      const name = state?.user?.name ?? 'someone'
      const color = state?.user?.color ?? '#666'
      const where = state?.cursor ? `${state.cursor.line}:${state.cursor.col}` : 'viewing'
      entries.push({ key: String(clientID), name, color, where })
    }
    for (const e of entries) {
      presenceEl.append(
        h(
          'span',
          { class: 'presence-chip', title: `${e.name} — ${e.where}` },
          h('span', { class: 'presence-dot', style: `background:${e.color}` }),
          `${e.name} ${e.where === 'you' ? '(you)' : e.where}`,
        ),
      )
    }
  }

  function renderStatus(status: 'connecting' | 'synced' | 'offline'): void {
    doneBtn.disabled = false
    switch (status) {
      case 'synced':
        statusEl.textContent = 'live'
        statusEl.className = 'sync-status ok'
        break
      case 'connecting':
        statusEl.textContent = 'connecting…'
        statusEl.className = 'sync-status'
        break
      case 'offline':
        statusEl.textContent = 'offline — edits are kept and merge on reconnect'
        statusEl.className = 'sync-status offline'
        break
    }
  }

  // --- layout ------------------------------------------------------------

  const toolbar = h(
    'div',
    { class: 'editor-toolbar' },
    h('span', { class: 'editor-title' }, note.title),
    presenceEl,
    h('span', { class: 'spacer' }),
    statusEl,
    doneBtn,
  )
  clear(container)
  container.append(h('div', { class: 'editor-wrap' }, toolbar, textarea))
  textarea.focus()
  textarea.setSelectionRange(textarea.value.length, textarea.value.length)
  pushCursor()
  renderPresence()

  return { destroy: teardown }
}
