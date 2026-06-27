// Pheme Web Push service worker.
//
// Receives push events from the browser's push service and displays a
// notification. Registered by the web client after VAPID subscription; the
// server (dispatcher) delivers via the Web Push protocol.

// Activate immediately so an updated worker takes control without a manual
// unregister.
self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

self.addEventListener('push', (event) => {
  let payload = { title: 'Pheme', body: '' }
  if (event.data) {
    try {
      payload = event.data.json()
    } catch {
      payload = { title: 'Pheme', body: event.data.text() }
    }
  }

  const title = payload.title || 'Pheme'
  const options = {
    body: payload.body || '',
    image: payload.image || undefined,
    tag: payload.data && payload.data.channelId ? payload.data.channelId : undefined,
    renotify: true,
    data: payload.data || {},
  }

  event.waitUntil(self.registration.showNotification(title, options))
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  event.waitUntil(self.clients.openWindow('/'))
})
