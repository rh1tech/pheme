import { useLayoutEffect, useRef } from 'react'
import type { RefObject } from 'react'

/**
 * Holds a feed's scroll position across a prepend of older messages, synchronously.
 *
 * Call `beforePrepend()` immediately before the state update that adds the older messages. The
 * position is restored in a layout effect — in the same render commit, before the browser paints —
 * keyed on `dep` (pass the messages array). This is deterministic where the scroll hook's
 * ResizeObserver was not: that fires on the NEXT size change, which a net-zero prepend never
 * produces, so the anchor dangled and a later resize consumed it against the wrong height.
 *
 * `pending` is true from `beforePrepend()` until the restore runs. The caller must gate its
 * load-older on it, or a fast second scroll-to-top starts another load and clobbers the anchor
 * before the first has settled — the "second scroll jumps" bug.
 *
 * It also toggles `data-prepending` on the scroller (CSS turns native overflow-anchor off there), so
 * the browser and this manual restore never both compensate for the same inserted height.
 */
export interface PrependAnchor {
  /** True while a prepend's restore is still pending; gate load-older on it. */
  pending: RefObject<boolean>
  /** Capture the anchor right before committing the prepend. */
  beforePrepend: () => void
}

export function usePrependAnchor(
  scrollerRef: RefObject<HTMLDivElement | null>,
  dep: unknown,
): PrependAnchor {
  // Distance from the bottom captured just before the prepend: it survives the list growing by an
  // unknown height above the reader, which a saved scrollTop does not.
  const anchor = useRef<number | null>(null)
  const pending = useRef(false)

  useLayoutEffect(() => {
    if (!pending.current) return
    const el = scrollerRef.current
    if (el && anchor.current !== null) {
      // Same distance from the bottom: the prepend added height above the reader, so this holds
      // whatever they were looking at in place. A no-op when nothing was actually added.
      el.scrollTop = el.scrollHeight - anchor.current
      delete el.dataset.prepending
    }
    anchor.current = null
    pending.current = false
  }, [dep, scrollerRef])

  return {
    pending,
    beforePrepend() {
      const el = scrollerRef.current
      if (!el) return
      anchor.current = el.scrollHeight - el.scrollTop
      el.dataset.prepending = 'true'
      pending.current = true
    },
  }
}
