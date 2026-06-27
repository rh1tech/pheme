// Registers the Pheme service worker on every app load and checks for updates.
// Without this, the worker was only registered when a user enabled push, so
// existing installs kept running an old worker (and old notification-click
// behaviour). The worker skipWaiting()s + claims clients, so a new version takes
// over immediately once fetched.
export function registerServiceWorker(): void {
  if (!('serviceWorker' in navigator)) return
  window.addEventListener('load', () => {
    navigator.serviceWorker
      .register('/sw.js')
      .then((registration) => registration.update())
      .catch(() => {
        // Ignore: push/notifications simply stay unavailable in this browser.
      })
  })
}
