import { useRef, useState, type ReactNode, type TouchEvent } from 'react'
import { Loader } from '@mantine/core'

interface PullToRefreshProps {
  /** Re-fetch callback. The spinner shows until the returned promise settles. */
  onRefresh: () => Promise<void> | void
  children: ReactNode
}

// Damped finger travel needed to trigger, the visual cap, and the damping factor.
const TRIGGER = 64
const MAX = 96
const DAMP = 0.5
// Where the spinner rests while refreshing.
const REST = 44

/**
 * Touch pull-to-refresh for the window-scrolled SPA. The app sets
 * `overscroll-behavior: none`, which disables the browser's native gesture, so
 * we implement it: when the page is scrolled to the very top and the user drags
 * down past a threshold, `onRefresh` runs. Pointer/desktop users are unaffected
 * (touch handlers simply never fire).
 */
export function PullToRefresh({ onRefresh, children }: PullToRefreshProps) {
  const [pull, setPull] = useState(0)
  const [refreshing, setRefreshing] = useState(false)
  // `active` (state) drives the settle transition; the refs hold gesture data
  // that must not be read during render.
  const [active, setActive] = useState(false)
  const startY = useRef<number | null>(null)
  const dragging = useRef(false)

  function onTouchStart(e: TouchEvent) {
    if (refreshing || window.scrollY > 0) {
      startY.current = null
      return
    }
    startY.current = e.touches[0].clientY
    dragging.current = false
    setActive(true)
  }

  function onTouchMove(e: TouchEvent) {
    if (startY.current === null || refreshing) return
    if (window.scrollY > 0) {
      startY.current = null
      setPull(0)
      return
    }
    const dy = e.touches[0].clientY - startY.current
    if (dy <= 0) {
      dragging.current = false
      setPull(0)
      return
    }
    dragging.current = true
    setPull(Math.min(MAX, dy * DAMP))
  }

  async function onTouchEnd() {
    if (startY.current === null) {
      setActive(false)
      return
    }
    const trigger = dragging.current && pull >= TRIGGER
    startY.current = null
    dragging.current = false
    setActive(false)
    if (!trigger) {
      setPull(0)
      return
    }
    setRefreshing(true)
    setPull(REST)
    try {
      await onRefresh()
    } finally {
      setRefreshing(false)
      setPull(0)
    }
  }

  const settling = !active
  const offset = refreshing ? REST : pull
  const indicatorOpacity = refreshing ? 1 : Math.min(1, pull / TRIGGER)

  return (
    <div onTouchStart={onTouchStart} onTouchMove={onTouchMove} onTouchEnd={onTouchEnd} onTouchCancel={onTouchEnd}>
      <div style={{ height: 0, position: 'relative', display: 'flex', justifyContent: 'center' }}>
        <div
          style={{
            position: 'absolute',
            top: offset - 28,
            opacity: indicatorOpacity,
            pointerEvents: 'none',
            transition: settling ? 'top 200ms ease, opacity 150ms ease' : 'none',
          }}
        >
          <Loader size="sm" />
        </div>
      </div>
      <div
        style={{
          transform: `translateY(${offset}px)`,
          transition: settling ? 'transform 200ms ease' : 'none',
        }}
      >
        {children}
      </div>
    </div>
  )
}
