// The row of tabs above the open note, on tablets and desktops. A click
// brings a tab to the front, a double-click keeps the preview tab, a
// middle-click or the × closes one, and right-click or a long press opens
// its menu (the shell fills it in). Tabs drag to reorder; a note dragged
// in from the tree opens where it is dropped. The strip scrolls sideways
// when it overflows and keeps the active tab in view.

import { useEffect, useRef, useState } from 'preact/hooks'

import { Icon } from './icons'
import type { IconName } from './icons'
import { coarsePointer } from './layout'
import { NOTE_DRAG } from './tree'
import type { Tab } from './workspace'

export const TAB_DRAG = 'text/yana-tab'

/** What the strip shows for a tab's note. */
export interface TabInfo {
  title: string
  kind?: 'md' | 'html'
  public?: boolean
  /** The note is not in the tree any more: deleted, or out of reach. */
  gone: boolean
}

export interface TabStripProps {
  tabs: Tab[]
  /** The tab on screen; null while a page (tasks, settings) is shown. */
  active: string | null
  info: (tab: Tab) => TabInfo
  onActivate: (key: string) => void
  onClose: (key: string) => void
  onKeep: (key: string) => void
  onMenu: (tab: Tab, anchor: HTMLElement, at?: { x: number; y: number }) => void
  /** Reorder: the tab goes to this index in the strip. */
  onMove: (key: string, index: number) => void
  /** A note from the tree was dropped at this index. */
  onDropNote: (id: string, title: string, index: number) => void
}

const LONG_PRESS_MS = 450

export function TabStrip({ tabs, active, info, onActivate, onClose, onKeep, onMenu, onMove, onDropNote }: TabStripProps) {
  const strip = useRef<HTMLDivElement>(null)
  const [drop, setDrop] = useState<number | null>(null) // insertion index while dragging
  const press = useRef<{ timer: number; x: number; y: number; fired: boolean } | null>(null)

  // Sideways only: scrollIntoView would move the page around it too.
  useEffect(() => {
    const s = strip.current
    const el = s?.querySelector<HTMLElement>('.tab-active')
    if (!s || !el) return
    const left = el.offsetLeft
    const right = left + el.offsetWidth
    if (left < s.scrollLeft) s.scrollLeft = left - 4
    else if (right > s.scrollLeft + s.clientWidth) s.scrollLeft = right - s.clientWidth + 4
  }, [active, tabs.length])

  function cancelPress(): void {
    const p = press.current
    if (p && !p.fired) window.clearTimeout(p.timer)
    press.current = null
  }
  useEffect(() => cancelPress, [])

  /** The insertion index for a drag at clientX over tab i. */
  function indexAt(ev: DragEvent, i: number): number {
    const r = (ev.currentTarget as HTMLElement).getBoundingClientRect()
    return ev.clientX < r.left + r.width / 2 ? i : i + 1
  }

  function accepts(ev: DragEvent): boolean {
    const types = ev.dataTransfer?.types
    return Boolean(types && (types.includes(TAB_DRAG) || types.includes(NOTE_DRAG)))
  }

  function onDropAt(ev: DragEvent, index: number): void {
    setDrop(null)
    const tab = ev.dataTransfer?.getData(TAB_DRAG)
    const note = ev.dataTransfer?.getData(NOTE_DRAG)
    if (!tab && !note) return
    ev.preventDefault()
    ev.stopPropagation()
    try {
      if (tab) {
        const { key } = JSON.parse(tab) as { key: string }
        onMove(key, index)
      } else if (note) {
        const { id, title } = JSON.parse(note) as { id: string; title: string }
        if (id) onDropNote(id, title, index)
      }
    } catch {
      // not ours
    }
  }

  return (
    <div
      class="tabstrip"
      role="tablist"
      aria-label="open notes"
      ref={strip}
      onWheel={(ev) => {
        // A mouse wheel scrolls the strip sideways.
        const el = strip.current
        if (!el || ev.deltaX !== 0 || el.scrollWidth <= el.clientWidth) return
        el.scrollLeft += ev.deltaY
      }}
      onDragOver={(ev) => {
        if (!accepts(ev)) return
        ev.preventDefault()
        if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move'
        if (ev.target === ev.currentTarget) setDrop(tabs.length)
      }}
      onDragLeave={(ev) => {
        if ((ev.currentTarget as HTMLElement).contains(ev.relatedTarget as Node | null)) return
        setDrop(null)
      }}
      onDrop={(ev) => onDropAt(ev, tabs.length)}
    >
      {tabs.map((t, i) => {
        const inf = info(t)
        const on = t.key === active
        const icon: IconName | null = inf.kind === 'html' ? 'code' : inf.public ? 'globe' : t.pinned ? 'file' : null
        const title = inf.title || 'Untitled'
        return (
          <div
            key={t.key}
            role="tab"
            aria-selected={on}
            tabIndex={on ? 0 : -1}
            class={
              'tab' +
              (on ? ' tab-active' : '') +
              (t.preview ? ' tab-preview' : '') +
              (t.pinned ? ' tab-pinned' : '') +
              (inf.gone ? ' tab-gone' : '') +
              (drop === i ? ' tab-drop-before' : '') +
              (drop === i + 1 && i === tabs.length - 1 ? ' tab-drop-after' : '')
            }
            title={inf.gone ? `${title} (no longer there)` : t.preview ? `${title} (preview: double-click to keep it open)` : title}
            draggable={!coarsePointer}
            onDragStart={(ev) => {
              ev.dataTransfer?.setData(TAB_DRAG, JSON.stringify({ key: t.key }))
              if (ev.dataTransfer) ev.dataTransfer.effectAllowed = 'move'
            }}
            onDragOver={(ev) => {
              if (!accepts(ev)) return
              ev.preventDefault()
              ev.stopPropagation()
              if (ev.dataTransfer) ev.dataTransfer.dropEffect = 'move'
              setDrop(indexAt(ev, i))
            }}
            onDrop={(ev) => onDropAt(ev, indexAt(ev, i))}
            onDragEnd={() => setDrop(null)}
            onClick={() => {
              if (press.current?.fired) {
                press.current = null
                return
              }
              onActivate(t.key)
            }}
            onDblClick={() => onKeep(t.key)}
            onMouseDown={(ev) => {
              // The middle button would start the browser's autoscroll.
              if (ev.button === 1) ev.preventDefault()
            }}
            onAuxClick={(ev) => {
              if (ev.button !== 1) return
              ev.preventDefault()
              if (!t.pinned) onClose(t.key)
            }}
            onContextMenu={(ev) => {
              ev.preventDefault()
              cancelPress()
              onMenu(t, ev.currentTarget as HTMLElement, { x: ev.clientX, y: ev.clientY })
            }}
            onPointerDown={(ev) => {
              if (ev.pointerType === 'mouse') return
              cancelPress()
              const el = ev.currentTarget as HTMLElement
              const timer = window.setTimeout(() => {
                if (press.current) press.current.fired = true
                onMenu(t, el)
              }, LONG_PRESS_MS)
              press.current = { timer, x: ev.clientX, y: ev.clientY, fired: false }
            }}
            onPointerMove={(ev) => {
              const p = press.current
              if (p && !p.fired && Math.hypot(ev.clientX - p.x, ev.clientY - p.y) > 10) cancelPress()
            }}
            onPointerUp={() => {
              if (press.current && !press.current.fired) cancelPress()
            }}
            onPointerCancel={cancelPress}
            onKeyDown={(ev) => {
              if (ev.key === 'Enter' || ev.key === ' ') {
                ev.preventDefault()
                onActivate(t.key)
              }
            }}
          >
            {icon && <Icon name={icon} size={13} class="tab-icon" />}
            {!t.pinned && <span class="tab-title">{title}</span>}
            {!t.pinned && (
              <button
                type="button"
                class="tab-close"
                tabIndex={-1}
                title="Close"
                aria-label={`Close ${title}`}
                onClick={(ev) => {
                  ev.stopPropagation()
                  onClose(t.key)
                }}
              >
                <Icon name="x" size={12} />
              </button>
            )}
          </div>
        )
      })}
    </div>
  )
}
