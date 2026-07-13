import { useEffect } from 'react'

// Only a drag that begins within this many pixels of the left edge counts. A
// swipe that starts mid-screen is the reader panning an image carousel or
// selecting text, not asking to go back.
const EDGE_PX = 28
// How far it must travel before it commits.
const DISTANCE_PX = 70
// Beyond this much vertical drift it is a scroll, not a back gesture.
const SLOP_PX = 40

/**
 * The iOS "swipe from the left edge to go back" gesture, for the conversation
 * pane on a phone.
 *
 * A PWA installed to the home screen has no browser chrome, so it has no back
 * button either. Without this, the only way out of a channel is the small arrow
 * in the header — reachable by a thumb only on the smallest phones.
 *
 * @param enabled false on desktop, where there is nothing to go back from
 * @param onBack fired once the gesture commits
 */
export function useEdgeSwipeBack(enabled: boolean, onBack: () => void): void {
  useEffect(() => {
    if (!enabled) return

    let startX = 0
    let startY = 0
    let tracking = false

    const onTouchStart = (e: TouchEvent) => {
      const touch = e.touches[0]
      if (!touch || touch.clientX > EDGE_PX) return
      startX = touch.clientX
      startY = touch.clientY
      tracking = true
    }

    const onTouchMove = (e: TouchEvent) => {
      if (!tracking) return
      const touch = e.touches[0]
      if (!touch) return
      if (Math.abs(touch.clientY - startY) > SLOP_PX) {
        tracking = false
        return
      }
      if (touch.clientX - startX > DISTANCE_PX) {
        tracking = false
        onBack()
      }
    }

    const stop = () => {
      tracking = false
    }

    // Passive: the gesture only observes, so it must never block the scrolling it
    // sits on top of.
    document.addEventListener('touchstart', onTouchStart, { passive: true })
    document.addEventListener('touchmove', onTouchMove, { passive: true })
    document.addEventListener('touchend', stop)
    document.addEventListener('touchcancel', stop)
    return () => {
      document.removeEventListener('touchstart', onTouchStart)
      document.removeEventListener('touchmove', onTouchMove)
      document.removeEventListener('touchend', stop)
      document.removeEventListener('touchcancel', stop)
    }
  }, [enabled, onBack])
}
