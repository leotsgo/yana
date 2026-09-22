// Which of the three layouts the shell is in. Phone is one pane with the
// sidebar as a drawer and a bar along the bottom; tablet keeps the drawer
// but has room for the top bar's actions; desktop shows the columns.

import { useEffect, useState } from 'preact/hooks'

export type Layout = 'phone' | 'tablet' | 'desktop'

const phone = window.matchMedia('(max-width: 719px)')
const tablet = window.matchMedia('(max-width: 1023px)')

/** The layout right now, outside a component. */
export function current(): Layout {
  if (phone.matches) return 'phone'
  if (tablet.matches) return 'tablet'
  return 'desktop'
}

export function useLayout(): Layout {
  const [layout, setLayout] = useState<Layout>(current)
  useEffect(() => {
    const update = () => setLayout(current())
    phone.addEventListener('change', update)
    tablet.addEventListener('change', update)
    return () => {
      phone.removeEventListener('change', update)
      tablet.removeEventListener('change', update)
    }
  }, [])
  return layout
}

/** True on devices whose primary pointer cannot hover: no drag, no hover-only affordances. */
export const coarsePointer = window.matchMedia('(pointer: coarse)').matches
