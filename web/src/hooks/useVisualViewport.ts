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

/**
 * Whether the software keyboard is currently covering the bottom of the screen.
 *
 * The composer keeps `env(safe-area-inset-bottom)` of padding to clear the home
 * indicator — and iOS keeps reporting that inset even while the keyboard physically
 * covers the indicator, leaving a dead strip between the text field and the keys.
 * Knowing the keyboard is up lets that padding collapse.
 *
 * Detected by comparing the visual viewport to the TALLEST it has been (the
 * keyboard-closed state), NOT to `window.innerHeight`: the page's
 * `interactive-widget=resizes-content` shrinks the layout viewport — and so
 * `innerHeight` — along with the visual one, so their difference is ~0 with the
 * keyboard open. Its own high-water mark is the only stable reference.
 */
const KEYBOARD_MIN_PX = 120

export function useKeyboardOpen(): boolean {
  const [open, setOpen] = useState(false)

  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return
    let tallest = vv.height
    const update = () => {
      tallest = Math.max(tallest, vv.height)
      setOpen(tallest - vv.height > KEYBOARD_MIN_PX)
    }
    update()
    vv.addEventListener('resize', update)
    vv.addEventListener('scroll', update)
    return () => {
      vv.removeEventListener('resize', update)
      vv.removeEventListener('scroll', update)
    }
  }, [])

  return open
}
