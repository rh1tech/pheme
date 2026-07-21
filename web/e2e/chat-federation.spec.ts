import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test'
import { login } from './helpers'
import { openChatAndJoin, renderedMessages, send } from './chat-helpers'
import { FED_A_URL, FED_B_URL, FED_B_DOMAIN } from './constants'

// Federation, proven end to end with real crypto: alice on host A and bob on host B
// — two SEPARATE Pheme instances — exchange MLS-encrypted messages and each reads the
// other's plaintext. The two Go hosts (started by playwright.config) share a signed
// nodelist and reach each other over loopback; nothing is mocked. The servers move
// opaque ciphertext; only the browsers can read it.
//
// One device per user for the whole file (beforeAll), not one per test. A fresh device
// each test would leave the previous device's KeyPackages published but dead — zombies
// a later group can claim, which is a test-harness artifact, not a product bug. A single
// long-lived device per user is also what a real person is.

test.skip(({ browserName }) => browserName !== 'chromium', 'crypto round-trip: chromium only')

const PASSWORD = 'Admin12345' // the seeded admin password on both fed hosts

interface Host {
  context: BrowserContext
  page: Page
  userId: string
}

// A browser context pinned to one federation host: the app reads its API base from
// window.__PHEME_CONFIG, so an init script points this whole context at the given host
// before any app code runs.
async function signInOnHost(browser: Browser, apiBase: string, email: string): Promise<Host> {
  const context = await browser.newContext()
  await context.route('**/config.js', (route) =>
    route.fulfill({
      contentType: 'application/javascript',
      body: `window.__PHEME_CONFIG = { apiBase: ${JSON.stringify(apiBase)} };`,
    }),
  )
  await context.addInitScript(() => {
    const w = window as { __phemeSkipRecoveryPrompt?: boolean; __phemeAutoStartFresh?: boolean }
    w.__phemeSkipRecoveryPrompt = true
    w.__phemeAutoStartFresh = true
  })
  const page = await context.newPage()
  await login(page, email, PASSWORD)
  // Wait until this device has published key packages — a peer needs one to add it.
  await expect
    .poll(
      () =>
        page.evaluate(async (base) => {
          const device = localStorage.getItem('pheme.mlsDeviceId') ?? ''
          const res = await fetch(
            `${base}/v1/mls/key-packages/count?deviceId=${encodeURIComponent(device)}`,
            { headers: { Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}` } },
          )
          return res.ok ? ((await res.json()) as { count: number }).count : 0
        }, apiBase),
      { timeout: 20_000 },
    )
    .toBeGreaterThan(0)
  const userId = await page.evaluate(() => {
    const token = localStorage.getItem('pheme.accessToken') ?? ''
    return (JSON.parse(atob(token.split('.')[1] ?? '')) as { sub?: string }).sub ?? ''
  })
  return { context, page, userId }
}

// A signed-in page's fetch against its own host, carrying its bearer token. The method
// defaults to POST when a body is given and GET otherwise; pass one explicitly for
// PATCH/DELETE.
function api<T>(page: Page, base: string, path: string, body?: unknown, methodOverride?: string) {
  return page.evaluate(
    async ([b, p, method, payload]) => {
      const res = await fetch(`${b}${p}`, {
        method: method as string,
        headers: {
          'content-type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}`,
        },
        body: payload ? (payload as string) : undefined,
      })
      if (!res.ok) throw new Error(`${p} -> ${res.status}`)
      const text = await res.text() // 204 (delete) has none; JSON.parse('') throws
      return (text ? JSON.parse(text) : null) as unknown
    },
    [base, path, methodOverride ?? (body ? 'POST' : 'GET'), body ? JSON.stringify(body) : ''] as const,
  ) as Promise<T>
}

interface ConvList {
  conversations: Array<{ id: string }>
}
interface Roster {
  members: Array<{ userId: string; user: { displayName?: string } }>
}

test.describe.serial('federation', () => {
  let alice: Host
  let bob: Host

  test.beforeAll(async ({ browser }) => {
    alice = await signInOnHost(browser, FED_A_URL, 'alice@a.test')
    bob = await signInOnHost(browser, FED_B_URL, 'bob@b.test')
    // Usernames + display names, so each can address the other by handle and see a
    // real name rather than a bare id.
    await api(alice.page, FED_A_URL, '/v1/me', { username: 'alicefed', displayName: 'Alice A' }, 'PATCH')
    await api(bob.page, FED_B_URL, '/v1/me', { username: 'bobfed', displayName: 'Bobby' }, 'PATCH')
  })

  test.afterAll(async () => {
    await Promise.all([alice?.context.close(), bob?.context.close()])
  })

  // Every test starts from an empty conversation list on both hosts, so one test's
  // chats never leak into the next (a stale direct chat would dedup a later create).
  test.afterEach(async () => {
    for (const h of [
      { d: alice, url: FED_A_URL },
      { d: bob, url: FED_B_URL },
    ]) {
      const list = await api<ConvList>(h.d.page, h.url, '/v1/conversations').catch(() => ({ conversations: [] }))
      for (const c of list.conversations) {
        await api(h.d.page, h.url, `/v1/conversations/${c.id}`, undefined, 'DELETE').catch(() => {})
      }
    }
  })

  test('direct chat: names cross, both sides decrypt', async () => {
    const conv = await api<{ id: string; kind: string }>(alice.page, FED_A_URL, '/v1/conversations', {
      kind: 'direct',
      memberIds: ['bobfed@hostb'],
    })
    expect(conv.kind).toBe('direct')

    // Bob's display name rode across the boundary — alice's host has no user row for him.
    const roster = await api<Roster>(alice.page, FED_A_URL, `/v1/conversations/${conv.id}/members`)
    expect(roster.members.find((m) => m.userId === bob.userId)?.user.displayName).toBe('Bobby')

    await openChatAndJoin(alice.page, conv.id)
    await send(alice.page, 'the eagle lands at dawn')
    await expect(alice.page.getByTestId('chat-message')).toContainText('the eagle lands at dawn')

    await openChatAndJoin(bob.page, conv.id)
    await expect(bob.page.getByTestId('chat-message')).toContainText('the eagle lands at dawn', {
      timeout: 30_000,
    })
    await send(bob.page, 'acknowledged')
    await expect(alice.page.getByTestId('chat-message').last()).toContainText('acknowledged', {
      timeout: 30_000,
    })
    expect((await renderedMessages(alice.page)).join('\n')).toContain('acknowledged')
  })

  // One conversation per pair no matter who starts it, and delete reaches both — the
  // bug that split a chat into two MLS groups and made messages look undecryptable.
  test('direct chat dedups both ways and deletes on both sides', async () => {
    const c1 = await api<{ id: string }>(alice.page, FED_A_URL, '/v1/conversations', {
      kind: 'direct',
      memberIds: ['bobfed@hostb'],
    })
    const c2 = await api<{ id: string }>(alice.page, FED_A_URL, '/v1/conversations', {
      kind: 'direct',
      memberIds: ['bobfed@hostb'],
    })
    expect(c2.id).toBe(c1.id)
    // Bob starting it back the OTHER way lands on the same one — the mirror carries the
    // dedup key, which is what stops each host making its own.
    const c3 = await api<{ id: string }>(bob.page, FED_B_URL, '/v1/conversations', {
      kind: 'direct',
      memberIds: ['alicefed@hosta'],
    })
    expect(c3.id).toBe(c1.id)
    const bobList = await api<ConvList>(bob.page, FED_B_URL, '/v1/conversations')
    expect(bobList.conversations.filter((c) => c.id === c1.id)).toHaveLength(1)

    await api(alice.page, FED_A_URL, `/v1/conversations/${c1.id}`, undefined, 'DELETE')
    await expect
      .poll(
        async () => {
          const list = await api<ConvList>(bob.page, FED_B_URL, '/v1/conversations')
          return list.conversations.some((c) => c.id === c1.id)
        },
        { timeout: 15_000 },
      )
      .toBe(false)
  })

  // A sender cannot MLS-decrypt their OWN message; its plaintext comes from a local
  // cache keyed by the message id. So the id the sender caches under MUST equal the id
  // the message is stored under — and that is subtle across hosts, because BOTH the hub
  // (alice, the conversation's creator) and the MIRROR (bob, on the other host) can
  // send, and the mirror's message makes a round trip to the hub for its id.
  //
  // The mirror-sender case is the one that broke: bob's message was forwarded to the
  // hub, which assigned an id, but the mirror stored its own copy under a FRESH local id
  // instead of the hub's — so bob's cached plaintext (keyed to the hub's id from the
  // POST response) never matched the stored message, and bob's own message rendered as
  // "not decrypted" while alice read it fine. This test sends from BOTH sides and
  // reloads BOTH, so each must still read its own words.
  test('both the hub and the mirror sender read their own message after a reload', async () => {
    const conv = await api<{ id: string }>(alice.page, FED_A_URL, '/v1/conversations', {
      kind: 'direct',
      memberIds: ['bobfed@hostb'],
    })
    await openChatAndJoin(alice.page, conv.id)
    await send(alice.page, 'alice speaks') // hub sender
    await openChatAndJoin(bob.page, conv.id)
    await expect(bob.page.getByTestId('chat-message')).toContainText('alice speaks', { timeout: 30_000 })
    await send(bob.page, 'bob replies') // MIRROR sender — the path that broke
    await expect(alice.page.getByTestId('chat-message').last()).toContainText('bob replies', {
      timeout: 30_000,
    })

    // Reload BOTH and reopen. After a reload a sender can only render its own message
    // from the plaintext cache, so this is where an id mismatch surfaces.
    await alice.page.reload()
    await openChatAndJoin(alice.page, conv.id)
    await bob.page.reload()
    await openChatAndJoin(bob.page, conv.id)

    // Each reads BOTH messages — crucially, its OWN one.
    await expect
      .poll(async () => (await renderedMessages(alice.page)).join('\n'), { timeout: 30_000 })
      .toContain('alice speaks')
    expect((await renderedMessages(alice.page)).join('\n')).toContain('bob replies')
    await expect
      .poll(async () => (await renderedMessages(bob.page)).join('\n'), { timeout: 30_000 })
      .toContain('bob replies')
    expect((await renderedMessages(bob.page)).join('\n')).toContain('alice speaks')
  })

  // A GROUP with a member on another host: add by handle, exchange both ways, the remote
  // member's name shows, and leaving drops it from the leaver's list.
  test('group: add a remote member, exchange, and leave', async () => {
    const conv = await api<{ id: string; kind: string }>(alice.page, FED_A_URL, '/v1/conversations', {
      kind: 'group',
      title: 'Cross Crew',
      memberIds: [],
    })
    expect(conv.kind).toBe('group')
    await api(alice.page, FED_A_URL, `/v1/conversations/${conv.id}/members`, { userId: 'bobfed@hostb' })

    const roster = await api<Roster>(alice.page, FED_A_URL, `/v1/conversations/${conv.id}/members`)
    expect(roster.members.find((m) => m.userId === bob.userId)?.user.displayName).toBe('Bobby')

    await openChatAndJoin(alice.page, conv.id)
    await send(alice.page, 'crew assemble')
    await openChatAndJoin(bob.page, conv.id)
    await expect(bob.page.getByTestId('chat-message')).toContainText('crew assemble', { timeout: 30_000 })
    await send(bob.page, 'present')
    await expect(alice.page.getByTestId('chat-message').last()).toContainText('present', { timeout: 30_000 })

    // Bob leaves; it drops from HIS list at once.
    await api(bob.page, FED_B_URL, `/v1/conversations/${conv.id}/members/${bob.userId}`, undefined, 'DELETE')
    const bobList = await api<ConvList>(bob.page, FED_B_URL, '/v1/conversations')
    expect(bobList.conversations.some((c) => c.id === conv.id)).toBe(false)
  })

  // Channels federate too: subscribing to a channel on another host over S2S creates a
  // local MIRROR of it. The subscribe (and mirror creation) is app-driven and proven
  // here; the broadcast FAN-OUT to remote subscribers runs in the dispatcher, which
  // this app-only harness does not run — that half is covered by the in-Docker
  // federation-e2e (test/federation-e2e), which stands up full stacks.
  test('channel: subscribing to a remote channel creates a mirror', async () => {
    // Open mode: only open channels federate (an approval queue cannot yet model a
    // remote subscriber). The create default is approval, so ask for open explicitly.
    const ch = await api<{ id: string; publicId: string }>(bob.page, FED_B_URL, '/v1/channels', {
      name: 'Fed News',
      subscriptionMode: 'open',
    })
    expect(ch.publicId).toBeTruthy()

    // Subscribe from host A by `publicId@domain` (channels resolve the full domain, not
    // the alias). This S2S-subscribes on B and stands up A's local mirror.
    const joined = await api<{ channel: { id: string; name: string; originPublicId?: string; originDomain?: string } }>(
      alice.page,
      FED_A_URL,
      '/v1/channels/join-remote',
      { ref: `${ch.publicId}@${FED_B_DOMAIN}` },
    )
    expect(joined.channel.originPublicId).toBe(ch.publicId)
    expect(joined.channel.originDomain).toBe(FED_B_DOMAIN)
    expect(joined.channel.name).toBe('Fed News')

    // And it is discoverable as one of alice's channels (the subscriber owns their mirror).
    const owned = await api<{ channels: Array<{ id: string; originPublicId?: string }> }>(
      alice.page,
      FED_A_URL,
      '/v1/channels',
    )
    expect(owned.channels.some((c) => c.originPublicId === ch.publicId)).toBe(true)

    await api(bob.page, FED_B_URL, `/v1/channels/${ch.id}`, undefined, 'DELETE').catch(() => {})
    await api(alice.page, FED_A_URL, `/v1/channels/${joined.channel.id}`, undefined, 'DELETE').catch(() => {})
  })
})
