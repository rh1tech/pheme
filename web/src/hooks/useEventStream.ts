import { useEffect, useRef } from 'react'
import { streamUrl } from '../lib/api'
import type { LiveEvent } from '../lib/types'

// One EventSource per tab, shared by every subscriber.
//
// The chat list and the open conversation both listen for live messages. Opening
// a connection per call site would hold several SSE streams against the App API
// from a single tab — each one a long-lived request the server must keep warm —
// so the connection is refcounted here instead: it opens on the first listener
// and closes on the last.

type Listener = (e: LiveEvent) => void

const listeners = new Set<Listener>()
let source: EventSource | null = null

function handleMessage(ev: MessageEvent): void {
  let data: LiveEvent
  try {
    data = JSON.parse(ev.data) as LiveEvent
  } catch {
    return // ignore malformed events
  }
  for (const listener of listeners) listener(data)
}

function open(): void {
  if (source) return
  const url = streamUrl()
  if (!url) return
  source = new EventSource(url)
  // Reconnection is handled natively by EventSource.
  source.addEventListener('message', handleMessage)
}

function close(): void {
  source?.close()
  source = null
}

function subscribe(listener: Listener): () => void {
  listeners.add(listener)
  open()
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0) close()
  }
}

/**
 * Subscribes to the App API live stream. The handler is held in a ref, so a new
 * inline callback on every render does not churn the connection.
 */
export function useEventStream(onEvent: Listener): void {
  const handler = useRef(onEvent)
  useEffect(() => {
    handler.current = onEvent
  })

  useEffect(() => subscribe((e) => handler.current(e)), [])
}
