// Pheme Web Push service worker.
//
// Receives push events from the browser's push service and displays a
// notification. Registered by the web client after VAPID subscription; the
// server (dispatcher) delivers via the Web Push protocol.

// Message previews: decrypting a push's ciphertext, in here, to show what was actually said.
//
// The server cannot do this — it holds only ciphertext — so it ships the ciphertext and this
// worker decrypts it against a SNAPSHOT of the device's MLS state, which it never writes back.
// See mls/preview.js for why that is safe; the short version is that the page keeps its own
// unconsumed copy of the key and reads the message again for real when the app opens.
//
// AT TOP LEVEL, and it has to be. This was loaded lazily inside the push handler, on the reasoning
// that a user without previews should not pay for the code — and it silently never worked.
//
// A service worker's importScripts() may only fetch a URL that is already in its script resource
// map, and a URL only gets there by being imported during initial evaluation or the install event
// (Service Workers spec §6.3.2). Called from a push handler, on a worker that is "activated", it
// throws NetworkError. The catch below turned that into a generic notification, so every preview
// quietly degraded to "New message" with nothing anywhere to say why.
//
// The 1.2 MB of WASM is still lazy, which is the part that actually mattered: it is fetched by
// wasm_bindgen inside preview.js, and fetch() has no such lifecycle restriction.
//
// Wrapped, because a service worker whose top-level script throws does not install AT ALL — and
// that would take calls and every other notification down with it. Previews are worth having;
// they are not worth the whole worker.
let previewLoaded = false
try {
  // The ?v= is not decoration. nginx now sends no-store for this path, but a header cannot reach
  // back and invalidate a response a browser has already stored — and every device that loaded the
  // app during the four-hour window has the old copy. Changing the URL is the only thing that
  // bypasses it. Bump this whenever preview.js changes in a way the worker depends on.
  importScripts('/mls/preview.js?v=3')
  previewLoaded = true
} catch (e) {
  // An old deploy, a 404, a cache miss. Notifications carry on without previews.
  console.warn('pheme sw: previews unavailable', e)
}

// The message text for a push, or null to fall back to the server's generic body.
//
// Every failure here is silent and ends in that fallback: no key material, a group this device
// cannot read, a browser that cannot run the WASM. A notification that says "New message" is a
// working notification; one that says nothing because a decrypt threw is not.
async function previewBody(data) {
  if (!data.ciphertext || !data.conversationId) return null
  if (!previewLoaded || typeof self.phemeDecryptPreview !== 'function') return null
  try {
    return await self.phemeDecryptPreview(data.conversationId, data.ciphertext)
  } catch {
    return null
  }
}

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
  // The same `image` field means two different things depending on what sent the push, and the two
  // must not be rendered the same way.
  //
  // A CHAT push carries the sender's avatar — a face, which belongs in the small round slot beside
  // the title (`icon`). A CHANNEL push carries the post's own photograph, which is the point of the
  // notification and belongs in the large hero slot below the text (`image`). Putting an avatar in
  // `image` blows a 40px face up to full width; putting a photo in `icon` shrinks it to a thumbnail
  // nobody can read.
  const isChat = Boolean(data.conversationId)
  const title = payload.title || 'Pheme'
  const options = {
    body: payload.body || '',
    // Absent on a chat when the sender has no avatar, or when the recipient asked not to be told
    // who is messaging them — the server decides which, and simply sends nothing.
    icon: isChat ? payload.image || undefined : undefined,
    image: isChat ? undefined : payload.image || undefined,
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
      // Decrypt AFTER the isViewing check, never before: a user already looking at the chat gets
      // no notification, so decrypting for them would be work done to throw away — and it would
      // mean touching key material on every message of an open conversation rather than only on
      // the ones that actually raise a banner.
      if (!isCall) {
        const decrypted = await previewBody(data)
        if (decrypted) options.body = decrypted
      }
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
