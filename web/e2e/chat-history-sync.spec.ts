import { expect, test } from '@playwright/test'
import { createUserViaAdmin, loginAsAdmin, uniqueEmail } from './helpers'
import { openChatAndJoin, send, signInOnNewDevice, startDirectChat } from './chat-helpers'

const PASSWORD = 'Sup3rSecret!'

// Real crypto between real people on real devices. Chromium is enough; none of this is rendering.
test.skip(({ browserName }) => browserName !== 'chromium', 'crypto round-trip: chromium only')

// Sign-ins, an exchange, a fresh device joining and pulling history — more than 30 seconds.
test.describe.configure({ timeout: 120_000 })

/**
 * DEVICE-TO-DEVICE HISTORY SYNC. A new device gets the conversation's past from an online co-member,
 * with NO backup and NO recovery code — sealed under a key derived from the group, which the server
 * cannot read.
 *
 * The whole point: logging in on a new device "just works" — you see your history — as long as some
 * device that holds it is online, without anyone typing a recovery code.
 */
test('a new device receives pre-join history from an online co-member', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-hist')
  const bobEmail = uniqueEmail('bob-hist')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)
  const bobA = await signInOnNewDevice(browser, bobEmail, PASSWORD)

  // A real conversation with history, both directions.
  const conv = await startDirectChat(alice.page, bobA.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'the first thing said, long ago')
  await openChatAndJoin(bobA.page, conv)
  await expect(bobA.page.getByTestId('chat-message').last()).toContainText(
    'the first thing said, long ago',
    { timeout: 25_000 },
  )
  await send(bobA.page, 'and the reply, also long ago')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText(
    'and the reply, also long ago',
    { timeout: 25_000 },
  )

  // Both devices that hold the history stay online (Alice, and Bob's first device). Either can serve
  // the newcomer; the election picks one.

  // Bob signs in on a BRAND-NEW device. It starts fresh (no recovery code entered), external-joins,
  // and — holding none of the past — asks a co-member for it.
  const bobB = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  await openChatAndJoin(bobB.page, conv)

  // THE PROMISE: the pre-join history appears on the new device, pulled device-to-device. These are
  // messages bobB could NEVER MLS-decrypt (it joined after them) — so their presence proves the sync.
  await expect(
    bobB.page.getByTestId('chat-message').filter({ hasText: 'the first thing said, long ago' }),
  ).toHaveCount(1, { timeout: 40_000 })
  await expect(
    bobB.page.getByTestId('chat-message').filter({ hasText: 'and the reply, also long ago' }),
  ).toHaveCount(1, { timeout: 40_000 })

  // And it works going forward as its own independent leaf.
  await send(bobB.page, 'the new device can talk too')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText(
    'the new device can talk too',
    { timeout: 25_000 },
  )
  await send(alice.page, 'and read the new device')
  await expect(bobB.page.getByTestId('chat-message').last()).toContainText('and read the new device', {
    timeout: 25_000,
  })

  await Promise.all([alice.context.close(), bobA.context.close(), bobB.context.close()])
})

/**
 * When NO co-member is online, the new device simply shows new messages only (no error) — the case
 * Phase 3's recovery-code backup covers instead. New messages must always work regardless.
 */
test('a new device with no co-member online still works, showing new messages', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-histoff')
  const bobEmail = uniqueEmail('bob-histoff')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)
  const bobA = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const conv = await startDirectChat(alice.page, bobA.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'said before the new device')
  await openChatAndJoin(bobA.page, conv)
  await expect(bobA.page.getByTestId('chat-message').last()).toContainText(
    'said before the new device',
    { timeout: 25_000 },
  )

  // EVERY device that holds the history goes offline.
  await Promise.all([alice.context.close(), bobA.context.close()])

  // Bob's new device joins with nobody to sync from. It must still come up and work.
  const bobB = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  await openChatAndJoin(bobB.page, conv)

  // A co-member returns and sends something new — the new device reads it (own leaf).
  const alice2 = await signInOnNewDevice(browser, aliceEmail, PASSWORD)
  await openChatAndJoin(alice2.page, conv)
  await send(alice2.page, 'sent after the new device joined')
  await expect(bobB.page.getByTestId('chat-message').last()).toContainText(
    'sent after the new device joined',
    { timeout: 30_000 },
  )

  await Promise.all([bobB.context.close(), alice2.context.close()])
})
