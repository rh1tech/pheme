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
  const data = payload.data || {}
  const options = {
    body: payload.body || '',
    image: payload.image || undefined,
    // One notification per conversation or channel, replaced as newer ones arrive.
    tag: data.conversationId || data.channelId || undefined,
    renotify: true,
    data,
  }

  event.waitUntil(self.registration.showNotification(title, options))
})

// Where a notification lands when tapped. A chat notification carries only a
// conversation id: its body is encrypted, so there is nothing to show until the app
// opens the chat and decrypts it there.
//
// The `from=push` marker tells the app this deep link was a deliberate tap, so its
// cold-launch-to-list redirect (which sends a phone that merely reopened on an old
// channel back to the list) leaves it alone.
function targetPath(data) {
  if (data.conversationId) return `/chats/${data.conversationId}?from=push`
  if (data.channelId && data.messageId) {
    return `/channels/${data.channelId}/messages/${data.messageId}?from=push`
  }
  return '/'
}

self.addEventListener('notificationclick', (event) => {
  event.notification.close()

  const data = event.notification.data || {}
  const target = new URL(targetPath(data), self.location.origin).href

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
