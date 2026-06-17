// Pheme Web Push service worker (placeholder).
//
// In the Web phase this will receive push events and display notifications.
// Registering it requires VAPID keys configured on the server. See
// docs/ARCHITECTURE.md (Web Push).

self.addEventListener('push', (event) => {
  let payload = { title: 'Pheme', body: '' }
  try {
    if (event.data) payload = event.data.json()
  } catch {
    // ignore malformed payloads
  }
  event.waitUntil(
    self.registration.showNotification(payload.title ?? 'Pheme', {
      body: payload.body ?? '',
      icon: '/favicon.svg',
    }),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  event.waitUntil(self.clients.openWindow('/'))
})
