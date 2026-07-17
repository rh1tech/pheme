import { expect, type Browser, type BrowserContext, type Page } from '@playwright/test'
import { API_URL } from './constants'
import { login } from './helpers'

/**
 * Helpers for the encrypted-chat specs.
 *
 * The one that matters is `signInOnNewDevice`. A "device" here is a fresh
 * BrowserContext: its own localStorage (so its own MLS device id) and its own IndexedDB
 * (so its own key material). That is exactly what a second device is to this app — a
 * second browser, a phone, an installed PWA — and it is the thing none of the old specs
 * exercised. Two tabs of one context share both, so they are one device, which is why
 * the tab tests never caught any of this.
 */

/** One signed-in device: a context of its own, so its own keys and its own device id. */
export interface Device {
  context: BrowserContext
  page: Page
  userId: string
  deviceId: string
}

/**
 * Signs `email` in on a BRAND NEW device and waits until its KeyPackages are published.
 *
 * The wait is not incidental. A device that has not published keys cannot be added to a
 * group, so a conversation started before it publishes simply will not contain it — and
 * the test would be asserting against a race rather than against the code.
 */
export async function signInOnNewDevice(
  browser: Browser,
  email: string,
  password: string,
): Promise<Device> {
  const context = await browser.newContext()
  const page = await context.newPage()
  await login(page, email, password)
  await expect.poll(() => keyPackageCount(page), { timeout: 20_000 }).toBeGreaterThan(0)
  return {
    context,
    page,
    userId: await userId(page),
    deviceId: await deviceId(page),
  }
}

/** The signed-in user's own id, taken from their access token. */
export function userId(page: Page): Promise<string> {
  return page.evaluate(() => {
    const token = localStorage.getItem('pheme.accessToken') ?? ''
    const payload = JSON.parse(atob(token.split('.')[1] ?? '')) as { sub?: string }
    return payload.sub ?? ''
  })
}

/** This browser's MLS device id — its leaf in every group it belongs to. */
export function deviceId(page: Page): Promise<string> {
  return page.evaluate(() => localStorage.getItem('pheme.mlsDeviceId') ?? '')
}

/** How many single-use KeyPackages this device still has published. */
export function keyPackageCount(page: Page): Promise<number> {
  return page.evaluate(async (base: string) => {
    const deviceId = localStorage.getItem('pheme.mlsDeviceId') ?? ''
    const res = await fetch(
      `${base}/v1/mls/key-packages/count?deviceId=${encodeURIComponent(deviceId)}`,
      { headers: { Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}` } },
    )
    if (!res.ok) return 0
    return ((await res.json()) as { count: number }).count
  }, API_URL)
}

/** The conversation's MLS group id and epoch, straight from the server. */
export function groupState(page: Page, conversationId: string): Promise<{ groupId: string; epoch: number }> {
  return page.evaluate(
    async ([base, conv]) => {
      const res = await fetch(`${base}/v1/conversations/${conv}/mls`, {
        headers: { Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}` },
      })
      if (!res.ok) return { groupId: '', epoch: 0 }
      return (await res.json()) as { groupId: string; epoch: number }
    },
    [API_URL, conversationId] as const,
  )
}

/** Starts a direct chat via the API, so the test does not depend on the user picker. */
export function startDirectChat(page: Page, otherId: string): Promise<string> {
  return page.evaluate(
    async ([base, other]) => {
      const res = await fetch(`${base}/v1/conversations`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}`,
        },
        body: JSON.stringify({ kind: 'direct', memberIds: [other] }),
      })
      return ((await res.json()) as { id: string }).id
    },
    [API_URL, otherId] as const,
  )
}

/** Creates a group conversation via the API. */
export function createGroup(page: Page, title: string, memberIds: string[]): Promise<string> {
  return page.evaluate(
    async (args: { base: string; title: string; memberIds: string[] }) => {
      const res = await fetch(`${args.base}/v1/conversations`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}`,
        },
        body: JSON.stringify({ kind: 'group', title: args.title, memberIds: args.memberIds }),
      })
      return ((await res.json()) as { id: string }).id
    },
    { base: API_URL, title, memberIds },
  )
}

/** Sets a display name so the user is findable in the people search. */
export function setDisplayName(page: Page, name: string): Promise<void> {
  return page.evaluate(
    async ([base, displayName]) => {
      await fetch(`${base}/v1/me`, {
        method: 'PATCH',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}`,
        },
        body: JSON.stringify({ displayName }),
      })
    },
    [API_URL, name] as const,
  )
}

/** Types a message into the chat composer and sends it. */
export async function send(page: Page, text: string): Promise<void> {
  const composer = page.getByTestId('composer')
  await composer.getByTestId('composer-body').fill(text)
  await composer.getByRole('button', { name: 'Send' }).click()
}

/**
 * Opens a conversation and waits until this device is actually IN its encrypted group.
 *
 * A device that has just signed in is a member of the conversation but not yet of the
 * group: it has to announce itself and be admitted. Until then it can neither send nor
 * read anything.
 *
 * The wait is on `data-joined`, which is true only once the device holds the group. Do not
 * be tempted to wait for the "joining" banner to disappear instead — that banner is also
 * absent while the conversation is still loading, so the wait would pass instantly and the
 * test would race ahead and send into a group it had not joined. (It did exactly that.)
 */
export async function openChatAndJoin(page: Page, conversationId: string): Promise<void> {
  await page.goto(`/chats/${conversationId}`)
  await expect(page.locator('[data-testid="composer"][data-joined="true"]')).toBeVisible({
    timeout: 30_000,
  })
}

/** The text of every message bubble currently rendered, in order. */
export function renderedMessages(page: Page): Promise<string[]> {
  return page.getByTestId('chat-message').allInnerTexts()
}

/** The published-package stock of an EXPLICIT device of the signed-in user — not this browser's. */
export function keyPackageCountFor(
  page: Page,
  deviceId: string,
): Promise<{ count: number; hasLastResort: boolean }> {
  return page.evaluate(
    async ([base, device]) => {
      const res = await fetch(`${base}/v1/mls/key-packages/count?deviceId=${encodeURIComponent(device)}`, {
        headers: { Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}` },
      })
      if (!res.ok) return { count: 0, hasLastResort: false }
      return (await res.json()) as { count: number; hasLastResort: boolean }
    },
    [API_URL, deviceId] as const,
  )
}

/** Publishes a raw key-package payload as the signed-in user — the test's way to poison the directory. */
export function publishKeyPackagesRaw(page: Page, body: unknown): Promise<number> {
  return page.evaluate(
    async ([base, payload]) => {
      const res = await fetch(`${base}/v1/mls/key-packages`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}`,
        },
        body: payload as string,
      })
      return res.status
    },
    [API_URL, JSON.stringify(body)] as const,
  )
}

/** Purges a device's published key packages (owner only), as a retiring client would. */
export function deleteKeyPackagesFor(page: Page, deviceId: string): Promise<number> {
  return page.evaluate(
    async ([base, device]) => {
      const res = await fetch(`${base}/v1/mls/key-packages?deviceId=${encodeURIComponent(device)}`, {
        method: 'DELETE',
        headers: { Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}` },
      })
      return res.status
    },
    [API_URL, deviceId] as const,
  )
}

/**
 * Posts a Commit whose bytes are garbage — server-accepted (Commits are opaque to it), but one
 * no client can ever apply. This is what a forked device's history looks like to everyone
 * else, and it is the test's way of wedging every member at once.
 */
export function postJunkCommit(
  page: Page,
  conversationId: string,
  groupId: string,
  baseEpoch: number,
): Promise<number> {
  return page.evaluate(
    async (args: { base: string; conv: string; groupId: string; baseEpoch: number }) => {
      const res = await fetch(`${args.base}/v1/conversations/${args.conv}/mls/commit`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}`,
        },
        body: JSON.stringify({
          groupId: args.groupId,
          baseEpoch: args.baseEpoch,
          commit: btoa('not an mls commit, and never will be'),
        }),
      })
      return res.status
    },
    { base: API_URL, conv: conversationId, groupId, baseEpoch },
  )
}

/** The byte length of the sealed transcript the server currently holds (0 if none) — a proxy for "has auto-backup re-uploaded with more history yet". */
export function backupTranscriptLen(page: Page): Promise<number> {
  return page.evaluate(async (base) => {
    const res = await fetch(`${base}/v1/mls/key-backup`, {
      headers: { Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}` },
    })
    if (!res.ok) return 0
    const body = (await res.json()) as { transcriptCiphertext?: string | null }
    return body.transcriptCiphertext ? body.transcriptCiphertext.length : 0
  }, API_URL)
}

/** Retires the conversation's current MLS group, exactly as a stuck client would. */
export function resetGroup(page: Page, conversationId: string): Promise<number> {
  return page.evaluate(
    async ([base, conv]) => {
      const res = await fetch(`${base}/v1/conversations/${conv}/mls/reset`, {
        method: 'POST',
        headers: { Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}` },
      })
      return res.status
    },
    [API_URL, conversationId] as const,
  )
}
