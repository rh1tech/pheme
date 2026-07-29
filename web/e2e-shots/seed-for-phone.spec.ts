import { test, expect } from '@playwright/test'
import {
  signInOnNewDevice,
  startDirectChat,
  createGroup,
  send,
  openChatAndJoin,
  renderedMessages,
} from '../e2e/chat-helpers'

/**
 * A small, fast seed for the phone.
 *
 * It runs while a phone is signed in and holding still, so the conversations it
 * creates include that phone's freshly published key packages — which is the
 * only way the phone can read them. flutter drive uninstalls the app at the end
 * of every run, taking the MLS key store with it, so nothing seeded before the
 * run is ever readable.
 *
 * Deliberately tiny. The full set in shots.spec.ts takes about a hundred
 * seconds, and the Flutter driver loses the app if it is held still that long.
 */

const PW = 'orchard-lantern-97'
const at = (s: string) => `${s}@pheme.test`

test('seed a few conversations the phone can read', async ({ browser }) => {
  test.setTimeout(180_000)

  const maya = await signInOnNewDevice(browser, at('maya'), PW)
  const kenji = await signInOnNewDevice(browser, at('kenji'), PW)

  const priyaId = await maya.page.evaluate(async (base: string) => {
    const res = await fetch(`${base}/v1/users/search?q=priya`, {
      headers: { Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}` },
    })
    const body = (await res.json()) as { users?: Array<{ id: string; username?: string }> }
    return (body.users ?? []).find((u) => u.username === 'priya')?.id ?? ''
  }, 'http://localhost:8099')
  expect(priyaId, 'priya not found').not.toBe('')

  const dm = await startDirectChat(maya.page, priyaId)
  await openChatAndJoin(maya.page, dm)
  for (const t of [
    'ios build is green again, that was the podspec',
    'the one we thought we fixed last month',
    'that one. anyway it is fixed now',
    'nice. i will cut the release monday',
  ]) {
    await send(maya.page, t)
    await maya.page.waitForTimeout(150)
  }

  const group = await createGroup(kenji.page, 'Release 0.15', [maya.userId, priyaId])
  await openChatAndJoin(kenji.page, group)
  await openChatAndJoin(maya.page, group)
  for (const [who, t] of [
    [kenji, 'cutting 0.15 monday, shout if that is a problem'],
    [maya, 'fine by me'],
    [kenji, 'ios needs the podspec fix in first'],
    [maya, 'already merged'],
    [kenji, 'then monday it is'],
  ] as Array<[typeof maya, string]>) {
    await send(who.page, t)
    await who.page.waitForTimeout(150)
  }

  await expect
    .poll(async () => (await renderedMessages(maya.page)).length, { timeout: 20_000 })
    .toBeGreaterThan(2)
})
