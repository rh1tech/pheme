// Pheme Web Push service worker.
//
// Receives push events from the browser's push service and displays a
// notification. Registered by the web client after VAPID subscription; the
// server (dispatcher) delivers via the Web Push protocol.

// Activate immediately so an updated worker takes control without a manual
// unregister.
self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

// The conversation (or channel) the app says is on screen, as the app last told us.
//
// The app TELLS us rather than us reading it off client.url, because a client's url is the
// document's — and this app navigates with history.pushState, which does not reliably update it.
// A worker that reads the url can therefore still believe the reader is sitting on "/" while they
// are in fact looking at the chat, and notify them about the message already on their screen.
//
// Trusting it alone would be worse than the url, mind: nothing clears it if the tab dies. So it is
// only ever consulted alongside a VISIBLE client (see isViewing) — no visible client, no
// suppression, whatever this says.
let activeChatId = null

self.addEventListener('message', (event) => {
  const data = event.data
  if (data && data.type === 'pheme:active-chat') {
    activeChatId = data.id || null
  }
})

self.addEventListener('push', (event) => {
  let payload = { title: 'Pheme', body: '' }
  if (event.data) {
    try {
      payload = event.data.json()
    } catch {
      payload = { title: 'Pheme', body: event.data.text() }
    }
  }

  const data = payload.data || {}

  // A call that is no longer ringing — cancelled, missed, or answered on another device.
  //
  // Close the notification rather than show a second one. Without this a missed call leaves a
  // live-looking ring sitting on the lock screen, and tapping it deep-links into a call that
  // nobody is on any more. Nothing here shows anything: it takes something away.
  if (data.kind === 'call-cancel') {
    event.waitUntil(
      (async () => {
        const open = await self.registration.getNotifications({ tag: data.callId })
        for (const n of open) n.close()
      })(),
    )
    return
  }

  const isCall = data.kind === 'call'

  // Suppress a message notification for a chat the user is already looking at: it is on their
  // screen over the live stream, so a second buzz is only noise. See isViewing. Calls are exempt —
  // a ringing call must always surface, even from inside the chat.
  const title = payload.title || 'Pheme'
  const options = {
    body: payload.body || '',
    image: payload.image || undefined,
    // One notification per call, per conversation, or per channel — replaced as newer ones
    // arrive. A call is tagged by the CALL, not the conversation: it has to be closable on its
    // own when it stops ringing, and it must not be replaced by an ordinary message.
    tag: isCall ? data.callId : data.conversationId || data.channelId || undefined,
    renotify: true,
    // A ringing phone should not quietly dismiss itself after a few seconds.
    requireInteraction: isCall,
    // Deliberately no action buttons. iOS Safari does not render notification actions at all,
    // so an "Answer" button would simply be missing there — and a call feature that looks
    // different depending on where the notification lands is worse than one that always says
    // "tap to open".
    data,
  }

  event.waitUntil(
    (async () => {
      if (!isCall && (await isViewing(data))) return
      await self.registration.showNotification(title, options)
    })(),
  )
})

// Whether a window is currently SHOWING the chat (or channel) this push is about. Checked only for
// messages — never calls.
//
// A VISIBLE client is the requirement, not a focused one. Visible is the Push API's own bar for
// letting a worker stay quiet, and it is what the reader means: the chat is on screen, the message
// is already on it over the live stream, so buzzing them about it is noise. Demanding focus as well
// notified people who were plainly looking at the conversation — a window can be visible without
// holding the OS's focus (another window clicked, a second monitor, a standalone PWA), and this
// asked for both.
//
// A hidden or backgrounded tab has no visible client and so still notifies, which is the point.
async function isViewing(data) {
  const id = data.conversationId || data.channelId
  if (!id) return false
  const target = data.conversationId ? `/chats/${data.conversationId}` : `/channels/${data.channelId}`

  const clients = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
  const visible = clients.filter((client) => {
    if (client.visibilityState !== 'visible') return false
    try {
      return new URL(client.url).origin === self.location.origin
    } catch {
      return false
    }
  })
  if (visible.length === 0) return false

  // What the window says it is showing. Authoritative where it exists, because pushState leaves
  // client.url behind; gated on a visible client above, so a stale value cannot silence anything.
  if (activeChatId && activeChatId === id) return true

  // Otherwise fall back to the url — right on a fresh load, and all a restarted worker has.
  return visible.some((client) => {
    const path = new URL(client.url).pathname
    // Exact match, or a nested route under it (a channel's /messages/:id deep link).
    return path === target || path.startsWith(`${target}/`)
  })
}

// Where a notification lands when tapped. A chat notification carries only a
// conversation id: its body is encrypted, so there is nothing to show until the app
// opens the chat and decrypts it there.
//
// The `from=push` marker tells the app this deep link was a deliberate tap, so its
// cold-launch-to-list redirect (which sends a phone that merely reopened on an old
// channel back to the list) leaves it alone.
function targetPath(data) {
  if (data.kind === 'call' && data.conversationId && data.callId) {
    // The call id goes in the URL because a cold-launched app has no live stream yet and so
    // never saw the invite. It reads the call out of the mailbox instead, and rings.
    return `/chats/${data.conversationId}?from=push&call=${encodeURIComponent(data.callId)}`
  }
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
