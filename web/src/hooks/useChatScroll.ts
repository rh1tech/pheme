/*
 * Parts of this file are derived from Telegram Web K (GPL v3):
 *   https://github.com/morethanwords/tweb — src/helpers/scrollSaver.ts
 * Specifically: anchoring by distance-from-bottom across a prepend, and the
 * one-pixel tolerance for "scrolled to the end". See web/NOTICE.md.
 */
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'

// Telegram treats the feed as "at the end" within a pixel of the bottom
// (scrollSaver.ts: `scrollHeight - Math.ceil(scrollTop + clientHeight) <= 1`).
// A little more slack here, because the jump pill should not flicker into view
// when a fractional layout leaves the feed a few pixels short.
const BOTTOM_EPSILON = 80

// Height of the sticky date pill plus its offset (.pheme-day-pill), which always
// floats at the top of the feed.
const DAY_PILL_CLEARANCE = 40

/** Where the feed holds itself as content grows underneath it. */
type Mode =
  | { kind: 'bottom' }
  /** Keeping a specific message pinned to the top — how a channel opens on its first unread. */
  | { kind: 'message'; messageId: string }
  /** The reader is somewhere in the backlog; leave them alone. */
  | { kind: 'free' }

export interface ChatScrollApi {
  scrollerRef: RefObject<HTMLDivElement | null>
  contentRef: RefObject<HTMLDivElement | null>
  /** True when the reader is at (or very near) the newest message. */
  atBottom: boolean
  scrollToBottom: (behavior?: ScrollBehavior) => void
  /** Pin a message to the top of the viewport and hold it there as content settles. */
  scrollToMessage: (messageId: string) => void
  /**
   * Call immediately before committing a prepend of older messages. It records
   * where the viewport sits relative to the bottom; the restore happens before
   * the browser paints.
   */
  captureAnchor: () => void
}

/**
 * Owns the scroll position of the message feed.
 *
 * The feed is a normal top-to-bottom list (not `flex-direction: column-reverse`)
 * so DOM order matches reading order — find-in-page, text selection, screen
 * readers and sticky date separators all keep working.
 *
 * The central idea is that the feed *holds a position* while its content settles
 * around it. Setting the scroll once, when the channel opens, is not enough: at
 * that moment the messages have not arrived, the bubbles have no height, and no
 * image has decoded. Every one of those grows the content afterwards, which is
 * how a one-shot pin leaves the reader stranded mid-backlog. A ResizeObserver
 * re-applies the position on each growth instead.
 *
 * @param resetKey changing it (opening another channel) restores the default position
 * @param onReachBottom fired when the reader arrives at the newest message
 */
export function useChatScroll(resetKey: string, onReachBottom?: () => void): ChatScrollApi {
  const scrollerRef = useRef<HTMLDivElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const [atBottom, setAtBottom] = useState(true)

  // A ref, not state: the observers below must read the live value without being
  // torn down and rebuilt every time it changes.
  const mode = useRef<Mode>({ kind: 'bottom' })
  // Distance from the bottom captured just before a prepend; null when none pending.
  const anchor = useRef<number | null>(null)
  // Set while the feed moves itself. Writing scrollTop fires a scroll event just
  // like a finger does, and without this the feed's own correction would read as
  // the reader scrolling away — instantly releasing the position it just took.
  const programmatic = useRef(false)
  // How far the reader currently sits from the bottom, kept up to date as they scroll.
  //
  // It has to be recorded as it goes, because the moment it is needed it can no longer be
  // measured: a ResizeObserver fires *after* layout, when the viewport has already shrunk and
  // the old height is gone. Restoring this distance is what keeps the keyboard from covering
  // whatever the reader was in the middle of reading.
  const bottomDistance = useRef(0)

  const reachedBottom = useRef(onReachBottom)
  useEffect(() => {
    reachedBottom.current = onReachBottom
  })

  const setScrollTop = useCallback((top: number) => {
    const el = scrollerRef.current
    if (!el) return
    programmatic.current = true
    el.scrollTop = top
    requestAnimationFrame(() => {
      programmatic.current = false
    })
  }, [])

  /** Puts the message's top edge just below the top of the viewport. */
  const alignMessage = useCallback(
    (messageId: string) => {
      const el = scrollerRef.current
      const content = contentRef.current
      if (!el || !content) return false
      const target = content.querySelector<HTMLElement>(
        `[data-message-id="${CSS.escape(messageId)}"]`,
      )
      if (!target) return false

      // Anchor on the unread divider when the message has one, not on the bubble:
      // the divider is rendered *above* its message, so aiming at the bubble
      // scrolls the very line the reader needs to see off the top of the feed.
      const divider = target.parentElement?.querySelector<HTMLElement>('.pheme-unread-divider')
      const anchorEl = divider ?? target

      // Clear the sticky date pill, which permanently occupies the top of the
      // feed — anchoring flush with the top would park the divider behind it.
      const clearance = divider ? DAY_PILL_CLEARANCE : 8

      const top = anchorEl.getBoundingClientRect().top - content.getBoundingClientRect().top
      setScrollTop(Math.max(0, top - clearance))
      return true
    },
    [setScrollTop],
  )

  const applyMode = useCallback(() => {
    const el = scrollerRef.current
    if (!el) return
    const current = mode.current
    if (current.kind === 'bottom') {
      setScrollTop(el.scrollHeight)
      return
    }
    if (current.kind === 'message') alignMessage(current.messageId)
  }, [alignMessage, setScrollTop])

  const scrollToBottom = useCallback((behavior: ScrollBehavior = 'auto') => {
    const el = scrollerRef.current
    if (!el) return
    mode.current = { kind: 'bottom' }
    el.scrollTo({ top: el.scrollHeight, behavior })
  }, [])

  const scrollToMessage = useCallback(
    (messageId: string) => {
      mode.current = { kind: 'message', messageId }
      alignMessage(messageId)
    },
    [alignMessage],
  )

  const captureAnchor = useCallback(() => {
    const el = scrollerRef.current
    if (!el) return
    // Telegram's ScrollSaver keeps `scrollHeight - scrollTop` for a reversed list:
    // the distance from the bottom survives the list growing by an unknown height
    // above the reader, which a saved scrollTop does not.
    anchor.current = el.scrollHeight - el.scrollTop
  }, [])

  const [seenKey, setSeenKey] = useState(resetKey)
  if (seenKey !== resetKey) {
    setSeenKey(resetKey)
    setAtBottom(true)
  }

  // Opening a channel starts at the bottom. The route may then re-aim at the
  // first unread message once the first page has landed.
  useLayoutEffect(() => {
    mode.current = { kind: 'bottom' }
    anchor.current = null
    const el = scrollerRef.current
    if (el) setScrollTop(el.scrollHeight)
  }, [resetKey, setScrollTop])

  // The content's height changes when the first page lands, when older pages are
  // prepended, when a message arrives, and when an image finally decodes. Every
  // one of those is handled here.
  useEffect(() => {
    const el = scrollerRef.current
    const content = contentRef.current
    if (!el || !content) return

    const observer = new ResizeObserver(() => {
      if (anchor.current !== null) {
        setScrollTop(el.scrollHeight - anchor.current)
        anchor.current = null
        return
      }
      applyMode()
    })
    observer.observe(content)
    return () => observer.disconnect()
  }, [applyMode, setScrollTop])

  // The VIEWPORT's height changes too, and for one reason that matters: the on-screen
  // keyboard. It shrinks the shell, and with it the feed — but not the feed's *content*, so
  // the observer above never hears about it. The reader is left looking at whatever the
  // keyboard did not cover, which is not where they were.
  //
  // Nothing scrolled, so restoring the distance from the bottom puts them back: the last
  // message stays on the last message, and a reader mid-backlog keeps the line they were on.
  useEffect(() => {
    const el = scrollerRef.current
    if (!el) return

    let lastHeight = el.clientHeight
    const observer = new ResizeObserver(() => {
      const height = el.clientHeight
      if (height === lastHeight) return // a width change (rotation, pane resize) moves nothing
      lastHeight = height

      if (mode.current.kind === 'free') {
        setScrollTop(el.scrollHeight - height - bottomDistance.current)
        return
      }
      applyMode()
    })
    observer.observe(el)
    return () => observer.disconnect()
  }, [applyMode, setScrollTop])

  useEffect(() => {
    const el = scrollerRef.current
    if (!el) return
    const onScroll = () => {
      const distance = el.scrollHeight - el.scrollTop - el.clientHeight
      const bottom = distance <= BOTTOM_EPSILON
      bottomDistance.current = Math.max(0, distance)
      setAtBottom(bottom)
      // The reader's own scrolling always wins: reaching the bottom re-sticks,
      // moving away from it releases whatever the feed was holding. The feed's own
      // corrections must not count as the reader moving.
      if (!programmatic.current) {
        mode.current = bottom ? { kind: 'bottom' } : { kind: 'free' }
      }
      if (bottom) reachedBottom.current?.()
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [])

  return { scrollerRef, contentRef, atBottom, scrollToBottom, scrollToMessage, captureAnchor }
}
