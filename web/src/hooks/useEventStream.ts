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
//
// RECONNECTION IS OURS, NOT THE BROWSER'S.
//
// EventSource reconnects on its own, and that is useless to us for two reasons. It
// retries the SAME url, and ours carries an access token good for fifteen minutes —
// so every retry after that replays a dead token. And it only retries a *dropped*
// connection: a non-2xx status is fatal by specification, so the 401 the server
// answers an expired token with kills the EventSource for good.
//
// Together those meant the live stream died fifteen minutes after page load and
// never came back until a reload. Messages stopped arriving live, and — because a
// call rings over this stream — incoming calls silently stopped ringing at all.
//
// So we close on the first error and reopen ourselves, with a fresh token.

type Listener = (e: LiveEvent) => void

const listeners = new Set<Listener>()
// Fired when the stream RE-connects (not the first connect of the tab's life). A
// gap in the stream can drop one-shot events — a conversation deleted on another
// device, say — with no catch-up of their own. So a reconnect is the cue for the
// list hooks to re-fetch and reconcile whatever they missed while it was down.
const reconnectListeners = new Set<() => void>()
let everOpened = false
let source: EventSource | null = null
let reopenTimer: ReturnType<typeof setTimeout> | null = null
let attempt = 0
let hiddenSince = 0

const BACKOFF_BASE_MS = 1_000
const BACKOFF_MAX_MS = 30_000

// How long the tab may be hidden before its stream is presumed dead on return. iOS
// suspends a backgrounded PWA and severs its connections without telling the page:
// readyState still reads OPEN, no error fires, and nothing ever arrives again. That
// is undetectable from inside, so the stream is recycled on the way back in instead.
// One request on app resume is a cheap price for a stream that is actually alive.
const STALE_HIDDEN_MS = 30_000

function handleMessage(ev: MessageEvent): void {
  let data: LiveEvent
  try {
    data = JSON.parse(ev.data) as LiveEvent
  } catch {
    return // ignore malformed events
  }
  for (const listener of listeners) listener(data)
}

function close(): void {
  if (reopenTimer !== null) {
    clearTimeout(reopenTimer)
    reopenTimer = null
  }
  source?.close()
  source = null
}

function scheduleReopen(): void {
  if (reopenTimer !== null || listeners.size === 0) return
  // Exponential, with jitter: every client reconnects at once after a deploy, and
  // an un-jittered backoff would bring them all back in lockstep.
  const delay = Math.min(BACKOFF_BASE_MS * 2 ** attempt, BACKOFF_MAX_MS)
  attempt++
  reopenTimer = setTimeout(
    () => {
      reopenTimer = null
      void open()
    },
    delay * (0.5 + Math.random() / 2),
  )
}

async function open(): Promise<void> {
  if (source || listeners.size === 0) return

  const url = await streamUrl()
  // No session, or the refresh failed — and that path has already signalled the auth
  // failure. Do not retry: reconnecting in a loop against a dead session helps nobody.
  if (!url) return
  // Awaiting the token yielded to the event loop, so re-check: the last listener may
  // have unsubscribed while we were away, and opening now would leak the connection.
  if (listeners.size === 0 || source) return

  const es = new EventSource(url)
  source = es
  es.addEventListener('open', () => {
    attempt = 0
    // The first connect is covered by each subscriber's own mount fetch; only a
    // RE-connect needs a reconcile, so the initial open does not fire this.
    if (everOpened) for (const cb of reconnectListeners) cb()
    everOpened = true
  })
  es.addEventListener('message', handleMessage)
  es.addEventListener('error', () => {
    if (source !== es) return // already superseded
    es.close()
    source = null
    scheduleReopen()
  })
}

/** Recycles a stream that a background suspension may have killed underneath us. */
function onVisibilityChange(): void {
  if (document.visibilityState === 'hidden') {
    hiddenSince = Date.now()
    return
  }
  const wasSuspended = hiddenSince !== 0 && Date.now() - hiddenSince > STALE_HIDDEN_MS
  hiddenSince = 0
  if (listeners.size === 0) return
  if (!wasSuspended && source && source.readyState !== EventSource.CLOSED) return

  attempt = 0 // a deliberate resume, not a failure: reconnect now, do not back off
  close()
  void open()
}

function onOnline(): void {
  if (listeners.size === 0 || source) return
  attempt = 0
  close()
  void open()
}

function subscribe(listener: Listener): () => void {
  const first = listeners.size === 0
  listeners.add(listener)
  if (first) {
    document.addEventListener('visibilitychange', onVisibilityChange)
    window.addEventListener('online', onOnline)
    void open()
  }
  return () => {
    listeners.delete(listener)
    if (listeners.size > 0) return
    document.removeEventListener('visibilitychange', onVisibilityChange)
    window.removeEventListener('online', onOnline)
    attempt = 0
    close()
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

/**
 * Runs `onReconnect` whenever the live stream re-connects after a drop or a
 * background suspension — the moment to re-fetch and reconcile any one-shot event
 * (a remote deletion) that the gap may have swallowed. Held in a ref so a fresh
 * inline callback each render does not re-register.
 */
export function useStreamReconnect(onReconnect: () => void): void {
  const handler = useRef(onReconnect)
  useEffect(() => {
    handler.current = onReconnect
  })

  useEffect(() => {
    const cb = () => handler.current()
    reconnectListeners.add(cb)
    return () => {
      reconnectListeners.delete(cb)
    }
  }, [])
}
