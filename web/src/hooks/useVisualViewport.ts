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
/** The visible viewport's height and where it starts, relative to the layout viewport. */
export interface ViewportRect {
  height: number
  offsetTop: number
}

/**
 * The rectangle iOS is actually showing, or null where the browser has no
 * visualViewport.
 *
 * A `position: fixed` element is placed against the LAYOUT viewport, which iOS does
 * not shrink when the keyboard opens — only the visual viewport shrinks. So anything
 * pinned to the bottom of the screen (a dialog presented as a bottom sheet, say) ends
 * up underneath the keyboard, taking its text field with it. Positioning against this
 * rectangle instead keeps it above the keyboard, where it can be typed into.
 */
export function useVisualViewportRect(): ViewportRect | null {
  const [rect, setRect] = useState<ViewportRect | null>(() =>
    window.visualViewport
      ? { height: window.visualViewport.height, offsetTop: window.visualViewport.offsetTop }
      : null,
  )

  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return
    const update = () => setRect({ height: vv.height, offsetTop: vv.offsetTop })
    vv.addEventListener('resize', update)
    vv.addEventListener('scroll', update)
    return () => {
      vv.removeEventListener('resize', update)
      vv.removeEventListener('scroll', update)
    }
  }, [])

  return rect
}

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
