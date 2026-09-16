// The on-screen keyboard. On a phone the layout viewport does not always
// shrink when the keyboard opens (iOS keeps it and scrolls instead), so the
// shell reads the visual viewport and sizes itself to it while a note is
// being edited. The hook returns the visible height and its offset from
// the top of the layout viewport, or null when nothing needs doing.

import { useEffect, useState } from 'preact/hooks'

export interface Viewport {
  height: number
  top: number
}

export function useVisualViewport(active: boolean): Viewport | null {
  const [vp, setVp] = useState<Viewport | null>(null)
  useEffect(() => {
    const vv = window.visualViewport
    if (!active || !vv) {
      setVp(null)
      return
    }
    const update = () => {
      const height = Math.round(vv.height)
      const top = Math.round(vv.offsetTop)
      // Only a real keyboard is worth reacting to; browser chrome moving
      // by a few pixels is not.
      const covered = window.innerHeight - height - top
      setVp(covered > 80 || top > 0 ? { height, top } : null)
    }
    update()
    vv.addEventListener('resize', update)
    vv.addEventListener('scroll', update)
    return () => {
      vv.removeEventListener('resize', update)
      vv.removeEventListener('scroll', update)
      setVp(null)
    }
  }, [active])
  return vp
}
