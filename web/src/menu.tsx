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

  // Position under the anchor; the phone stylesheet overrides this to a sheet.
  const r = spec.anchor.getBoundingClientRect()
  const style = `top:${Math.round(r.bottom + 6)}px;right:${Math.round(Math.max(8, window.innerWidth - r.right))}px`

  return (
    <div class="menu-layer">
      <div class="menu" role="menu" aria-label={spec.label} style={style} ref={box}>
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
