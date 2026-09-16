// The sidebar tree. Pinned notes and folders sit at the top, then each
// space. Notes and folders drag: dropping one on a directory, a space, or
// another note moves it there (a rename on disk, with link propagation
// on the server). Dropping a note into the editor inserts a wikilink; the
// drag payload carries id, path and title for that. Every row has a
// context menu — right-click or the ⋯ that shows on hover on a desktop,
// a long press on a phone — which the shell fills in (tree actions).

import { useEffect, useRef, useState } from 'preact/hooks'

import type { SpaceTree, TreeNode } from './api'
import { baseOf, dirOf } from './api'
import { Icon } from './icons'
import { coarsePointer } from './layout'
import type { Pin } from './prefs'

// Directory open/closed state survives re-renders within a session.
const collapsed = new Set<string>()

export const NOTE_DRAG = 'text/yana-note'
export const DIR_DRAG = 'text/yana-dir'

/** What a tree action acts on. */
export type TreeTarget =
  | { kind: 'note'; node: TreeNode }
  | { kind: 'dir'; node: TreeNode }
  | { kind: 'space'; name: string }

export interface TreeProps {
  spaces: SpaceTree[]
  selected: string | null
  pins: Pin[]
  onOpen: (id: string) => void
  onMove: (id: string, from: string, toDir: string) => void
  onMoveDir: (path: string, toDir: string) => void
  onNew: (dir: string) => void
  /** Open the actions for a row, at the pointer when there is one. */
  onContext: (target: TreeTarget, anchor: HTMLElement, at?: { x: number; y: number }) => void
}

const LONG_PRESS_MS = 450

export function Tree({ spaces, selected, pins, onOpen, onMove, onMoveDir, onNew, onContext }: TreeProps) {
  const [dropOn, setDropOn] = useState<string | null>(null)
  const [, bump] = useState(0)
  // A long press opens the menu and swallows the click that follows it.
  const press = useRef<{ timer: number; x: number; y: number; fired: boolean } | null>(null)

  function dragProps(dir: string) {
    return {
      onDragOver: (ev: DragEvent) => {
        const types = ev.dataTransfer?.types
        if (!types || (!types.includes(NOTE_DRAG) && !types.includes(DIR_DRAG))) return
        ev.preventDefault()
        ev.stopPropagation()
        ev.dataTransfer.dropEffect = 'move'
        setDropOn(dir)
      },
      onDragLeave: (ev: DragEvent) => {
        if ((ev.currentTarget as HTMLElement).contains(ev.relatedTarget as Node | null)) return
        setDropOn((d) => (d === dir ? null : d))
      },
      onDrop: (ev: DragEvent) => {
        setDropOn(null)
        const note = ev.dataTransfer?.getData(NOTE_DRAG)
        const folder = ev.dataTransfer?.getData(DIR_DRAG)
        if (!note && !folder) return
        ev.preventDefault()
        ev.stopPropagation()
        try {
          if (note) {
            const { id, path } = JSON.parse(note) as { id: string; path: string }
            if (dirOf(path) === dir) return
            onMove(id, path, dir)
          } else if (folder) {
            const { path } = JSON.parse(folder) as { path: string }
            if (dirOf(path) === dir || path === dir || dir.startsWith(path + '/')) return
            onMoveDir(path, dir)
          }
        } catch {
          // not ours
        }
      },
    }
  }

  // Right-click on a desktop; a long press on a touch screen (Android
  // also fires contextmenu for one, which wins and cancels the timer).
  function contextProps(target: TreeTarget) {
    return {
      onContextMenu: (ev: MouseEvent) => {
        ev.preventDefault()
        ev.stopPropagation()
        cancelPress()
        onContext(target, ev.currentTarget as HTMLElement, { x: ev.clientX, y: ev.clientY })
      },
      onPointerDown: (ev: PointerEvent) => {
        if (ev.pointerType === 'mouse') return
        // The row's press, not its section's: one timer per touch.
        ev.stopPropagation()
        cancelPress()
        const el = ev.currentTarget as HTMLElement
        const timer = window.setTimeout(() => {
          if (press.current) press.current.fired = true
          onContext(target, el)
        }, LONG_PRESS_MS)
        press.current = { timer, x: ev.clientX, y: ev.clientY, fired: false }
      },
      onPointerMove: (ev: PointerEvent) => {
        const p = press.current
        if (p && !p.fired && Math.hypot(ev.clientX - p.x, ev.clientY - p.y) > 10) cancelPress()
      },
      onPointerUp: () => {
        const p = press.current
        if (p && !p.fired) cancelPress()
      },
      onPointerCancel: cancelPress,
      onClickCapture: (ev: MouseEvent) => {
        const p = press.current
        if (p?.fired) {
          ev.preventDefault()
          ev.stopPropagation()
          press.current = null
        }
      },
    }
  }

  function cancelPress(): void {
    const p = press.current
    if (p && !p.fired) {
      window.clearTimeout(p.timer)
      press.current = null
    }
  }

  useEffect(() => cancelPress, [])

  function moreButton(target: TreeTarget, label: string) {
    if (coarsePointer) return null
    return (
      <button
        type="button"
        class="tree-more"
        title={label}
        aria-label={label}
        tabIndex={-1}
        onClick={(ev) => {
          ev.preventDefault()
          ev.stopPropagation()
          onContext(target, ev.currentTarget as HTMLElement)
        }}
      >
        <Icon name="more" size={14} />
      </button>
    )
  }

  function noteRow(n: TreeNode) {
    const id = n.id ?? ''
    const target: TreeTarget = { kind: 'note', node: n }
    return (
      <a
        key={n.path}
        class={'tree-note' + (id === selected ? ' selected' : '') + (dropOn === dirOf(n.path) ? ' drop' : '')}
        href={`/n/${id}`}
        title={n.path}
        draggable={!coarsePointer}
        onDragStart={(ev) => {
          ev.dataTransfer?.setData(NOTE_DRAG, JSON.stringify({ id, path: n.path, title: n.title ?? n.name }))
          ev.dataTransfer?.setData('text/plain', `[[${n.name.replace(/\.(md|markdown|html?)$/i, '')}]]`)
          if (ev.dataTransfer) ev.dataTransfer.effectAllowed = 'copyMove'
        }}
        onClick={(ev) => {
          ev.preventDefault()
          if (id) onOpen(id)
        }}
        {...dragProps(dirOf(n.path))}
        {...contextProps(target)}
      >
        <span class="tree-title">{n.title || n.name}</span>
        {n.kind === 'html' && <span class="tree-kind">html</span>}
        {moreButton(target, `Actions for ${n.title || n.name}`)}
      </a>
    )
  }

  function dirRow(n: TreeNode, depth: number) {
    const isOpen = !collapsed.has(n.path)
    const target: TreeTarget = { kind: 'dir', node: n }
    const empty = (n.children ?? []).length === 0
    return (
      <div key={n.path} class="tree-dirwrap" style={`--depth:${depth}`}>
        <button
          type="button"
          class={'tree-dir' + (isOpen ? ' open' : '') + (dropOn === n.path ? ' drop' : '')}
          title={n.path}
          draggable={!coarsePointer}
          onDragStart={(ev) => {
            ev.dataTransfer?.setData(DIR_DRAG, JSON.stringify({ path: n.path }))
            if (ev.dataTransfer) ev.dataTransfer.effectAllowed = 'move'
          }}
          onClick={() => {
            if (collapsed.has(n.path)) collapsed.delete(n.path)
            else collapsed.add(n.path)
            bump((x) => x + 1)
          }}
          {...dragProps(n.path)}
          {...contextProps(target)}
        >
          <Icon name="chevron-right" class="tree-caret" size={14} />
          <Icon name={isOpen ? 'folder-open' : 'folder'} class="tree-folder" size={15} />
          <span class="tree-title">{n.name}</span>
          {moreButton(target, `Actions for ${n.name}`)}
        </button>
        <div class="tree-children" hidden={!isOpen}>
          {empty ? (
            <button type="button" class="tree-empty-dir" onClick={() => onNew(n.path)}>
              <Icon name="plus" size={14} />
              New note here
            </button>
          ) : (
            (n.children ?? []).map((c) => render(c, depth + 1))
          )}
        </div>
      </div>
    )
  }

  function render(n: TreeNode, depth: number) {
    return n.type === 'dir' ? dirRow(n, depth) : noteRow(n)
  }

  if (spaces.length === 0) {
    return (
      <div class="empty">
        <p>No notes yet. Start one, or drop a markdown file into the notes directory; it shows up on the next scan.</p>
        <button type="button" class="btn" onClick={() => onNew('')}>
          <Icon name="plus" />
          New note
        </button>
      </div>
    )
  }

  // Pins resolve against the live tree: a pinned note that was deleted,
  // or a folder that was moved away, simply does not show.
  const pinned = pins
    .map((p) => (p.kind === 'note' ? findNote(spaces, p.id) : findDir(spaces, p.path)))
    .filter((n): n is TreeNode => n !== null)

  return (
    <>
      {pinned.length > 0 && (
        <section class="space pinned" aria-label="pinned">
          <h2 class="space-name">
            <Icon name="pin" size={12} />
            Pinned
          </h2>
          {pinned.map((n) => render(n, 0))}
        </section>
      )}
      {spaces.map((s) => (
        <section
          key={s.name}
          class={'space' + (dropOn === s.name ? ' drop' : '')}
          {...dragProps(s.name)}
          {...contextProps({ kind: 'space', name: s.name })}
        >
          <h2 class="space-name" title={`${s.notes} notes`}>
            {s.name === '' ? '/' : s.name + '/'}
            <span class="space-count">{s.notes}</span>
            <button type="button" class="tree-add" title={`New note in ${s.name || 'the root'}`} aria-label={`New note in ${s.name || 'the root'}`} onClick={() => onNew(s.name)}>
              <Icon name="plus" size={14} />
            </button>
          </h2>
          {s.children.map((c) => render(c, 0))}
        </section>
      ))}
    </>
  )
}

export interface FlatNote {
  id: string
  path: string
  title: string
  name: string
  tags: string[]
}

/** Every note in the tree, flat, for the quick switcher. */
export function flatten(spaces: SpaceTree[]): FlatNote[] {
  const out: FlatNote[] = []
  const walk = (n: TreeNode) => {
    if (n.type === 'note' && n.id) out.push({ id: n.id, path: n.path, title: n.title || baseOf(n.path), name: n.name, tags: n.tags ?? [] })
    for (const c of n.children ?? []) walk(c)
  }
  for (const s of spaces) for (const c of s.children) walk(c)
  return out
}

/** Every directory in the tree, flat, for the move picker: the spaces
 * themselves first, then each folder by path. */
export function folders(spaces: SpaceTree[]): Array<{ path: string; depth: number }> {
  const out: Array<{ path: string; depth: number }> = []
  const walk = (n: TreeNode, depth: number) => {
    if (n.type !== 'dir') return
    out.push({ path: n.path, depth })
    for (const c of n.children ?? []) walk(c, depth + 1)
  }
  for (const s of spaces) {
    out.push({ path: s.name, depth: 0 })
    for (const c of s.children) walk(c, 1)
  }
  return out
}

function findNote(spaces: SpaceTree[], id: string): TreeNode | null {
  let found: TreeNode | null = null
  const walk = (n: TreeNode) => {
    if (found) return
    if (n.type === 'note' && n.id === id) found = n
    for (const c of n.children ?? []) walk(c)
  }
  for (const s of spaces) for (const c of s.children) walk(c)
  return found
}

function findDir(spaces: SpaceTree[], path: string): TreeNode | null {
  let found: TreeNode | null = null
  const walk = (n: TreeNode) => {
    if (found || n.type !== 'dir') return
    if (n.path === path) found = n
    for (const c of n.children ?? []) walk(c)
  }
  for (const s of spaces) for (const c of s.children) walk(c)
  return found
}
