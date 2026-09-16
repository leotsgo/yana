// A small dropdown for the overflow and account menus. Anchored to the
// button that opened it on wide screens; a sheet from the bottom on
// phones. Escape, a click outside, or picking an item closes it.

import { useEffect, useRef } from 'preact/hooks'

import { Icon } from './icons'
import type { IconName } from './icons'

export interface MenuItem {
  id: string
  label: string
  icon?: IconName
  detail?: string
  danger?: boolean
  /** True marks the current choice in a group of alternatives. */
  checked?: boolean
  disabled?: boolean
  run: () => void
}

export interface MenuSpec {
  /** The element the menu hangs off; the menu aligns to its right edge. */
  anchor: HTMLElement
  items: Array<MenuItem | 'sep'>
  label?: string
  /** A pointer position to open at instead (a right-click on a row). */
  at?: { x: number; y: number }
  /** What the menu acts on, shown at the top of the phone sheet. */
  title?: string
  /** A second line under the title: the path. */
  subtitle?: string
}

export function Menu({ spec, onClose }: { spec: MenuSpec; onClose: () => void }) {
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === 'Escape') {
        ev.preventDefault()
        ev.stopPropagation()
        onClose()
      }
    }
    const onDown = (ev: Event) => {
      const t = ev.target as Node
      if (box.current?.contains(t) || spec.anchor.contains(t)) return
      onClose()
    }
    document.addEventListener('keydown', onKey, true)
    document.addEventListener('pointerdown', onDown, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      document.removeEventListener('pointerdown', onDown, true)
    }
  }, [spec, onClose])

  // Position under the anchor, or at the pointer; the phone stylesheet
  // overrides this to a sheet. A menu near the bottom or right edge is
  // pulled back inside the window once its size is known.
  const r = spec.anchor.getBoundingClientRect()
  const style = spec.at
    ? `top:${Math.round(spec.at.y)}px;left:${Math.round(spec.at.x)}px`
    : `top:${Math.round(r.bottom + 6)}px;right:${Math.round(Math.max(8, window.innerWidth - r.right))}px`
  useEffect(() => {
    const el = box.current
    if (!el || !spec.at) return
    const b = el.getBoundingClientRect()
    if (b.right > window.innerWidth - 8) el.style.left = `${Math.max(8, window.innerWidth - 8 - b.width)}px`
    if (b.bottom > window.innerHeight - 8) el.style.top = `${Math.max(8, window.innerHeight - 8 - b.height)}px`
  }, [spec])

  return (
    <div class="menu-layer">
      <div class="menu" role="menu" aria-label={spec.label} style={style} ref={box}>
        {spec.title && (
          <div class="menu-head">
            <span class="menu-title">{spec.title}</span>
            {spec.subtitle && <span class="menu-subtitle">{spec.subtitle}</span>}
          </div>
        )}
        {spec.items.map((it, i) =>
          it === 'sep' ? (
            <div key={`sep${i}`} class="menu-sep" role="separator" />
          ) : (
            <button
              key={it.id}
              type="button"
              role="menuitem"
              class={'menu-item' + (it.danger ? ' danger' : '') + (it.checked ? ' checked' : '')}
              disabled={it.disabled}
              onClick={() => {
                onClose()
                it.run()
              }}
            >
              {it.icon ? <Icon name={it.icon} /> : <span class="icon" />}
              <span class="menu-label">{it.label}</span>
              {it.detail && <span class="menu-detail">{it.detail}</span>}
              {it.checked && <Icon name="check" class="menu-check" />}
            </button>
          ),
        )}
      </div>
    </div>
  )
}
