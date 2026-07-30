// The push worker's one judgement call: stay quiet about a message the reader is already looking at,
// notify about everything else. It is worth testing because it is untestable by hand — you cannot
// hold a phone in the right state and make a push arrive on cue — and because getting it wrong is
// invisible in the direction that matters: you simply get notified about the chat you are reading.
//
// The real public/sw.js is loaded and run against a fake worker global, then driven with real
// 'message' and 'push' events.

import { describe, expect, it, beforeEach } from 'vitest'
// The real worker, verbatim — not a copy of its logic, which would only ever test itself.
import SW_SOURCE from '../../public/sw.js?raw'

interface FakeClient {
  url: string
  visibilityState: 'visible' | 'hidden'
  focused: boolean
}

/** What the worker's preview hook returns: the decrypted line plus the AUTHENTICATED sender. */
interface FakePreview {
  body: string
  senderUserId: string
}

interface Notification {
  title: string
  body: string
  icon?: string
  data?: Record<string, unknown>
}

interface Harness {
  /** Fires the worker's 'push' handler and waits for whatever it kicked off. */
  push: (data: Record<string, unknown>) => Promise<void>
  /** Fires the worker's 'message' handler — how the app names the chat on screen. */
  tell: (message: unknown) => void
  /** Titles the worker actually chose to show. */
  shown: string[]
  /** The full notifications, for the tests that care about more than the title. */
  notifications: Notification[]
}

function load(clients: FakeClient[], preview?: FakePreview | null): Harness {
  const listeners: Record<string, ((event: unknown) => void)[]> = {}
  const shown: string[] = []
  const notifications: Notification[] = []
  const pending: Promise<unknown>[] = []

  const self = {
    addEventListener(type: string, fn: (event: unknown) => void) {
      ;(listeners[type] ??= []).push(fn)
    },
    skipWaiting() {},
    location: { origin: 'https://app.example.com' },
    registration: {
      showNotification(title: string, options: Record<string, unknown> = {}) {
        shown.push(title)
        notifications.push({
          title,
          body: typeof options.body === 'string' ? options.body : '',
          icon: typeof options.icon === 'string' ? options.icon : undefined,
          data: options.data as Record<string, unknown> | undefined,
        })
        return Promise.resolve()
      },
      getNotifications: () => Promise.resolve([]),
    },
    clients: {
      claim: () => Promise.resolve(),
      matchAll: () => Promise.resolve(clients),
      openWindow: () => Promise.resolve(null),
    },
  }

  // The worker imports its preview module at top level; give it something to import when a test
  // wants previews, and leave it throwing (as in a plain Node context) when it does not — which is
  // what every pre-existing test here exercises.
  const importScripts =
    preview === undefined
      ? undefined
      : () => {
          ;(self as Record<string, unknown>).phemeDecryptPreview = () => Promise.resolve(preview)
        }

  // Run the worker with `self` (and a matching bare `clients`) bound to the fakes.
  new Function(
    'self',
    'importScripts',
    `${SW_SOURCE}\n//# sourceURL=sw.js`,
  )(self, importScripts)

  return {
    shown,
    notifications,
    tell(message: unknown) {
      for (const fn of listeners.message ?? []) fn({ data: message })
    },
    async push(data: Record<string, unknown>) {
      const event = {
        data: { json: () => ({ title: 'Alice', body: 'New message', data }) },
        waitUntil: (p: Promise<unknown>) => pending.push(p),
      }
      for (const fn of listeners.push ?? []) fn(event)
      await Promise.all(pending)
    },
  }
}

const CONV = 'conv-1'
const onChat = (over: Partial<FakeClient> = {}): FakeClient => ({
  url: `https://app.example.com/chats/${CONV}`,
  visibilityState: 'visible',
  focused: true,
  ...over,
})

describe('push worker: notifying about a chat you are reading', () => {
  let harness: Harness
  beforeEach(() => {
    harness = load([])
  })

  it('stays quiet when a visible window is on the conversation', async () => {
    harness = load([onChat()])
    await harness.push({ conversationId: CONV, messageId: 'm1' })
    expect(harness.shown).toEqual([])
  })

  // The bug this was reported as: a window can be plainly on screen without holding the OS's focus
  // — another window clicked, a second monitor, a standalone PWA. Demanding focus notified people
  // who were looking straight at the conversation.
  it('stays quiet when the window is visible but not focused', async () => {
    harness = load([onChat({ focused: false })])
    await harness.push({ conversationId: CONV, messageId: 'm1' })
    expect(harness.shown).toEqual([])
  })

  // The app navigates with pushState, which does not reliably update client.url — so the worker can
  // be looking at a stale "/" while the reader is in the chat. The app tells it instead.
  it('stays quiet when the window says it is on the chat even though its url is stale', async () => {
    harness = load([onChat({ url: 'https://app.example.com/' })])
    harness.tell({ type: 'pheme:active-chat', id: CONV })
    await harness.push({ conversationId: CONV, messageId: 'm1' })
    expect(harness.shown).toEqual([])
  })

  it('notifies when the window is hidden, even on that chat', async () => {
    harness = load([onChat({ visibilityState: 'hidden' })])
    await harness.push({ conversationId: CONV, messageId: 'm1' })
    expect(harness.shown).toEqual(['Alice'])
  })

  // Nothing clears the app's word when a tab dies, so it must never suppress on its own.
  it('notifies when nothing is on screen, however stale the last word from the app', async () => {
    harness = load([])
    harness.tell({ type: 'pheme:active-chat', id: CONV })
    await harness.push({ conversationId: CONV, messageId: 'm1' })
    expect(harness.shown).toEqual(['Alice'])
  })

  it('notifies about a DIFFERENT conversation while reading this one', async () => {
    harness = load([onChat()])
    harness.tell({ type: 'pheme:active-chat', id: CONV })
    await harness.push({ conversationId: 'conv-2', messageId: 'm1' })
    expect(harness.shown).toEqual(['Alice'])
  })

  it('clears the active chat when the app leaves it', async () => {
    harness = load([onChat({ url: 'https://app.example.com/' })])
    harness.tell({ type: 'pheme:active-chat', id: CONV })
    harness.tell({ type: 'pheme:active-chat', id: null })
    await harness.push({ conversationId: CONV, messageId: 'm1' })
    expect(harness.shown).toEqual(['Alice'])
  })

  it('always rings a call, even from inside that very chat', async () => {
    harness = load([onChat()])
    harness.tell({ type: 'pheme:active-chat', id: CONV })
    await harness.push({ kind: 'call', conversationId: CONV, callId: 'c1' })
    expect(harness.shown).toEqual(['Alice'])
  })

  it('stays quiet on a channel that is on screen, and notifies on another', async () => {
    harness = load([
      { url: 'https://app.example.com/channels/ch-1', visibilityState: 'visible', focused: false },
    ])
    await harness.push({ channelId: 'ch-1', messageId: 'm1' })
    expect(harness.shown).toEqual([])

    await harness.push({ channelId: 'ch-2', messageId: 'm2' })
    expect(harness.shown).toEqual(['Alice'])
  })
})

// The lock screen is where a name is least likely to be questioned, and the push that carries it is
// composed by the SERVER — the untrusted Delivery Service. The ciphertext inside it is not: MLS
// authenticates the leaf that signed it. When the two disagree, the worker has caught the server
// putting one person's name on another person's words.
describe('push worker: attributing a decrypted preview', () => {
  const decrypted = { conversationId: CONV, messageId: 'm1', senderId: 'user-alice', ciphertext: 'AAAA', groupIds: 'g1' }

  it('shows the decrypted line under the claimed sender when MLS agrees', async () => {
    const harness = load([], { body: 'the real message', senderUserId: 'user-alice' })
    await harness.push(decrypted)
    expect(harness.notifications).toEqual([
      expect.objectContaining({ title: 'Alice', body: 'the real message' }),
    ])
  })

  // The attack: the server relays Bob's ciphertext with Alice's name on the envelope.
  it('strips the claimed identity when MLS names somebody else', async () => {
    const harness = load([], { body: 'the real message', senderUserId: 'user-bob' })
    await harness.push({ ...decrypted, senderId: 'user-alice' })
    const shown = harness.notifications[0]
    expect(shown.body, 'the message itself is real and must still be shown').toBe('the real message')
    expect(shown.title, 'the payload named the wrong person').toBe('Pheme')
    expect(shown.icon, 'the avatar is the payload\'s claim too').toBeUndefined()
    expect(shown.data?.senderId).toBe('')
    expect(shown.data?.senderUnverified).toBe(true)
  })

  it('shows the preview neutrally when the push claims no sender to check against', async () => {
    const harness = load([], { body: 'the real message', senderUserId: 'user-bob' })
    await harness.push({ ...decrypted, senderId: '' })
    expect(harness.notifications[0]).toEqual(expect.objectContaining({
      title: 'Pheme',
      body: 'the real message',
      icon: undefined,
    }))
    expect(harness.notifications[0].data?.senderUnverified).toBe(true)
  })

  it('shows a legacy cached preview neutrally because its sender was never authenticated', async () => {
    const harness = load([], { body: 'from the old cache', senderUserId: '' })
    await harness.push(decrypted)
    expect(harness.notifications[0]).toEqual(expect.objectContaining({
      title: 'Pheme',
      body: 'from the old cache',
      icon: undefined,
    }))
  })

  it('falls back to the generic body when nothing could be decrypted', async () => {
    const harness = load([], null)
    await harness.push(decrypted)
    expect(harness.notifications[0]).toEqual(
      expect.objectContaining({ title: 'Alice', body: 'New message' }),
    )
  })
})
