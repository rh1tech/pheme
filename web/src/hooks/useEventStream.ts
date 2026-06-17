import { useEffect, useRef } from 'react'
import { streamUrl } from '../lib/api'
import type { LiveEvent } from '../lib/types'

/**
 * Subscribes to the App API SSE stream and invokes onEvent for each live
 * message. The handler is kept in a ref so the EventSource is not recreated on
 * every render. Reconnection is handled natively by EventSource.
 */
export function useEventStream(onEvent: (e: LiveEvent) => void): void {
  const handler = useRef(onEvent)
  useEffect(() => {
    handler.current = onEvent
  })

  useEffect(() => {
    const url = streamUrl()
    if (!url) return

    const source = new EventSource(url)
    source.addEventListener('message', (ev) => {
      try {
        const data = JSON.parse((ev as MessageEvent).data) as LiveEvent
        handler.current(data)
      } catch {
        // ignore malformed events
      }
    })

    return () => source.close()
  }, [])
}
