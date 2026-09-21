// The sidebar tree. Pinned notes and folders sit at the top, then each
// space. Notes and folders drag: dropping one on a directory, a space, or
// another note moves it there (a rename on disk, with link propagation
// on the server). Dropping a note into the editor inserts a wikilink; the
// drag payload carries id, path and title for that. Every row has a
// context menu — right-click or the ⋯ that shows on hover on a desktop,
// a long press on a phone — which the shell fills in (tree actions).
// Folders are made and renamed in place: an input row where the folder
// is, not a prompt somewhere else. A double-click renames a folder, or
// opens a note with its title selected. Spaces and folders open and
// close; folders start closed, spaces open, and the state is kept per
// browser (prefs). Opening a note from anywhere but the tree itself
// reveals it: its space and every folder above it open.

import type { ComponentChildren } from 'preact'
import { useEffect, useRef, useState } from 'preact/hooks'

import type { SpaceTree, TreeNode } from './api'
import { baseOf, dirOf } from './api'
import { Icon } from './icons'
import { coarsePointer } from './layout'
import type { Pin } from './prefs'
import * as prefs from './prefs'
import { howFrom } from './workspace'
import type { OpenHow } from './workspace'

/** How long a drag hovers over a closed space or folder before it opens. */
const SPRING_MS = 600

export const NOTE_DRAG = 'text/yana-note'
export const DIR_DRAG = 'text/yana-dir'

/** What a tree action acts on. */
export type TreeTarget =
  | { kind: 'note'; node: TreeNode }
  | { kind: 'dir'; node: TreeNode }
  | { kind: 'space'; name: string }

/** An input open in the tree: a folder being made or renamed. */
export type TreeEdit = { kind: 'new-dir'; parent: string } | { kind: 'rename-dir'; path: string }

export interface TreeProps {
  spaces: SpaceTree[]
  selected: string | null
  pins: Pin[]
  /** Open a note: in the preview tab, or as the click asks (Mod-click, the middle button). */
  onOpen: (id: string, how?: OpenHow) => void
  /** Open a note with its title selected: the way to rename one from here. */
  onOpenTitle: (id: string) => void
  onMove: (id: string, from: string, toDir: string) => void
  onMoveDir: (path: string, toDir: string) => void
  onNew: (dir: string) => void
  /** Start an input row for a folder inside the parent. */
  onNewFolder: (parent: string) => void
  /** Start renaming a folder in place. */
  onRenameFolder: (node: TreeNode) => void
  edit: TreeEdit | null
  /** The input row was submitted: make the folder, or rename it. */
  onCreateDir: (path: string) => void
  onRenameDir: (node: TreeNode, name: string) => void
  onEditDone: () => void
  /** Open the actions for a row, at the pointer when there is one. */
  onContext: (target: TreeTarget, anchor: HTMLElement, at?: { x: number; y: number }) => void
}

const LONG_PRESS_MS = 450

export function Tree(props: TreeProps) {
  const { spaces, selected, pins, onOpen, onOpenTitle, onMove, onMoveDir, onNew, onNewFolder, onRenameFolder, edit, onCreateDir, onRenameDir, onEditDone, onContext } = props
  const [dropOn, setDropOn] = useState<string | null>(null)
  const [, bump] = useState(0)
  const spaceNames = new Set(spaces.map((s) => s.name))

  // Open state lives in prefs; a change there (a click here, a palette
  // command, another tab) redraws the tree.
  useEffect(() => prefs.onChange(() => bump((x) => x + 1)), [])

  // Keys for folders and spaces that are gone go with them.
  useEffect(() => {
    prefs.pruneTreeState(
      folders(spaces).map((f) => f.path),
      spaces.map((s) => s.name),
    )
  }, [spaces])

  // A folder being made inside a closed folder or space: open it so the row shows.
  useEffect(() => {
    if (edit?.kind !== 'new-dir') return
    if (spaceNames.has(edit.parent)) {
      if (!prefs.isSpaceOpen(edit.parent)) prefs.setSpaceOpen(edit.parent, true)
    } else if (!prefs.isFolderOpen(edit.parent)) prefs.setFolderOpen(edit.parent, true)
  }, [edit])

  // The note that opened by a way other than a click in the tree gets
  // revealed: its space and the folders above it open, and its row is
  // scrolled into view. A click in the tree never reopens what the
  // person just closed. A note the tree does not hold yet (one just
  // made, indexed a moment later) is revealed once it shows up.
  const clicked = useRef<string | null>(null)
  const revealed = useRef<string | null>(null)
  useEffect(() => {
    if (!selected || revealed.current === selected) return
    if (clicked.current === selected) {
      clicked.current = null
      revealed.current = selected
      return
    }
    const note = findNote(spaces, selected)
    if (!note) return
    revealed.current = selected
    const parts = note.path.split('/')
    const space = parts.length > 1 ? parts[0] ?? '' : ''
    if (!prefs.isSpaceOpen(space)) prefs.setSpaceOpen(space, true)
    const above: string[] = []
    for (let i = 2; i < parts.length; i++) above.push(parts.slice(0, i).join('/'))
    const closed = above.filter((p) => !prefs.isFolderOpen(p))
    if (closed.length > 0) prefs.setFoldersOpen(closed, true)
    requestAnimationFrame(() => {
      const row = document.querySelector('.space:not(.pinned) .tree-note.selected')
      row?.scrollIntoView({ block: 'nearest' })
    })
  }, [selected, spaces])

  // A drag held over a closed space or folder opens it, as a file manager does.
  const spring = useRef<{ dir: string; timer: number } | null>(null)
  function cancelSpring(): void {
    if (spring.current) window.clearTimeout(spring.current.timer)
    spring.current = null
  }
  useEffect(() => cancelSpring, [])
  // A long press opens the menu and swallows the click that follows it.
  const press = useRef<{ timer: number; x: number; y: number; fired: boolean } | null>(null)

  /** Drop handling for a directory; `open` is how to open it when it is closed. */
  function dragProps(dir: string, open?: () => void) {
    return {
      onDragOver: (ev: DragEvent) => {
        const types = ev.dataTransfer?.types
        if (!types || (!types.includes(NOTE_DRAG) && !types.includes(DIR_DRAG))) return
        ev.preventDefault()
        ev.stopPropagation()
        ev.dataTransfer.dropEffect = 'move'
        setDropOn(dir)
        if (open && spring.current?.dir !== dir) {
          cancelSpring()
          spring.current = { dir, timer: window.setTimeout(() => { spring.current = null; open() }, SPRING_MS) }
        }
      },
      onDragLeave: (ev: DragEvent) => {
        if ((ev.currentTarget as HTMLElement).contains(ev.relatedTarget as Node | null)) return
        setDropOn((d) => (d === dir ? null : d))
        if (spring.current?.dir === dir) cancelSpring()
      },
      onDrop: (ev: DragEvent) => {
        setDropOn(null)
        cancelSpring()
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

  function noteRow(n: TreeNode, depth = 0) {
    const id = n.id ?? ''
    const target: TreeTarget = { kind: 'note', node: n }
    const row = (
      <a
        key={n.path}
        class={'tree-note' + (id === selected ? ' selected' : '') + (n.conflict ? ' conflict' : '') + (dropOn === dirOf(n.path) ? ' drop' : '')}
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
          if (!id) return
          const how = howFrom(ev)
          if (how !== 'background') clicked.current = id
          onOpen(id, how)
        }}
        onAuxClick={(ev) => {
          if (ev.button !== 1 || !id) return
          ev.preventDefault()
          onOpen(id, 'background')
        }}
        onDblClick={(ev) => {
          ev.preventDefault()
          if (id) onOpenTitle(id)
        }}
        {...dragProps(dirOf(n.path))}
        {...contextProps(target)}
      >
        {n.conflict && <Icon name="alert" class="tree-conflict-mark" size={12} />}
        <span class="tree-title">{n.title || n.name}</span>
        {n.public && <Icon name="globe" class="tree-public" size={12} />}
        {n.kind === 'html' && <span class="tree-kind">html</span>}
        {moreButton(target, `Actions for ${n.title || n.name}`)}
      </a>
    )
    // A note carrying conflict copies nests them under itself instead
    // of letting them read as siblings of their survivor.
    if (!n.conflict && (n.children?.length ?? 0) > 0) {
      return (
        <div key={n.path} class="tree-notewrap">
          {row}
          <div class="tree-conflicts" aria-label="conflict copies">
            {(n.children ?? []).map((c) => noteRow(c, depth + 1))}
          </div>
        </div>
      )
    }
    return row
  }

  /** One folder's children in view order: directories first (by name),
   * then notes by the folder's sort preference — title ascending when
   * the folder declares nothing. Server sends rel_path order; this only
   * reorders the view, never the tree the client caches. */
  function sortChildren(n: TreeNode, depth: number): TreeNode[] {
    const kids = [...(n.children ?? [])]
    const mode = prefs.folderSort(n.path)
    const dirs = kids.filter((c) => c.type === 'dir').sort((a, b) => a.name.toLowerCase() < b.name.toLowerCase() ? -1 : 1)
    const notes = kids.filter((c) => c.type !== 'dir')
    const time = (c: TreeNode, which: 'created' | 'modified') =>
      which === 'created' ? Date.parse(c.created ?? '') || 0 : Date.parse(c.modified ?? '') || 0
    const which: 'created' | 'modified' = mode.startsWith('created') ? 'created' : 'modified'
    const desc = mode.endsWith('-desc')
    notes.sort((a, b) => {
      if (mode === 'title-asc' || mode === 'title-desc') {
        const at = (a.title ?? '').toLowerCase()
        const bt = (b.title ?? '').toLowerCase()
        const cmp = at < bt ? -1 : at > bt ? 1 : 0
        return desc ? -cmp : cmp
      }
      const diff = time(a, which) - time(b, which)
      return desc ? -diff : diff
    })
    void depth
    return [...dirs, ...notes]
  }

  function dirRow(n: TreeNode, depth: number) {
    const isOpen = prefs.isFolderOpen(n.path)
    const target: TreeTarget = { kind: 'dir', node: n }
    const empty = (n.children ?? []).length === 0
    const renaming = edit?.kind === 'rename-dir' && edit.path === n.path
    const making = edit?.kind === 'new-dir' && edit.parent === n.path
    const row = renaming ? (
      // An input cannot sit inside a button; the row is a plain box while it edits.
      <div class={'tree-dir editing' + (isOpen ? ' open' : '')} role="group" aria-label={`Renaming ${n.name}`}>
        <Icon name="chevron-right" class="tree-caret" size={14} />
        <Icon name={isOpen ? 'folder-open' : 'folder'} class="tree-folder" size={15} />
        <InlineName
          initial={n.name}
          label={`New name for ${n.name}`}
          onSubmit={(v) => {
            if (v !== n.name) onRenameDir(n, v)
          }}
          onDone={onEditDone}
        />
      </div>
    ) : (
      <button
        type="button"
        class={'tree-dir' + (isOpen ? ' open' : '') + (dropOn === n.path ? ' drop' : '')}
        title={n.path}
        draggable={!coarsePointer}
        onDragStart={(ev) => {
          ev.dataTransfer?.setData(DIR_DRAG, JSON.stringify({ path: n.path }))
          if (ev.dataTransfer) ev.dataTransfer.effectAllowed = 'move'
        }}
        aria-expanded={isOpen}
        onClick={() => prefs.setFolderOpen(n.path, !isOpen)}
        onDblClick={(ev) => {
          ev.preventDefault()
          if (!coarsePointer) onRenameFolder(n)
        }}
        {...dragProps(n.path, isOpen ? undefined : () => prefs.setFolderOpen(n.path, true))}
        {...contextProps(target)}
      >
        <Icon name="chevron-right" class="tree-caret" size={14} />
        <Icon name={isOpen ? 'folder-open' : 'folder'} class="tree-folder" size={15} />
        <span class="tree-title">{n.name}</span>
        {moreButton(target, `Actions for ${n.name}`)}
      </button>
    )
    return (
      <div key={n.path} class="tree-dirwrap" style={`--depth:${depth}`}>
        {row}
        <div class="tree-children" hidden={!isOpen}>
          {making && newDirRow(n.path, depth + 1)}
          {empty && !making ? (
            <button type="button" class="tree-empty-dir" onClick={() => onNew(n.path)}>
              <Icon name="plus" size={14} />
              New note here
            </button>
          ) : (
            sortChildren(n, depth + 1).map((c) => render(c, depth + 1))
          )}
        </div>
      </div>
    )
  }

  // The input row for a folder that does not exist yet.
  function newDirRow(parent: string, depth: number) {
    return (
      <div key={`${parent}//new`} class="tree-dirwrap" style={`--depth:${depth}`}>
        <div class="tree-dir editing" role="group" aria-label="New folder">
          <Icon name="chevron-right" class="tree-caret" size={14} />
          <Icon name="folder" class="tree-folder" size={15} />
          <InlineName
            initial=""
            label={`Name for the new folder in ${parent}`}
            placeholder="Folder name"
            onSubmit={(v) => onCreateDir(parent ? `${parent}/${v}` : v)}
            onDone={onEditDone}
          />
        </div>
      </div>
    )
  }

  function render(n: TreeNode, depth: number) {
    return n.type === 'dir' ? dirRow(n, depth) : noteRow(n, depth)
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

  // The header is the toggle: the name, the count and the caret in one
  // button, the add buttons beside it.
  function spaceHead(name: string, open: boolean, label: ComponentChildren, title: string, adds?: ComponentChildren) {
    return (
      <h2 class={'space-name' + (open ? ' open' : '')} title={title}>
        <button type="button" class="space-toggle" aria-expanded={open} onClick={() => prefs.setSpaceOpen(name, !open)}>
          <Icon name="chevron-right" class="tree-caret" size={12} />
          {label}
        </button>
        {adds}
      </h2>
    )
  }

  const pinnedOpen = prefs.isSpaceOpen(prefs.PINNED_SECTION)
  return (
    <>
      {pinned.length > 0 && (
        <section class="space pinned" aria-label="pinned">
          {spaceHead(
            prefs.PINNED_SECTION,
            pinnedOpen,
            <>
              <Icon name="pin" size={12} />
              Pinned
            </>,
            `${pinned.length} pinned`,
          )}
          <div class="space-body" hidden={!pinnedOpen}>
            {pinned.map((n) => render(n, 0))}
          </div>
        </section>
      )}
      {spaces.map((s) => {
        const open = prefs.isSpaceOpen(s.name)
        return (
          <section
            key={s.name}
            class={'space' + (dropOn === s.name ? ' drop' : '')}
            {...dragProps(s.name, open ? undefined : () => prefs.setSpaceOpen(s.name, true))}
            {...contextProps({ kind: 'space', name: s.name })}
          >
            {spaceHead(
              s.name,
              open,
              <>
                {s.name === '' ? '/' : s.name + '/'}
                <span class="space-count">{s.notes}</span>
              </>,
              `${s.notes} notes`,
              <span class="tree-adds">
                {s.name !== '' && (
                  <button type="button" class="tree-add" title={`New folder in ${s.name}`} aria-label={`New folder in ${s.name}`} onClick={() => onNewFolder(s.name)}>
                    <Icon name="folder-plus" size={14} />
                  </button>
                )}
                <button type="button" class="tree-add" title={`New note in ${s.name || 'the root'}`} aria-label={`New note in ${s.name || 'the root'}`} onClick={() => onNew(s.name)}>
                  <Icon name="plus" size={14} />
                </button>
              </span>,
            )}
            <div class="space-body" hidden={!open}>
              {edit?.kind === 'new-dir' && edit.parent === s.name && newDirRow(s.name, 0)}
              {s.children.length === 0 && !(edit?.kind === 'new-dir' && edit.parent === s.name) ? (
                <button type="button" class="tree-empty-dir space-empty" onClick={() => onNew(s.name)}>
                  <Icon name="plus" size={14} />
                  Nothing here yet. New note
                </button>
              ) : (
                s.children.map((c) => render(c, 0))
              )}
            </div>
          </section>
        )
      })}
    </>
  )
}

interface InlineNameProps {
  initial: string
  label: string
  placeholder?: string
  /** The trimmed text, with slashes stripped, when it is not empty. */
  onSubmit: (name: string) => void
  /** Always called once, after submit or on cancel. */
  onDone: () => void
}

// One line of text in the tree. Enter submits, Escape cancels, and a
// blur submits too (a click elsewhere is how a person finishes on a
// phone). Clicks inside do not reach the row, which would toggle it.
function InlineName({ initial, label, placeholder, onSubmit, onDone }: InlineNameProps) {
  const el = useRef<HTMLInputElement>(null)
  const done = useRef(false)

  useEffect(() => {
    const i = el.current
    if (!i) return
    i.focus()
    i.select()
  }, [])

  function finish(submit: boolean): void {
    if (done.current) return
    done.current = true
    const v = (el.current?.value ?? '').replace(/^\/+|\/+$/g, '').trim()
    if (submit && v !== '') onSubmit(v)
    onDone()
  }

  return (
    <input
      ref={el}
      class="tree-input"
      type="text"
      value={initial}
      placeholder={placeholder}
      aria-label={label}
      spellcheck={false}
      autocomplete="off"
      onClick={(ev) => ev.stopPropagation()}
      onDblClick={(ev) => ev.stopPropagation()}
      onPointerDown={(ev) => ev.stopPropagation()}
      onContextMenu={(ev) => ev.stopPropagation()}
      onKeyDown={(ev) => {
        ev.stopPropagation()
        if (ev.key === 'Enter') {
          ev.preventDefault()
          finish(true)
        } else if (ev.key === 'Escape') {
          ev.preventDefault()
          finish(false)
        }
      }}
      onBlur={() => finish(true)}
    />
  )
}

export interface FlatNote {
  id: string
  path: string
  title: string
  name: string
  tags: string[]
  kind?: 'md' | 'html'
  public?: boolean
  /** The note's file name says it is a conflict copy. */
  conflict?: boolean
  /** The note this copy belongs to, when it still exists. */
  conflictOf?: string
}

/** Every note in the tree, flat, for the quick switcher. */
export function flatten(spaces: SpaceTree[]): FlatNote[] {
  const out: FlatNote[] = []
  const walk = (n: TreeNode) => {
    if (n.type === 'note' && n.id) {
      out.push({
        id: n.id,
        path: n.path,
        title: n.title || baseOf(n.path),
        name: n.name,
        tags: n.tags ?? [],
        kind: n.kind,
        public: n.public,
        conflict: n.conflict || /\.conflict-\d{8}[-T]\d{6}/.test(n.path),
        conflictOf: n.conflict_of,
      })
    }
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
