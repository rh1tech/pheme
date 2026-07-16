import { useEffect } from 'react'

/**
 * Tells the service worker which conversation or channel is on screen, so it can stay quiet about a
 * message the reader is already looking at (sw.js, isViewing).
 *
 * The worker cannot work this out for itself. It would have to read the client's url, and this app
 * navigates with history.pushState, which does not reliably update it — so the worker could believe
 * the reader was on the list while they were in fact reading the chat, and notify them about the
 * message already on their screen. So the app says so, the way the mobile app does
 * (activeConversationIdProvider).
 *
 * Sent to the registration's active worker rather than navigator.serviceWorker.controller: on the
 * very first load there is no controller yet, and that first load is exactly when a chat opened from
 * a notification is on screen.
 */
export function useActiveChatSync(activeId: string | undefined): void {
  useEffect(() => {
    if (!('serviceWorker' in navigator)) return
    let cancelled = false

    void navigator.serviceWorker.ready
      .then((registration) => {
        if (cancelled) return
        registration.active?.postMessage({ type: 'pheme:active-chat', id: activeId ?? null })
      })
      .catch(() => {
        // No worker (unsupported, or blocked): notifications simply are not suppressed.
      })

    return () => {
      cancelled = true
    }
  }, [activeId])
}
