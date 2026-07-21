import { expect, test, type Browser } from '@playwright/test'
import { login } from './helpers'
import { openChatAndJoin, renderedMessages, send } from './chat-helpers'
import { FED_A_URL, FED_B_URL } from './constants'

// The whole point of federation, proven end to end with real crypto: alice on
// host A and bob on host B — two SEPARATE, independently-deployed Pheme instances
// — exchange an MLS-encrypted message and each reads the other's plaintext. The
// two Go hosts (started by playwright.config) share a signed nodelist and reach
// each other over loopback; nothing here is mocked. The servers move opaque
// ciphertext; only the browsers can read it.
//
// This is what the in-process Go tests and the two-host CI pipeline cannot show:
// that the bytes they relay actually DECRYPT on a client on the far host.

test.skip(({ browserName }) => browserName !== 'chromium', 'crypto round-trip: chromium only')

const PASSWORD = 'Admin12345' // the seeded admin password on both fed hosts

// A browser context pinned to one federation host: the app reads its API base
// from window.__PHEME_CONFIG, so an init script points this whole context at the
// given host before any app code runs.
async function signInOnHost(browser: Browser, apiBase: string, email: string) {
  const context = await browser.newContext()
  // The app reads its API base from /config.js (a page script that runs after any
  // init script and would overwrite it), so pin this whole context to its host by
  // serving that file itself.
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

// A signed-in page's fetch against its own host, carrying its bearer token. The
// method defaults to POST when a body is given and GET otherwise; pass one
// explicitly for PATCH/DELETE.
function api<T>(
  page: import('@playwright/test').Page,
  base: string,
  path: string,
  body?: unknown,
  methodOverride?: string,
) {
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
      return (await res.json()) as unknown
    },
    [base, path, methodOverride ?? (body ? 'POST' : 'GET'), body ? JSON.stringify(body) : ''] as const,
  ) as Promise<T>
}

test('alice on host A and bob on host B exchange an encrypted message', async ({ browser }) => {
  const alice = await signInOnHost(browser, FED_A_URL, 'alice@a.test')
  const bob = await signInOnHost(browser, FED_B_URL, 'bob@b.test')

  // Bob picks a username and a display name on his own host, so alice can address
  // him as a human handle and should see his real name, not a bare id.
  await api(bob.page, FED_B_URL, '/v1/me', { username: 'bobfed', displayName: 'Bobby' }, 'PATCH')

  // Alice (on A) starts a DIRECT chat with bob, who lives on B, addressing him by
  // his handle via B's nodelist ALIAS — `bobfed@hostb`, not the full domain. Her
  // host maps the alias to b.test, resolves the username there over S2S, creates the
  // direct conversation, and provisions the mirror on B. A cross-host 1:1 is a
  // direct chat, not a group.
  const conv = await api<{ id: string; kind: string }>(alice.page, FED_A_URL, '/v1/conversations', {
    kind: 'direct',
    memberIds: ['bobfed@hostb'],
  })
  expect(conv.kind).toBe('direct')

  // Bob's real id and — crucially — his display name crossed the boundary: alice's
  // host has no user row for bob, so a bare id would render as "User". The name
  // rides on the membership.
  const roster = await api<{ members: Array<{ userId: string; user: { displayName?: string } }> }>(
    alice.page,
    FED_A_URL,
    `/v1/conversations/${conv.id}/members`,
  )
  const bobMember = roster.members.find((m) => m.userId === bob.userId)
  expect(bobMember?.user.displayName).toBe('Bobby')

  // Alice opens the chat: her client establishes the MLS group, claims bob's key
  // package from host B (the server routes the claim), and sends.
  await openChatAndJoin(alice.page, conv.id)
  await send(alice.page, 'the eagle lands at dawn')
  await expect(alice.page.getByTestId('chat-message')).toContainText('the eagle lands at dawn')

  // Bob opens the mirror on host B, joins from the relayed Welcome, and DECRYPTS.
  await openChatAndJoin(bob.page, conv.id)
  await expect(bob.page.getByTestId('chat-message')).toContainText('the eagle lands at dawn', {
    timeout: 30_000,
  })

  // And he can reply — which requires he actually joined the group, not just rendered a cache.
  await send(bob.page, 'acknowledged')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText('acknowledged', {
    timeout: 30_000,
  })

  // Neither side is looking at a sealed placeholder — both plaintexts are rendered.
  expect((await renderedMessages(alice.page)).join('\n')).toContain('the eagle lands at dawn')
  expect((await renderedMessages(alice.page)).join('\n')).toContain('acknowledged')

  await Promise.all([alice.context.close(), bob.context.close()])
})
