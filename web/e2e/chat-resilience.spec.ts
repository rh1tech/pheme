import { expect, test } from './fixtures'
import { createUserViaAdmin, login, loginAsAdmin, uniqueEmail } from './helpers'
import {
  deleteKeyPackagesFor,
  groupState,
  keyPackageCount,
  keyPackageCountFor,
  openChatAndJoin,
  postJunkCommit,
  publishKeyPackagesRaw,
  resetGroup,
  send,
  signInOnNewDevice,
  startDirectChat,
  userId,
  deviceId,
} from './chat-helpers'
import type { Browser } from '@playwright/test'
import type { Device } from './chat-helpers'

const PASSWORD = 'Sup3rSecret!'

// Real crypto between real people on real devices. Chromium is enough; none of this is
// about rendering.
test.skip(({ browserName }) => browserName !== 'chromium', 'crypto round-trip: chromium only')

// Each of these walks several devices through sign-in, key publication, admission and a
// message exchange — honest work that does not fit the suite's default 30 seconds.
test.describe.configure({ timeout: 120_000 })

/**
 * The failure modes of the July 2026 production incident, as regression tests.
 *
 * A conversation between two real users burned five hundred MLS epochs in two days: a pair
 * of long-dead "zombie" KeyPackages in the directory made every client's reconcile add a
 * device that could never materialise and prune the corpse it left, forever, from both
 * sides at once. A message sent mid-storm was sealed to an epoch its reader had not applied
 * yet, and every hole in the retry path — the deduped catch-up, the missing on-open nudge,
 * the cache fallback that only ran on a thrown error — kept it reading "Not available on
 * this device" while its plaintext was one decrypt away.
 *
 * The app's one promise is that a message, once sent, is readable by the people it was
 * sent to. Each test here holds a piece of that promise under the exact conditions that
 * broke it.
 */

/** Signs in and captures the body of the device's first KeyPackage publish. */
async function signInCapturingPublish(
  browser: Browser,
  email: string,
): Promise<{ device: Device; publishBody: string }> {
  const context = await browser.newContext()
  const page = await context.newPage()
  let publishBody = ''
  await page.route('**/v1/mls/key-packages', async (route) => {
    if (route.request().method() === 'POST' && !publishBody) {
      publishBody = route.request().postData() ?? ''
    }
    await route.continue()
  })
  await login(page, email, PASSWORD)
  await expect.poll(() => keyPackageCount(page), { timeout: 20_000 }).toBeGreaterThan(0)
  expect(publishBody).not.toBe('')
  return {
    device: { context, page, userId: await userId(page), deviceId: await deviceId(page) },
    publishBody,
  }
}

/**
 * THE INCIDENT. A zombie KeyPackage — published under one device id, carrying a dead
 * device's credential inside — must not start a reconcile war, must not stop the two
 * people talking, and must end up purged from the directory by its owner's own client.
 *
 * On the old code this poisons the conversation exactly as it did in production: every
 * open adds the phantom device (whose leaf answers to the dead identity, so it stays
 * "missing" forever) and prunes the corpse, two commits at a time, from every client that
 * looks — and the epoch climbs without bound.
 */
test('a zombie key package in the directory neither wars nor blocks the conversation', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-zombie')
  const bobEmail = uniqueEmail('bob-zombie')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  // Bob signs in on a device that is about to die, and we keep a copy of the KeyPackages
  // it published. This is the raw material of the incident: packages whose credential
  // names an identity that will shortly no longer exist anywhere.
  const doomed = await signInCapturingPublish(browser, bobEmail)
  const doomedDeviceId = doomed.device.deviceId

  // Bob's real, living device.
  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)

  // The doomed device dies: its client is gone and its own packages are purged — but its
  // captured packages come BACK under a different device id. That is precisely the state
  // production was in: a directory entry whose bytes answer to a dead identity.
  const zombieId = `zombie-${Date.now()}`
  const captured = JSON.parse(doomed.publishBody) as { deviceId: string }
  captured.deviceId = zombieId
  expect(await publishKeyPackagesRaw(bob.page, captured)).toBe(204)
  expect(await deleteKeyPackagesFor(bob.page, doomedDeviceId)).toBe(204)
  await doomed.device.context.close()

  // The two people talk. This must simply work.
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)
  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'hello past the zombie')
  await openChatAndJoin(bob.page, conv)
  await expect(bob.page.getByTestId('chat-message').last()).toContainText(
    'hello past the zombie',
    { timeout: 25_000 },
  )
  await send(bob.page, 'and back at you')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText('and back at you', {
    timeout: 25_000,
  })

  // Bob's own client discovers the trap — an accepted Add whose leaf never appeared — and
  // purges the zombie's packages from the directory for everyone, last-resort included.
  await expect
    .poll(
      async () => {
        const stock = await keyPackageCountFor(bob.page, zombieId)
        return stock.count + (stock.hasLastResort ? 1 : 0)
      },
      { timeout: 30_000 },
    )
    .toBe(0)

  // No war. Re-opening the chat — which is what fed the storm, two commits per look — must
  // leave the epoch exactly where it was.
  await openChatAndJoin(alice.page, conv)
  await openChatAndJoin(bob.page, conv)
  const settled = (await groupState(bob.page, conv)).epoch
  expect(settled).toBeLessThan(12)
  await openChatAndJoin(alice.page, conv)
  await openChatAndJoin(bob.page, conv)
  expect((await groupState(bob.page, conv)).epoch).toBe(settled)

  // And nothing anywhere is sealed.
  await expect(alice.page.getByTestId('chat-sealed-divider')).toHaveCount(0)
  await expect(bob.page.getByTestId('chat-sealed-divider')).toHaveCount(0)

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * A message sealed to an epoch the reader has not applied yet.
 *
 * Bob is away while the group moves on without him (a new device is admitted — a Commit)
 * and a message is sent into the new epoch. When he comes back, his device decrypts the
 * backlog at the epoch it WOKE UP WITH, fails, and the on-open catch-up must then send the
 * parked message back for another try. Before v0.9.24 nothing did: the bubble said "Not
 * available on this device" forever, one decrypt away from the body.
 */
test('a message sealed ahead of the reader decrypts after the on-open catch-up', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-ahead')
  const bobEmail = uniqueEmail('bob-ahead')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'before you left')
  await openChatAndJoin(bob.page, conv)
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('before you left', {
    timeout: 25_000,
  })

  // Bob walks away — the tab is gone, only his keys remain on the device.
  const epochBefore = (await groupState(alice.page, conv)).epoch
  await bob.page.close()

  // The group moves on without him: Alice's second device is admitted, which is a Commit.
  const alicePhone = await signInOnNewDevice(browser, aliceEmail, PASSWORD)
  await openChatAndJoin(alicePhone.page, conv)
  await expect
    .poll(async () => (await groupState(alice.page, conv)).epoch, { timeout: 30_000 })
    .toBeGreaterThan(epochBefore)

  // And a message is sealed into the epoch Bob has never seen.
  await send(alice.page, 'sealed beyond your epoch')

  // Bob comes back. His device must catch up AND retry — the message appears, not a
  // permanent "Not available on this device".
  const returned = await bob.context.newPage()
  await openChatAndJoin(returned, conv)
  await expect(
    returned.getByTestId('chat-message').filter({ hasText: 'sealed beyond your epoch' }),
  ).toHaveCount(1, { timeout: 30_000 })
  // The history from before he left is still there too.
  await expect(
    returned.getByTestId('chat-message').filter({ hasText: 'before you left' }),
  ).toHaveCount(1)
  await expect(returned.getByTestId('chat-sealed-divider')).toHaveCount(0)

  await Promise.all([alice.context.close(), alicePhone.context.close(), bob.context.close()])
})

/**
 * Two tabs of ONE device, both watching the same chat.
 *
 * MLS lets exactly one of them decrypt an arriving message — the key is destroyed on
 * first use. The loser must read the winner's plaintext back from the shared cache
 * instead of declaring the message unavailable, which is what it did before v0.9.22.
 */
test('a message arriving into two open tabs of the same device shows in both', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-tabs')
  const bobEmail = uniqueEmail('bob-tabs')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'warm up')
  await openChatAndJoin(bob.page, conv)
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('warm up', {
    timeout: 25_000,
  })

  // The same device opens the same chat in a second tab. Same context: same keys, same
  // cache — one device, two views.
  const secondTab = await bob.context.newPage()
  await openChatAndJoin(secondTab, conv)

  await send(alice.page, 'exactly one of you decrypts this')

  // BOTH tabs show it. The loser of the decrypt race reads the cache; the 6-second grace
  // and its disk re-check are why the timeout is generous.
  for (const tab of [bob.page, secondTab]) {
    await expect(
      tab.getByTestId('chat-message').filter({ hasText: 'exactly one of you decrypts this' }),
    ).toHaveCount(1, { timeout: 25_000 })
    await expect(tab.getByTestId('chat-sealed-divider')).toHaveCount(0)
  }

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * A reload keeps every word.
 *
 * Decryption is one-shot, so after a reload nothing can be decrypted again: the transcript
 * IS the local cache. If anything leaks out of it, a message is lost for good — silently.
 */
test('every message survives a reload on both sides', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-reload')
  const bobEmail = uniqueEmail('bob-reload')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'first from alice')
  await openChatAndJoin(bob.page, conv)
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('first from alice', {
    timeout: 25_000,
  })
  await send(bob.page, 'second from bob')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText('second from bob', {
    timeout: 25_000,
  })
  await send(alice.page, 'third from alice')
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('third from alice', {
    timeout: 25_000,
  })

  // Both reload. The keys for all of this are gone — the cache is the only copy.
  for (const device of [alice, bob]) {
    await device.page.reload()
    await openChatAndJoin(device.page, conv)
    for (const said of ['first from alice', 'second from bob', 'third from alice']) {
      await expect(device.page.getByTestId('chat-message').filter({ hasText: said })).toHaveCount(
        1,
        { timeout: 25_000 },
      )
    }
    await expect(device.page.getByTestId('chat-sealed-divider')).toHaveCount(0)
  }

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * FORK SELF-HEALING. A Commit lands that no client can apply — which is exactly what a
 * forked member's history looks like to everyone else, and what the July 2026 storm left
 * behind. Every device now holds the group yet can never follow it again: the server's
 * epoch is ahead, and applying the Commits that would close the gap changes nothing.
 *
 * Nobody may need to reach for an operator. Each client must notice the wedge on its own,
 * retire the group after the grace period, establish a fresh one, and carry the
 * conversation on. Before the healer existed this state was permanent and silent — the
 * production conversation sat in it for a day, readable by no fix short of a hand reset.
 */
test('a commit nobody can apply heals itself into a fresh group', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-heal')
  const bobEmail = uniqueEmail('bob-heal')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'from before the poison')
  await openChatAndJoin(bob.page, conv)
  await expect(bob.page.getByTestId('chat-message').last()).toContainText(
    'from before the poison',
    { timeout: 25_000 },
  )

  // The poison: a Commit the server accepts and no client can ever apply. Both open tabs
  // are now wedged — behind an epoch they can never reach.
  const poisoned = await groupState(alice.page, conv)
  expect(await postJunkCommit(alice.page, conv, poisoned.groupId, poisoned.epoch)).toBe(200)

  // The healer notices, waits out its grace, retires the group and establishes a fresh
  // one — all without anyone touching anything.
  await expect
    .poll(async () => (await groupState(alice.page, conv)).groupId, {
      timeout: 90_000,
      intervals: [2_000],
    })
    .not.toBe(poisoned.groupId)

  // The conversation carries on in the fresh group, in both directions, and what was said
  // before the poison is still on both screens.
  await openChatAndJoin(alice.page, conv)
  await openChatAndJoin(bob.page, conv)
  await send(alice.page, 'healed and talking')
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('healed and talking', {
    timeout: 30_000,
  })
  await send(bob.page, 'confirmed on this side')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText(
    'confirmed on this side',
    { timeout: 25_000 },
  )
  for (const device of [alice, bob]) {
    await expect(
      device.page.getByTestId('chat-message').filter({ hasText: 'from before the poison' }),
    ).toHaveCount(1)
  }

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * The last-resort recovery: retiring the group.
 *
 * When a group is beyond saving (every holder lost its keys — or, as in production, a
 * commit storm forked a member off it), the server retires it and remembers it. The
 * clients must then establish a fresh group BY THEMSELVES, keep showing everything they
 * had already decrypted, and carry the conversation on as if nothing happened.
 */
test('the conversation survives its group being reset', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-reset')
  const bobEmail = uniqueEmail('bob-reset')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'from before the reset')
  await openChatAndJoin(bob.page, conv)
  await expect(bob.page.getByTestId('chat-message').last()).toContainText(
    'from before the reset',
    { timeout: 25_000 },
  )
  const retired = await groupState(alice.page, conv)
  expect(retired.groupId).not.toBe('')

  // The group is retired — the recovery of last resort, exactly as a stuck client (or an
  // operator repairing a fork) performs it.
  expect(await resetGroup(alice.page, conv)).toBeLessThan(300)

  // Both come back. A fresh group must come up on its own, and what was said before must
  // still be on both screens: the reset retires keys, never words.
  for (const device of [alice, bob]) {
    await device.page.reload()
    await openChatAndJoin(device.page, conv)
    await expect(
      device.page.getByTestId('chat-message').filter({ hasText: 'from before the reset' }),
    ).toHaveCount(1, { timeout: 25_000 })
  }
  await expect
    .poll(async () => (await groupState(alice.page, conv)).groupId, { timeout: 30_000 })
    .not.toBe(retired.groupId)

  // And the conversation simply continues, in both directions.
  await send(alice.page, 'we made it through')
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('we made it through', {
    timeout: 30_000,
  })
  await send(bob.page, 'good as new')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText('good as new', {
    timeout: 25_000,
  })
  await expect(alice.page.getByTestId('chat-sealed-divider')).toHaveCount(0)
  await expect(bob.page.getByTestId('chat-sealed-divider')).toHaveCount(0)

  await Promise.all([alice.context.close(), bob.context.close()])
})
