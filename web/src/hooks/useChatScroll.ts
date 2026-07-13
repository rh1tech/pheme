import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

// Within this many pixels of the bottom, the feed counts as "at the bottom" and
// a newly arrived message scrolls into view instead of raising the jump pill.
const BOTTOM_EPSILON = 80

export interface ChatScrollApi {
  scrollerRef: RefObject<HTMLDivElement | null>
  /** True when the reader is at (or very near) the newest message. */
  atBottom: boolean
  scrollToBottom: (behavior?: ScrollBehavior) => void
  /**
   * Call immediately before committing a prepend of older messages. It records
   * where the viewport sits relative to the bottom; the restore happens on the
   * next layout, before the browser paints.
   */
  captureAnchor: () => void
}

/**
 * Owns the scroll position of the message feed.
 *
 * The feed is a normal top-to-bottom list (not `flex-direction: column-reverse`)
 * so that DOM order matches reading order — which keeps find-in-page, text
 * selection and screen readers working, and lets date separators be plain sticky
 * elements. The cost is that bottom-pinning and the scroll restoration on prepend
 * have to be done by hand, which is what this hook is.
 *
 * @param resetKey changing it (i.e. opening another channel) re-pins to the bottom
 * @param itemCount number of rendered messages; a change triggers the restore
 * @param onReachBottom fired when the reader arrives at the newest message
 */
export function useChatScroll(
  resetKey: string,
  itemCount: number,
  onReachBottom?: () => void,
): ChatScrollApi {
  const scrollerRef = useRef<HTMLDivElement>(null)
  const [atBottom, setAtBottom] = useState(true)
  // Distance from the bottom, captured just before a prepend. Null when none is pending.
  const anchor = useRef<number | null>(null)
  // Until the first pin lands, scroll events are our own doing, not the reader's.
  const pinned = useRef(false)

  const reachedBottom = useRef(onReachBottom)
  useEffect(() => {
    reachedBottom.current = onReachBottom
  })

  // Resetting on a new channel is a state adjustment during render — the React-
  // sanctioned alternative to a setState inside an effect, which would render
  // the old channel's scroll state once before correcting it.
  const [seenKey, setSeenKey] = useState(resetKey)
  if (seenKey !== resetKey) {
    setSeenKey(resetKey)
    setAtBottom(true)
  }

  const scrollToBottom = useCallback((behavior: ScrollBehavior = 'auto') => {
    const el = scrollerRef.current
    if (!el) return
    el.scrollTo({ top: el.scrollHeight, behavior })
  }, [])

  const captureAnchor = useCallback(() => {
    const el = scrollerRef.current
    if (!el) return
    anchor.current = el.scrollHeight - el.scrollTop
  }, [])

  // Opening a channel starts at the newest message. A layout effect, so the jump
  // happens before paint and the reader never glimpses the top of the list.
  useLayoutEffect(() => {
    pinned.current = false
    const el = scrollerRef.current
    if (!el) return
    el.scrollTop = el.scrollHeight
    pinned.current = true
  }, [resetKey])

  // After older messages are prepended, put the viewport back where it was.
  // Distance-from-bottom is the invariant to restore: it survives the list
  // growing by an arbitrary, unknown height above the reader.
  useLayoutEffect(() => {
    const el = scrollerRef.current
    if (!el || anchor.current === null) return
    el.scrollTop = el.scrollHeight - anchor.current
    anchor.current = null
  }, [itemCount])

  useEffect(() => {
    const el = scrollerRef.current
    if (!el) return
    const onScroll = () => {
      if (!pinned.current) return
      const distance = el.scrollHeight - el.scrollTop - el.clientHeight
      const bottom = distance <= BOTTOM_EPSILON
      setAtBottom(bottom)
      if (bottom) reachedBottom.current?.()
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [])

  return { scrollerRef, atBottom, scrollToBottom, captureAnchor }
}
