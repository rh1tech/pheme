import { expect, test } from './fixtures'
import { createUserViaAdmin, loginAsAdmin, uniqueEmail } from './helpers'
import { openChatAndJoin, send, signInOnNewDevice, startDirectChat } from './chat-helpers'

const PASSWORD = 'Sup3rSecret!'

// Real crypto between real people on real devices. Chromium is enough; none of this is rendering.
test.skip(({ browserName }) => browserName !== 'chromium', 'crypto round-trip: chromium only')

// Sign-ins, an exchange, a fresh device joining and pulling history — more than 30 seconds.
test.describe.configure({ timeout: 120_000 })

/**
 * DEVICE-TO-DEVICE HISTORY SYNC. A new device gets the conversation's past from an online device of
 * the same account, with NO backup and NO recovery code — sealed under a group-derived key.
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

  // Bob's first device stays online and is the only eligible provider. Alice is a group member but
  // cannot vouch for Bob's imported plaintext merely because she owns a valid leaf key.

  // Bob signs in on a BRAND-NEW device. It starts fresh (no recovery code entered), external-joins,
  // and — holding none of the past — asks its other account device for it.
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
 * THE SAME PERSON'S OTHER DEVICE ANSWERS — with the other participant gone.
 *
 * The test above leaves Alice online, so the newcomer could be served by the OTHER user. That hid a
 * real bug for a whole release: the responder stood down on any request carrying its own user id,
 * reasoning that "a co-member of another user answers". Apart from being unreliable, another
 * participant cannot be trusted to supply historical plaintext: a valid leaf authenticates that
 * participant, not the message bodies they claim.
 *
 * So: Alice is closed before the new device ever appears. The only device left holding the history
 * belongs to Bob himself. It must be the one that hands it over — that is the commonest case there
 * is ("I added my laptop"), not an edge case.
 */
test("a new device gets its history from the same person's other device, nobody else online", async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-selfhist')
  const bobEmail = uniqueEmail('bob-selfhist')

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
  await send(alice.page, 'said before the laptop existed')
  await openChatAndJoin(bobA.page, conv)
  await expect(bobA.page.getByTestId('chat-message').last()).toContainText(
    'said before the laptop existed',
    { timeout: 25_000 },
  )
  await send(bobA.page, 'and answered before it existed')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText(
    'and answered before it existed',
    { timeout: 25_000 },
  )

  // THE OTHER PARTICIPANT LEAVES. Only Bob's own first device still holds this conversation's past.
  await alice.context.close()

  // Bob signs in on a new device, fresh — no recovery code, no backup restored.
  const bobB = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  await openChatAndJoin(bobB.page, conv)

  // Bob's OWN other device must serve him. Neither message is MLS-decryptable by bobB, which joined
  // after both were sealed, so their presence can only mean the handoff happened.
  await expect(
    bobB.page.getByTestId('chat-message').filter({ hasText: 'said before the laptop existed' }),
  ).toHaveCount(1, { timeout: 40_000 })
  await expect(
    bobB.page.getByTestId('chat-message').filter({ hasText: 'and answered before it existed' }),
  ).toHaveCount(1, { timeout: 40_000 })

  await Promise.all([bobA.context.close(), bobB.context.close()])
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
