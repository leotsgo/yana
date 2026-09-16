// The sidebar tree. Notes drag: dropping one on a directory, a space, or
// another note moves it there (a rename on disk, with link propagation
// on the server). Dropping one into the editor inserts a wikilink; the
// drag payload carries id, path and title for that.

import { useState } from 'preact/hooks'

import type { SpaceTree, TreeNode } from './api'
import { baseOf, dirOf } from './api'
import { Icon } from './icons'
import { coarsePointer } from './layout'

// Directory open/closed state survives re-renders within a session.
const collapsed = new Set<string>()

export const NOTE_DRAG = 'text/yana-note'

export interface TreeProps {
  spaces: SpaceTree[]
  selected: string | null
  onOpen: (id: string) => void
  onMove: (id: string, from: string, toDir: string) => void
  onNew: (space: string) => void
}

export function Tree({ spaces, selected, onOpen, onMove, onNew }: TreeProps) {
  const [dropOn, setDropOn] = useState<string | null>(null)
  const [, bump] = useState(0)

  function dragProps(dir: string) {
    return {
      onDragOver: (ev: DragEvent) => {
        if (!ev.dataTransfer?.types.includes(NOTE_DRAG)) return
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
        const raw = ev.dataTransfer?.getData(NOTE_DRAG)
        if (!raw) return
        ev.preventDefault()
        ev.stopPropagation()
        try {
          const { id, path } = JSON.parse(raw) as { id: string; path: string }
          if (dirOf(path) === dir) return
          onMove(id, path, dir)
        } catch {
          // not ours
        }
      },
    }
  }

  function noteRow(n: TreeNode) {
    const id = n.id ?? ''
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
      >
        <span class="tree-title">{n.title || n.name}</span>
        {n.kind === 'html' && <span class="tree-kind">html</span>}
      </a>
    )
  }

  function dirRow(n: TreeNode, depth: number) {
    const isOpen = !collapsed.has(n.path)
    return (
      <div key={n.path} class="tree-dirwrap" style={`--depth:${depth}`}>
        <button
          type="button"
          class={'tree-dir' + (isOpen ? ' open' : '') + (dropOn === n.path ? ' drop' : '')}
          onClick={() => {
            if (collapsed.has(n.path)) collapsed.delete(n.path)
            else collapsed.add(n.path)
            bump((x) => x + 1)
          }}
          {...dragProps(n.path)}
        >
          <Icon name="chevron-right" class="tree-caret" size={14} />
          <Icon name={isOpen ? 'folder-open' : 'folder'} class="tree-folder" size={15} />
          <span class="tree-title">{n.name}</span>
        </button>
        <div class="tree-children" hidden={!isOpen}>
          {(n.children ?? []).map((c) => render(c, depth + 1))}
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

  return (
    <>
      {spaces.map((s) => (
        <section key={s.name} class={'space' + (dropOn === s.name ? ' drop' : '')} {...dragProps(s.name)}>
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

/** Every note in the tree, flat, for the quick switcher. */
export function flatten(spaces: SpaceTree[]): Array<{ id: string; path: string; title: string; name: string }> {
  const out: Array<{ id: string; path: string; title: string; name: string }> = []
  const walk = (n: TreeNode) => {
    if (n.type === 'note' && n.id) out.push({ id: n.id, path: n.path, title: n.title || baseOf(n.path), name: n.name })
    for (const c of n.children ?? []) walk(c)
  }
  for (const s of spaces) for (const c of s.children) walk(c)
  return out
}
