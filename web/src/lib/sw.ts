// The service worker URL. The `?v=` query is a one-time cache-bust past a stale
// Cloudflare-cached copy of /sw.js (an old version that pinned notification
// behaviour): a new query key is a CDN miss, so every browser fetches the
// current worker. The origin now also sends `Cache-Control: no-store` for the
// worker, so this URL is never edge-cached again. Bump the number only if a CDN
// edge ever caches it despite no-store. The query does not affect the worker's
// scope (still "/").
export const SERVICE_WORKER_URL = '/sw.js?v=7'

// Registers the Pheme service worker on every app load and checks for updates.
// Without this, the worker was only registered when a user enabled push, so
// existing installs kept running an old worker (and old notification-click
// behaviour). The worker skipWaiting()s + claims clients, so a new version takes
// over immediately once fetched.
export function registerServiceWorker(): void {
  if (!('serviceWorker' in navigator)) return
  window.addEventListener('load', () => {
    navigator.serviceWorker
      .register(SERVICE_WORKER_URL)
      .then((registration) => registration.update())
      .catch(() => {
        // Ignore: push/notifications simply stay unavailable in this browser.
      })
  })
}
