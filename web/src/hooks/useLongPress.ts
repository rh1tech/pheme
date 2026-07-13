import { useCallback, useRef } from 'react'
import type { PointerEvent as ReactPointerEvent } from 'react'

// Roughly the delay iOS and Android use before a press becomes a "hold".
const HOLD_MS = 450
// A press that wanders further than this is a scroll, not a hold.
const SLOP_PX = 10

export interface LongPressHandlers {
  onPointerDown: (e: ReactPointerEvent) => void
  onPointerMove: (e: ReactPointerEvent) => void
  onPointerUp: () => void
  onPointerCancel: () => void
  onContextMenu: (e: { preventDefault: () => void; clientX: number; clientY: number }) => void
}

/**
 * Opens a menu on a long press (touch) or a right-click (mouse) — the two ways
 * people ask "what can I do with this?".
 *
 * The press is cancelled if the finger travels: in a scrolling feed almost every
 * touch begins as a press on a message, and a hold that fired mid-flick would put
 * a menu under the reader's thumb while they were trying to scroll past.
 */
export function useLongPress(onTrigger: (x: number, y: number) => void): LongPressHandlers {
  const timer = useRef<number | null>(null)
  const origin = useRef<{ x: number; y: number } | null>(null)

  const cancel = useCallback(() => {
    if (timer.current !== null) {
      window.clearTimeout(timer.current)
      timer.current = null
    }
    origin.current = null
  }, [])

  const onPointerDown = useCallback(
    (e: ReactPointerEvent) => {
      // Mouse users get the menu from right-click; a held left button is a text
      // selection, and hijacking it would be hostile.
      if (e.pointerType === 'mouse') return
      const { clientX: x, clientY: y } = e
      origin.current = { x, y }
      timer.current = window.setTimeout(() => {
        timer.current = null
        onTrigger(x, y)
      }, HOLD_MS)
    },
    [onTrigger],
  )

  const onPointerMove = useCallback(
    (e: ReactPointerEvent) => {
      const start = origin.current
      if (!start || timer.current === null) return
      if (Math.abs(e.clientX - start.x) > SLOP_PX || Math.abs(e.clientY - start.y) > SLOP_PX) {
        cancel()
      }
    },
    [cancel],
  )

  const onContextMenu = useCallback(
    (e: { preventDefault: () => void; clientX: number; clientY: number }) => {
      e.preventDefault()
      onTrigger(e.clientX, e.clientY)
    },
    [onTrigger],
  )

  return {
    onPointerDown,
    onPointerMove,
    onPointerUp: cancel,
    onPointerCancel: cancel,
    onContextMenu,
  }
}
