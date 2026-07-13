import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

// Within this many pixels of the bottom, the feed counts as "at the bottom": a
// newly arrived message scrolls into view instead of raising the jump pill, and
// the feed stays stuck to the bottom as content grows.
const BOTTOM_EPSILON = 80

export interface ChatScrollApi {
  /** The scrolling element. */
  scrollerRef: RefObject<HTMLDivElement | null>
  /** The element wrapping the messages. Its height changes drive the stick. */
  contentRef: RefObject<HTMLDivElement | null>
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
 * so DOM order matches reading order — find-in-page, text selection, screen
 * readers and sticky date separators all keep working. The cost is that staying
 * at the bottom has to be done by hand, which is what this hook is.
 *
 * The central idea is "stick": the feed is glued to the bottom until the reader
 * scrolls away from it, and re-glues when they come back. Pinning once, when the
 * channel opens, is not enough — at that moment the messages have not been
 * fetched, the bubbles have no height, and images have not decoded. Each of those
 * grows the content afterwards, which is why a one-shot pin leaves the reader
 * stranded in the middle of the backlog. A ResizeObserver on the content re-pins
 * on every one of those growths, so "open a channel, see the newest message"
 * holds regardless of what lands late.
 *
 * @param resetKey changing it (opening another channel) re-sticks to the bottom
 * @param onReachBottom fired when the reader arrives at the newest message
 */
export function useChatScroll(resetKey: string, onReachBottom?: () => void): ChatScrollApi {
  const scrollerRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const [atBottom, setAtBottom] = useState(true)

  // Glued to the bottom? Starts true for every channel and is released the moment
  // the reader scrolls up. A ref, not state: the observers below must read the
  // live value without being torn down and rebuilt on each change.
  const stick = useRef(true)
  // Distance from the bottom captured just before a prepend; null when none pending.
  const anchor = useRef<number | null>(null)

  const reachedBottom = useRef(onReachBottom)
  useEffect(() => {
    reachedBottom.current = onReachBottom
  })

  const scrollToBottom = useCallback((behavior: ScrollBehavior = 'auto') => {
    const el = scrollerRef.current
    if (!el) return
    stick.current = true
    el.scrollTo({ top: el.scrollHeight, behavior })
  }, [])

  const captureAnchor = useCallback(() => {
    const el = scrollerRef.current
    if (!el) return
    anchor.current = el.scrollHeight - el.scrollTop
  }, [])

  // Opening another channel starts glued to the bottom again. Adjusting state
  // during render is the sanctioned alternative to a setState inside an effect,
  // which would paint the previous channel's scroll state for a frame.
  const [seenKey, setSeenKey] = useState(resetKey)
  if (seenKey !== resetKey) {
    setSeenKey(resetKey)
    setAtBottom(true)
  }

  // Re-glue and jump to the bottom for the new channel. The ref is set here, not
  // in the render-phase reset above: writing a ref during render is a bug the
  // compiler rejects, and this layout effect runs before paint anyway.
  useLayoutEffect(() => {
    stick.current = true
    const el = scrollerRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [resetKey])

  // The content's height changes when the first page lands, when older pages are
  // prepended, when a new message arrives, and when an image finally decodes.
  // Every one of those is handled here.
  useEffect(() => {
    const el = scrollerRef.current
    const content = contentRef.current
    if (!el || !content) return

    const observer = new ResizeObserver(() => {
      // A prepend: put the viewport back where it was. Distance-from-bottom is the
      // invariant to restore — it survives the list growing by an unknown height
      // above the reader.
      if (anchor.current !== null) {
        el.scrollTop = el.scrollHeight - anchor.current
        anchor.current = null
        return
      }
      if (stick.current) el.scrollTop = el.scrollHeight
    })
    observer.observe(content)
    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    const el = scrollerRef.current
    if (!el) return
    const onScroll = () => {
      const distance = el.scrollHeight - el.scrollTop - el.clientHeight
      const bottom = distance <= BOTTOM_EPSILON
      // Scrolling up releases the glue; scrolling back down restores it.
      stick.current = bottom
      setAtBottom(bottom)
      if (bottom) reachedBottom.current?.()
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [])

  return { scrollerRef, contentRef, atBottom, scrollToBottom, captureAnchor }
}
