// Mobile "app-like" behaviour helpers.

/**
 * Suppresses pinch-zoom and double-tap-zoom so the web app feels like an
 * installed mobile app. iOS WebKit (which backs every iOS browser, including
 * Firefox/Chrome) ignores the viewport `user-scalable=no` / `maximum-scale`
 * hints, so the gesture must be blocked in JS. Normal one-finger scrolling is
 * left untouched. Safe no-op on platforms without these events.
 */
export function lockViewportZoom(): void {
  if (typeof document === 'undefined') return

  // iOS Safari/WebKit fires non-standard gesture* events for pinch-zoom.
  const preventGesture = (e: Event) => e.preventDefault()
  document.addEventListener('gesturestart', preventGesture)
  document.addEventListener('gesturechange', preventGesture)
  document.addEventListener('gestureend', preventGesture)

  // Belt-and-braces for double-tap-zoom: collapse a second tap that lands
  // within the zoom window. `touch-action: manipulation` (set in CSS) handles
  // most engines; this covers the stragglers without blocking real clicks.
  let lastTouchEnd = 0
  document.addEventListener(
    'touchend',
    (e) => {
      const now = e.timeStamp
      if (now - lastTouchEnd <= 300 && e.touches.length === 0) {
        e.preventDefault()
      }
      lastTouchEnd = now
    },
    { passive: false },
  )
}
