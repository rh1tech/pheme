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
    return subscribeToViewport(vv, update)
  }, [])

  return rect
}

/**
 * Subscribes to every event that can change the visible viewport, and re-reads after the ones iOS
 * animates through.
 *
 * `visualViewport`'s own resize event is not enough, and the failure is not subtle. iOS can drop
 * the FINAL resize as the keyboard slides away — reliably so in an installed PWA — which leaves
 * the shell sized for a keyboard that is no longer there: the app occupying the top two-thirds of
 * the screen with a band of page background under it, until something else happens to fire a
 * resize. A keyboard is about a third of the screen, which is exactly the size of the hole.
 *
 * So:
 *   - `focusout` is the reliable signal that the keyboard is on its way out, but it fires at the
 *     START of the animation, when the viewport is still short. Hence the re-reads after it: two
 *     frames for the fast path, then a timeout past the end of the animation for the real value.
 *   - `pageshow` and `visibilitychange` cover returning to a backgrounded PWA, which iOS may
 *     resume at a stale size without ever firing a resize.
 *   - `window.resize` and `orientationchange` are cheap and catch whatever the above miss.
 *
 * Re-reading costs one state update that usually changes nothing, and React bails out of a render
 * when the value is unchanged. Missing one costs a third of the screen.
 *
 * Exported for its test: the behaviour worth pinning is that a DROPPED resize still recovers,
 * which is exactly what cannot be observed by rendering the hook normally.
 */
export function subscribeToViewport(vv: VisualViewport, update: () => void): () => void {
  // Every deferred read goes through this, so teardown stops them all with one flag. Clearing the
  // timeouts alone is not enough: a frame callback queued just before unsubscribing still runs,
  // and would write a viewport size into a component that has gone.
  let live = true
  const timers: number[] = []
  const read = () => {
    if (live) update()
  }
  const settle = () => {
    requestAnimationFrame(read)
    requestAnimationFrame(() => requestAnimationFrame(read))
    // 400ms clears iOS's keyboard-dismiss animation with room to spare.
    timers.push(window.setTimeout(read, 400))
  }

  vv.addEventListener('resize', update)
  vv.addEventListener('scroll', update)
  window.addEventListener('resize', update)
  window.addEventListener('orientationchange', settle)
  window.addEventListener('focusout', settle)
  window.addEventListener('pageshow', settle)
  document.addEventListener('visibilitychange', settle)

  return () => {
    live = false
    for (const t of timers) window.clearTimeout(t)
    vv.removeEventListener('resize', update)
    vv.removeEventListener('scroll', update)
    window.removeEventListener('resize', update)
    window.removeEventListener('orientationchange', settle)
    window.removeEventListener('focusout', settle)
    window.removeEventListener('pageshow', settle)
    document.removeEventListener('visibilitychange', settle)
  }
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
    // The same subscription as the rect above, and for the same reason: a dropped resize leaves
    // this stuck reporting a keyboard that has already gone, which collapses padding that should
    // have come back.
    return subscribeToViewport(vv, update)
  }, [])

  return open
}
