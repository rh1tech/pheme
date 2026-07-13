import { useEffect, useState } from 'react'

/**
 * The height of the *visible* viewport in pixels, or null where the browser has
 * no visualViewport (callers should fall back to a CSS `100dvh`).
 *
 * The chat shell is a fixed-height grid, so its height must exclude whatever iOS
 * is currently overlaying: the dynamic Safari toolbar and, more importantly, the
 * software keyboard — otherwise the composer sits underneath the keyboard the
 * moment it is focused. visualViewport.height accounts for both, which is why it
 * is used instead of `100dvh` plus a separate inset: combining the two would
 * subtract the toolbar twice.
 */
export function useVisualViewportHeight(): number | null {
  const [height, setHeight] = useState<number | null>(
    () => window.visualViewport?.height ?? null,
  )

  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return

    const update = () => {
      setHeight(vv.height)
      // iOS scrolls the layout viewport out from under a fixed-height root when
      // the keyboard opens. Pinning it back keeps the shell aligned to the window.
      window.scrollTo(0, 0)
    }

    vv.addEventListener('resize', update)
    vv.addEventListener('scroll', update)
    return () => {
      vv.removeEventListener('resize', update)
      vv.removeEventListener('scroll', update)
    }
  }, [])

  return height
}
