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

  // Deep-link to the specific message when the server provided the ids; else
  // fall back to the app root.
  const data = event.notification.data || {}
  const path =
    data.channelId && data.messageId
      ? `/channels/${data.channelId}/messages/${data.messageId}`
      : '/'
  const target = new URL(path, self.location.origin).href

  event.waitUntil(
    (async () => {
      const all = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
      // Reuse a same-origin window if one exists: focus it and navigate there.
      for (const client of all) {
        if (new URL(client.url).origin !== self.location.origin) continue
        try {
          await client.focus()
          if ('navigate' in client) await client.navigate(target)
          return
        } catch {
          // navigate() can reject for an uncontrolled client; fall through and
          // open a fresh window so the deep-link still lands.
          break
        }
      }
      await self.clients.openWindow(target)
    })(),
  )
})
