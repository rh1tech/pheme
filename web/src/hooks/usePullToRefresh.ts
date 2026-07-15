import { useEffect, useState } from 'react'
import type { RefObject } from 'react'

// How far the finger must drag (after resistance) before a release refreshes.
const TRIGGER_PX = 64
// The pull cannot stretch further than this, however hard it is dragged.
const MAX_PULL_PX = 96
// Fingers move further than the indicator should: the pull follows at half speed,
// which is what gives the gesture its rubber-band feel.
const RESISTANCE = 0.5

export interface PullToRefreshState {
  /** Current pull distance in px, for positioning the indicator (0 when idle). */
  pull: number
  /** True while the refresh promise is in flight. */
  refreshing: boolean
  /** True once the pull has passed the trigger, so the indicator can arm. */
  armed: boolean
}

/**
 * Pull-to-refresh for a scroll container.
 *
 * Fires `onRefresh` when the reader drags down from the very top (scrollTop 0) past
 * the trigger and lets go. Listeners are passive and attached to the scroller, so
 * they never fight the scrolling they sit on top of; state drives only the
 * indicator, so a drag does not re-subscribe them.
 *
 * @param scrollerRef the element whose top the pull is measured from
 * @param onRefresh the refetch to run; the pull holds until it settles
 * @param enabled false to detach entirely (e.g. desktop)
 */
export function usePullToRefresh(
  scrollerRef: RefObject<HTMLElement | null>,
  onRefresh: () => Promise<void>,
  enabled = true,
): PullToRefreshState {
  const [pull, setPull] = useState(0)
  const [refreshing, setRefreshing] = useState(false)

  useEffect(() => {
    const el = scrollerRef.current
    if (!el || !enabled) return

    let startY = 0
    let tracking = false
    let distance = 0
    let busy = false

    const setDistance = (d: number) => {
      distance = d
      setPull(d)
    }

    const onStart = (e: TouchEvent) => {
      if (busy) return
      const touch = e.touches[0]
      // Only a pull that begins at the very top is a refresh; anywhere else it is an
      // ordinary scroll through the list.
      if (!touch || el.scrollTop > 0) return
      startY = touch.clientY
      tracking = true
    }

    const onMove = (e: TouchEvent) => {
      if (!tracking) return
      const touch = e.touches[0]
      if (!touch) return
      const delta = touch.clientY - startY
      // Dragging up, or the content having scrolled off the top mid-gesture, ends the
      // pull and hands the touch back to normal scrolling.
      if (delta <= 0 || el.scrollTop > 0) {
        tracking = false
        setDistance(0)
        return
      }
      setDistance(Math.min(MAX_PULL_PX, delta * RESISTANCE))
    }

    const onEnd = () => {
      if (!tracking) return
      tracking = false
      if (distance < TRIGGER_PX || busy) {
        setDistance(0)
        return
      }
      busy = true
      setRefreshing(true)
      setDistance(TRIGGER_PX) // hold the indicator up while the refetch runs
      void onRefresh()
        .catch(() => undefined)
        .finally(() => {
          busy = false
          setRefreshing(false)
          setDistance(0)
        })
    }

    el.addEventListener('touchstart', onStart, { passive: true })
    el.addEventListener('touchmove', onMove, { passive: true })
    el.addEventListener('touchend', onEnd)
    el.addEventListener('touchcancel', onEnd)
    return () => {
      el.removeEventListener('touchstart', onStart)
      el.removeEventListener('touchmove', onMove)
      el.removeEventListener('touchend', onEnd)
      el.removeEventListener('touchcancel', onEnd)
    }
    // onRefresh is intentionally not a dep: it is read at gesture time, and depending
    // on a fresh inline callback each render would re-subscribe the listeners.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollerRef, enabled])

  return { pull, refreshing, armed: pull >= TRIGGER_PX }
}
