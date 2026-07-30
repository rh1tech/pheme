import { expect, test } from './fixtures'
import { createUserViaAdmin, loginAsAdmin, uniqueEmail } from './helpers'
import {
  createGroup,
  deviceId,
  groupState,
  keyPackageCount,
  openChatAndJoin,
  send,
  setDisplayName,
  signInOnNewDevice,
  startDirectChat,
} from './chat-helpers'

const PASSWORD = 'Sup3rSecret!'

// Real crypto between real people on real devices. Chromium is enough; none of this is
// about rendering.
test.skip(({ browserName }) => browserName !== 'chromium', 'crypto round-trip: chromium only')

// The same budget every other multi-device MLS file in this suite sets, and for the same reason:
// each of these walks several devices through sign-in, key publication, admission and a message
// exchange, which does not fit the suite's default 30 seconds.
//
// This file was the only one of them missing it, while being the heaviest — ten tests, four browser
// contexts apiece. "removing a member cuts off every device they have" spends 24 of its 30 seconds
// on two deliberate waits alone (a 20s assertion for a removal to propagate, and 4s to establish
// that something does NOT appear), leaving six for three admin-created accounts and four logins.
// On an unloaded laptop that just fits; on a shared CI runner it does not, and it failed there as
// "Test timeout of 30000ms exceeded" — the harness cutting the test off mid-way and reporting it as
// a product failure.
test.describe.configure({ timeout: 120_000 })

/**
 * The bug, exactly as it was reported.
 *
 * Two people talk. One of them is signed in on TWO devices — a phone and a desktop
 * browser — which is not an edge case, it is how people use a chat app. Every message
 * must be readable on every device of every participant.
 *
 * It was not. An MLS leaf is a device, but the group was built from one KeyPackage per
 * USER, so exactly one of her two devices was in the group and the other showed a
 * conversation of blanks. This test fails on the old code at the first assertion against
 * her second device.
 */
test('a user signed in on two devices reads and writes from both', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-md')
  const juliettEmail = uniqueEmail('juliett-md')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, juliettEmail, PASSWORD)
  await setup.close()

  // Juliett is signed in on two devices BEFORE the conversation starts, so both should be
  // in the group from the moment it is built.
  const phone = await signInOnNewDevice(browser, juliettEmail, PASSWORD)
  const desktop = await signInOnNewDevice(browser, juliettEmail, PASSWORD)
  expect(phone.deviceId).not.toBe(desktop.deviceId) // two devices, not two tabs

  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)
  const conv = await startDirectChat(alice.page, phone.userId)

  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'can you both see this')

  // BOTH of Juliett's devices read it. This is the assertion the old code could not pass.
  await openChatAndJoin(phone.page, conv)
  await expect(phone.page.getByTestId('chat-message').last()).toContainText(
    'can you both see this',
    { timeout: 20_000 },
  )
  await openChatAndJoin(desktop.page, conv)
  await expect(desktop.page.getByTestId('chat-message').last()).toContainText(
    'can you both see this',
    { timeout: 20_000 },
  )

  // Juliett replies from her phone. Alice reads it — and so does Juliett's OWN desktop,
  // because a different device of the same person is a different leaf, not the sender.
  await send(phone.page, 'sent from my phone')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText('sent from my phone', {
    timeout: 20_000,
  })
  await expect(desktop.page.getByTestId('chat-message').last()).toContainText(
    'sent from my phone',
    { timeout: 20_000 },
  )

  // And from her desktop, read on both of the others.
  await send(desktop.page, 'and this from the browser')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText(
    'and this from the browser',
    { timeout: 20_000 },
  )
  await expect(phone.page.getByTestId('chat-message').last()).toContainText(
    'and this from the browser',
    { timeout: 20_000 },
  )

  // Nothing anywhere is sealed: every device could read every message.
  for (const d of [alice, phone, desktop]) {
    await expect(d.page.getByTestId('chat-sealed-divider')).toHaveCount(0)
  }

  await Promise.all([alice.context.close(), phone.context.close(), desktop.context.close()])
})

/**
 * The other half of the reported bug: the SENDER signs in on a second device.
 *
 * The old code gated group creation on "did I create this conversation?" — a check on the
 * USER. So the sender's second device also believed it was the creator, found no group
 * locally, and built a SECOND group under the same conversation. From then on his two
 * devices encrypted into two different groups, and the person he was talking to could read
 * one of them and saw blanks for the other. That is precisely "I see Julia's messages but
 * not mine".
 *
 * Now the group id is minted once, server-side, under a compare-and-set: a second device
 * cannot create a second group, it can only be admitted to the existing one.
 */
test('the conversation creator signing in on a second device does not fork the group', async ({
  browser,
}) => {
  const mikhailEmail = uniqueEmail('mikhail-fork')
  const juliettEmail = uniqueEmail('juliett-fork')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, mikhailEmail, PASSWORD)
  await createUserViaAdmin(admin, juliettEmail, PASSWORD)
  await setup.close()

  const juliett = await signInOnNewDevice(browser, juliettEmail, PASSWORD)
  const laptop = await signInOnNewDevice(browser, mikhailEmail, PASSWORD)

  // Mikhail starts the chat on his laptop and says something. The group is established.
  const conv = await startDirectChat(laptop.page, juliett.userId)
  await openChatAndJoin(laptop.page, conv)
  await send(laptop.page, 'from the laptop')
  await openChatAndJoin(juliett.page, conv)
  await expect(juliett.page.getByTestId('chat-message').last()).toContainText('from the laptop', {
    timeout: 20_000,
  })

  const established = await groupState(laptop.page, conv)
  expect(established.groupId).not.toBe('')

  // NOW Mikhail signs in on his phone and opens the same chat. The old code built a second
  // group here.
  const mobile = await signInOnNewDevice(browser, mikhailEmail, PASSWORD)
  await openChatAndJoin(mobile.page, conv)

  // The conversation's group is still the SAME one. Nothing was replaced, so nothing that
  // was already said was destroyed.
  const after = await groupState(mobile.page, conv)
  expect(after.groupId).toBe(established.groupId)

  // Mikhail writes from his phone. Juliett reads it — and she must ALSO still be able to
  // read what came next from the laptop. A fork would break one of these two.
  await send(mobile.page, 'now from the phone')
  await expect(juliett.page.getByTestId('chat-message').last()).toContainText(
    'now from the phone',
    { timeout: 20_000 },
  )

  await send(laptop.page, 'and the laptop still works')
  await expect(juliett.page.getByTestId('chat-message').last()).toContainText(
    'and the laptop still works',
    { timeout: 20_000 },
  )
  await expect(mobile.page.getByTestId('chat-message').last()).toContainText(
    'and the laptop still works',
    { timeout: 20_000 },
  )

  // Juliett replies, and both of Mikhail's devices read it.
  await send(juliett.page, 'i can read both of you')
  await expect(laptop.page.getByTestId('chat-message').last()).toContainText(
    'i can read both of you',
    { timeout: 20_000 },
  )
  await expect(mobile.page.getByTestId('chat-message').last()).toContainText(
    'i can read both of you',
    { timeout: 20_000 },
  )

  await Promise.all([juliett.context.close(), laptop.context.close(), mobile.context.close()])
})

/**
 * A device added to a conversation that is already running gets let in, reads new messages, AND —
 * because another device of the same account is online — receives the pre-join history
 * device-to-device. MLS itself gives a new member no access to the past.
 */
test('a device that joins late reads new messages and syncs the old ones from a co-member', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-late')
  const bobEmail = uniqueEmail('bob-late')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bobPhone = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bobPhone.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'said before the laptop existed')
  await openChatAndJoin(bobPhone.page, conv)
  await expect(bobPhone.page.getByTestId('chat-message').last()).toContainText(
    'said before the laptop existed',
    { timeout: 20_000 },
  )

  // Bob signs in on a laptop, long after. It announces itself and a member admits it.
  const bobLaptop = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  await openChatAndJoin(bobLaptop.page, conv)

  // Anything said from now on is readable on the laptop.
  await send(alice.page, 'and this is after')
  await expect(bobLaptop.page.getByTestId('chat-message').last()).toContainText(
    'and this is after',
    { timeout: 20_000 },
  )
  await send(bobLaptop.page, 'the laptop can talk too')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText(
    'the laptop can talk too',
    { timeout: 20_000 },
  )

  // The message that predates the laptop — which it could never MLS-decrypt — is SYNCED to it from
  // Bob's same-account phone. Alice is not eligible to vouch for Bob's historical plaintext.
  await expect(
    bobLaptop.page.getByTestId('chat-message').filter({ hasText: 'said before the laptop existed' }),
  ).toHaveCount(1, { timeout: 40_000 })
  await expect(bobLaptop.page.getByTestId('chat-sealed-divider')).toHaveCount(0)

  // And crucially, the devices that WERE there keep every word. The old code's rebuild is
  // what took this away from everybody.
  await expect(bobPhone.page.getByTestId('chat-message').first()).toContainText(
    'said before the laptop existed',
  )

  await Promise.all([alice.context.close(), bobPhone.context.close(), bobLaptop.context.close()])
})

/**
 * Removing someone from a group removes EVERY device they have.
 *
 * With one leaf per user, removing a two-device member took out whichever leaf the group
 * happened to find first — and the person you just threw out carried on reading the
 * conversation from their other device. That is a confidentiality failure, and it is
 * invisible unless a test actually keeps the second device open and looks.
 */
test('removing a member cuts off every device they have', async ({ browser }) => {
  const ownerEmail = uniqueEmail('owner-rm')
  const bobEmail = uniqueEmail('bob-rm')
  const carolEmail = uniqueEmail('carol-rm')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, ownerEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await createUserViaAdmin(admin, carolEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  // Carol is on two devices. Both must lose access when she is removed.
  const carolPhone = await signInOnNewDevice(browser, carolEmail, PASSWORD)
  const carolTablet = await signInOnNewDevice(browser, carolEmail, PASSWORD)
  await setDisplayName(carolPhone.page, 'Carol Twodevice')

  const owner = await signInOnNewDevice(browser, ownerEmail, PASSWORD)
  const group = await createGroup(owner.page, `Squad ${Date.now()}`, [bob.userId, carolPhone.userId])

  await openChatAndJoin(owner.page, group)
  await send(owner.page, 'kickoff')
  await openChatAndJoin(bob.page, group)
  await openChatAndJoin(carolPhone.page, group)
  await openChatAndJoin(carolTablet.page, group)

  // Both of Carol's devices are in, and both can read.
  for (const page of [bob.page, carolPhone.page, carolTablet.page]) {
    await expect(page.getByTestId('chat-message').last()).toContainText('kickoff', {
      timeout: 20_000,
    })
  }

  // The owner removes Carol through the real member-management UI.
  await owner.page.getByRole('button', { name: 'Conversation menu' }).click()
  await owner.page.getByRole('menuitem', { name: 'Members' }).click()
  await owner.page.getByRole('button', { name: 'Member actions: Carol Twodevice' }).click()
  await owner.page.getByRole('menuitem', { name: 'Remove from group' }).click()
  await expect(owner.page.getByText('Member removed')).toBeVisible({ timeout: 20_000 })
  await owner.page.keyboard.press('Escape')

  await send(owner.page, 'carol is gone now')

  // Bob, who stayed, applied the removal Commit and still reads.
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('carol is gone now', {
    timeout: 20_000,
  })

  // NEITHER of Carol's devices can read it. The tablet is the one the old code forgot.
  //
  // Asserted by COUNT on both devices. Removal takes the conversation off Carol's list entirely,
  // so `.last()` matched no element and threw "element(s) not found" instead of passing — this
  // test failed precisely when the confidentiality it guards was most thoroughly enforced. And
  // counting is the stronger claim anyway: `.last()` inspected only the newest message, so the
  // text leaking anywhere above it would have gone unnoticed.
  await carolPhone.page.waitForTimeout(4000)
  // Both of Carol's clients are alive and rendering — otherwise "no such message" would be
  // satisfied by a blank page, and the absence below would prove nothing.
  await expect(carolPhone.page.getByTestId('chat-sidebar')).toBeVisible()
  await expect(carolTablet.page.getByTestId('chat-sidebar')).toBeVisible()
  await expect(
    carolPhone.page.getByTestId('chat-message').filter({ hasText: 'carol is gone now' }),
  ).toHaveCount(0)
  await expect(
    carolTablet.page.getByTestId('chat-message').filter({ hasText: 'carol is gone now' }),
  ).toHaveCount(0)

  await Promise.all([
    owner.context.close(),
    bob.context.close(),
    carolPhone.context.close(),
    carolTablet.context.close(),
  ])
})

/**
 * THE UPGRADE PATH. Every existing user is in this state the moment this ships.
 *
 * Their stored MLS identity was minted before identities carried a device id, so its
 * credential is the bare user id — it names a person, not a device. Left alone, the new
 * code reads no device half out of it, publishes that device's KeyPackages under an empty
 * device id, and the user quietly becomes unreachable: no leaf anyone can add, no
 * conversation that works, no error either.
 *
 * So a legacy identity is discarded and a proper device identity minted in its place. The
 * old groups were already unreadable — that is the bug being fixed — but the app has to
 * come back working, and it has to come back working without anyone doing anything.
 *
 * Simulated by rewriting the credential in the stored state to the pre-device form, which
 * is exactly what is sitting in real users' browsers right now.
 */
test('a user whose stored identity predates device ids recovers on upgrade', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-legacy')
  const bobEmail = uniqueEmail('bob-legacy')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  // Bob's stored identity is rewritten to the old shape: credential = the user id alone,
  // with no device half. This is what every already-signed-in user has today.
  await bob.page.evaluate(async (userId: string) => {
    const db: IDBDatabase = await new Promise((resolve, reject) => {
      const req = indexedDB.open('pheme', 1)
      req.onsuccess = () => resolve(req.result)
      req.onerror = () => reject(req.error)
    })
    const read = (key: string): Promise<Uint8Array | undefined> =>
      new Promise((resolve, reject) => {
        const r = db.transaction('mls', 'readonly').objectStore('mls').get(key)
        r.onsuccess = () => resolve(r.result as Uint8Array | undefined)
        r.onerror = () => reject(r.error)
      })
    const state = await read('client-state')
    if (!state) throw new Error('no client state to downgrade')

    const envelope = JSON.parse(new TextDecoder().decode(state)) as { identity: number[] }
    envelope.identity = Array.from(new TextEncoder().encode(userId)) // the bare user id
    const rewritten = new TextEncoder().encode(JSON.stringify(envelope))

    await new Promise<void>((resolve, reject) => {
      const r = db.transaction('mls', 'readwrite').objectStore('mls').put(rewritten, 'client-state')
      r.onsuccess = () => resolve()
      r.onerror = () => reject(r.error)
    })
  }, bob.userId)

  // He reloads into the new code — which is exactly what a deploy does to him.
  await bob.page.reload()
  await expect(bob.page.getByTestId('chat-sidebar')).toBeVisible()

  // He must come back with a REAL device identity and publish keys under it, or nobody can
  // ever add him to anything again.
  await expect.poll(() => keyPackageCount(bob.page), { timeout: 25_000 }).toBeGreaterThan(0)
  const recoveredDevice = await deviceId(bob.page)
  expect(recoveredDevice).not.toBe('')
  expect(recoveredDevice).not.toBe(bob.deviceId) // a new device, not the dead one's name

  // And the conversation works, in both directions.
  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'after the upgrade')
  await openChatAndJoin(bob.page, conv)
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('after the upgrade', {
    timeout: 25_000,
  })
  await send(bob.page, 'and i can reply')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText('and i can reply', {
    timeout: 20_000,
  })

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * The group gets established even if the person who CREATED the conversation never opens it.
 *
 * Reserving that job for the creator looked tidy — it stops a loser burning the KeyPackages it
 * claimed — but it is a deadlock. The creator may not come back for a week, and until they do,
 * everybody else sits at "setting up encryption", unable to read, unable to send, and with
 * nothing to tell them why. A wasted KeyPackage is a rounding error next to a conversation that
 * never works. The server's compare-and-set already makes the race safe.
 */
test('a member who did not create the conversation can still establish its group', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-est')
  const bobEmail = uniqueEmail('bob-est')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  // Alice creates the conversation — and never opens it.
  const conv = await startDirectChat(alice.page, bob.userId)
  expect((await groupState(alice.page, conv)).groupId).toBe('')

  // BOB opens it. He did not create it, and the group must still come up.
  await openChatAndJoin(bob.page, conv)
  const established = await groupState(bob.page, conv)
  expect(established.groupId).not.toBe('')

  await send(bob.page, 'i set this up myself')

  // And Alice, arriving afterwards, joins the group Bob built and reads it.
  await openChatAndJoin(alice.page, conv)
  await expect(alice.page.getByTestId('chat-message').last()).toContainText('i set this up myself', {
    timeout: 25_000,
  })
  await send(alice.page, 'so you did')
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('so you did', {
    timeout: 20_000,
  })

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * A new device is let in even though NOBODY has that conversation open.
 *
 * A device cannot add itself: only a member who already holds the group can Commit. So it
 * announces itself and waits for one of them to notice — and the question that matters is who
 * is listening. It used to be "only somebody with that exact chat open on screen", which is a
 * deadlock dressed up as a race: two people rarely have the same conversation open at the same
 * moment, and the device that announced just sat there telling its owner that encryption was
 * still being set up.
 *
 * Here Alice is signed in and looking at her chat list. That has to be enough.
 */
test('a new device is admitted while the other member is not even in the chat', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-admit')
  const bobEmail = uniqueEmail('bob-admit')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bobPhone = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bobPhone.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'first')
  await openChatAndJoin(bobPhone.page, conv)

  // Alice walks away from the conversation. She is still signed in, but she is looking at the
  // list — which is where people actually are most of the time.
  await alice.page.goto('/')
  await expect(alice.page.getByTestId('chat-sidebar')).toBeVisible()

  // Bob signs in on a laptop and opens the chat. Nobody is watching this conversation, and it
  // still has to let him in.
  const bobLaptop = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  await openChatAndJoin(bobLaptop.page, conv)

  await send(bobLaptop.page, 'let me in')
  await expect(alice.page.getByTestId('chat-sidebar')).toBeVisible()
  await alice.page.goto(`/chats/${conv}`)
  await expect(alice.page.getByTestId('chat-message').last()).toContainText('let me in', {
    timeout: 25_000,
  })

  await Promise.all([alice.context.close(), bobPhone.context.close(), bobLaptop.context.close()])
})

/**
 * EXTERNAL JOIN — a new device joins with literally NO ONE online to admit it.
 *
 * The previous test still needed a member (Alice) signed in somewhere to react to the announce.
 * This one closes every other device: Alice establishes the group and publishes GroupInfo, then
 * goes away entirely. Bob's new laptop must still join — by external commit against the published
 * GroupInfo — read what Alice said next when she returns, and never sit at "setting up encryption"
 * or trigger a group reset. This is what makes "log in on the web at any time" instant.
 */
test('a new device external-joins with no one online to admit it', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-ext')
  const bobEmail = uniqueEmail('bob-ext')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bobPhone = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  // Alice establishes the group and, in settling, publishes GroupInfo for future joiners.
  const conv = await startDirectChat(alice.page, bobPhone.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'before the laptop')
  await openChatAndJoin(bobPhone.page, conv)
  await expect(bobPhone.page.getByTestId('chat-message').last()).toContainText('before the laptop', {
    timeout: 25_000,
  })

  // EVERY other device goes away — Alice and Bob's phone both close. Nobody is online to admit.
  await Promise.all([alice.context.close(), bobPhone.context.close()])

  // Bob's brand-new laptop opens the chat. With no admitter anywhere, it must external-join off
  // the published GroupInfo — openChatAndJoin waits for data-joined=true, which only happens once
  // it actually holds the group.
  const bobLaptop = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  await openChatAndJoin(bobLaptop.page, conv)
  const joined = await groupState(bobLaptop.page, conv)
  expect(joined.groupId).not.toBe('')

  // It can send as a real member; the group id never changed (no reset happened).
  await send(bobLaptop.page, 'joined with nobody home')
  await expect(bobLaptop.page.getByTestId('chat-message').last()).toContainText(
    'joined with nobody home',
    { timeout: 25_000 },
  )

  // Alice comes back on a fresh device — it too external-joins with only the laptop around — and
  // the two exchange NEW messages BOTH ways, proving the laptop joined the SAME live group (a
  // reset would have forked them onto groups the other could not read). Alice2 cannot read the
  // laptop's earlier line, and shouldn't: it is a fresh leaf that joined after it (correctly
  // hidden), which is why this asserts a fresh round-trip rather than old history.
  const alice2 = await signInOnNewDevice(browser, aliceEmail, PASSWORD)
  await openChatAndJoin(alice2.page, conv)
  expect((await groupState(alice2.page, conv)).groupId).toBe(joined.groupId)

  await send(alice2.page, 'and alice is back')
  await expect(bobLaptop.page.getByTestId('chat-message').last()).toContainText('and alice is back', {
    timeout: 25_000,
  })
  await send(bobLaptop.page, 'laptop reads alice too')
  await expect(alice2.page.getByTestId('chat-message').last()).toContainText(
    'laptop reads alice too',
    { timeout: 25_000 },
  )

  await Promise.all([bobLaptop.context.close(), alice2.context.close()])
})

/**
 * Leaving a group.
 *
 * MLS forbids committing your own removal (CannotRemoveSelf), so leaving cannot be a
 * Commit: the member drops their server-side membership and destroys their local group
 * state, and the members who stay prune the leaf they left behind when they next
 * reconcile. Both halves have to actually happen — the leaver must lose access, and the
 * group must keep working — so both are asserted here.
 */
test('a member who leaves a group loses access, and the group carries on', async ({
  browser,
}) => {
  const ownerEmail = uniqueEmail('owner-leave')
  const bobEmail = uniqueEmail('bob-leave')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, ownerEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const owner = await signInOnNewDevice(browser, ownerEmail, PASSWORD)

  const group = await createGroup(owner.page, `Leavers ${Date.now()}`, [bob.userId])
  await openChatAndJoin(owner.page, group)
  await send(owner.page, 'before bob left')
  await openChatAndJoin(bob.page, group)
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('before bob left', {
    timeout: 20_000,
  })

  // Bob leaves through the real menu.
  await bob.page.getByRole('button', { name: 'Conversation menu' }).click()
  await bob.page.getByRole('menuitem', { name: 'Leave group' }).click()
  await bob.page.getByRole('dialog').getByRole('button', { name: 'Leave group' }).click()
  await expect(bob.page).toHaveURL(/\/$/, { timeout: 20_000 })

  // The group still works for the owner — leaving must not take the conversation with it.
  await send(owner.page, 'after bob left')
  await expect(owner.page.getByTestId('chat-message').last()).toContainText('after bob left')
  await expect(owner.page.getByTestId('chat-sealed-divider')).toHaveCount(0)

  // And Bob cannot read it: he is not a member any more, so the conversation is gone from
  // his side entirely.
  await bob.page.goto(`/chats/${group}`)
  await bob.page.waitForTimeout(3000)
  await expect(bob.page.getByTestId('chat-message').filter({ hasText: 'after bob left' })).toHaveCount(0)

  await Promise.all([owner.context.close(), bob.context.close()])
})

/**
 * Three people in a group, one of them on two devices — the exact shape of the reported
 * failure, where a third person could read one participant's messages and saw blanks for
 * the other's.
 */
test('in a group, everyone reads messages from every device of every member', async ({
  browser,
}) => {
  const mikhailEmail = uniqueEmail('mikhail-grp')
  const juliettEmail = uniqueEmail('juliett-grp')
  const thirdEmail = uniqueEmail('third-grp')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, mikhailEmail, PASSWORD)
  await createUserViaAdmin(admin, juliettEmail, PASSWORD)
  await createUserViaAdmin(admin, thirdEmail, PASSWORD)
  await setup.close()

  const juliettPhone = await signInOnNewDevice(browser, juliettEmail, PASSWORD)
  const juliettDesktop = await signInOnNewDevice(browser, juliettEmail, PASSWORD)
  const third = await signInOnNewDevice(browser, thirdEmail, PASSWORD)
  const mikhail = await signInOnNewDevice(browser, mikhailEmail, PASSWORD)

  const group = await createGroup(mikhail.page, `Team ${Date.now()}`, [
    juliettPhone.userId,
    third.userId,
  ])

  const everyone = [mikhail, juliettPhone, juliettDesktop, third]
  for (const d of everyone) await openChatAndJoin(d.page, group)

  // Each participant speaks — including both of Juliett's devices. Each one waits for its
  // own message to land before the next speaks: four browsers firing inside the same
  // millisecond is a rendering race in the test, not a property of the encryption, and
  // letting it in would make this flaky without testing anything more.
  for (const [device, said] of [
    [mikhail, 'mikhail here'],
    [juliettPhone, 'juliett on her phone'],
    [juliettDesktop, 'juliett on her desktop'],
    [third, 'and the third person'],
  ] as const) {
    await send(device.page, said)
    await expect(device.page.getByTestId('chat-message').last()).toContainText(said, {
      timeout: 20_000,
    })
  }

  // Everyone reads everything. The reported failure was the third person seeing one
  // participant's messages and blanks for the other's.
  for (const d of everyone) {
    for (const said of [
      'mikhail here',
      'juliett on her phone',
      'juliett on her desktop',
      'and the third person',
    ]) {
      await expect(d.page.getByTestId('chat-message').filter({ hasText: said })).toHaveCount(1, {
        timeout: 25_000,
      })
    }
    await expect(d.page.getByTestId('chat-sealed-divider')).toHaveCount(0)
  }

  await Promise.all(everyone.map((d) => d.context.close()))
})
