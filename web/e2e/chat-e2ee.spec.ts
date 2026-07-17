import { expect, test } from '@playwright/test'
import { createUserViaAdmin, loginAsAdmin, uniqueEmail } from './helpers'
import {
  createGroup,
  openChatAndJoin,
  send,
  setDisplayName,
  signInOnNewDevice,
  startDirectChat,
} from './chat-helpers'

const PASSWORD = 'Sup3rSecret!'

// Real crypto between two real people. Chromium is enough; this is not about rendering.
test.skip(({ browserName }) => browserName !== 'chromium', 'crypto round-trip: chromium only')

/**
 * Two people exchange an encrypted message and can both read it.
 *
 * This is the whole feature. Everything else — key packages, Welcomes, the ratchet, the
 * plaintext cache — exists to make this one thing work.
 */
test('two people exchange an encrypted message and both can read it', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice')
  const bobEmail = uniqueEmail('bob')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  // Bob signs in first so his KeyPackages are published — Alice needs one per device of
  // his to add him.
  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'the eagle lands at dawn')
  await expect(alice.page.getByTestId('chat-message')).toContainText('the eagle lands at dawn')

  // Bob opens the chat. He must join from the Welcome and READ the message — not sit
  // looking at a placeholder.
  await openChatAndJoin(bob.page, conv)
  await expect(bob.page.getByTestId('chat-message')).toContainText('the eagle lands at dawn', {
    timeout: 20_000,
  })

  // And he must be able to reply — which he cannot do unless he actually joined.
  await send(bob.page, 'acknowledged')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText('acknowledged', {
    timeout: 20_000,
  })

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * The recipient already has the chat OPEN when the sender first writes.
 *
 * This is what actually happens between two people: the conversation exists, the recipient
 * is sitting in it, and the Welcome that lets them join arrives over the live stream rather
 * than being fetched with the history. If joining only works on the history path, the
 * recipient is left unable to read or reply — with the previous test still green, because
 * there the recipient arrived afterwards.
 */
test('the recipient can read a message that arrives while they already have the chat open', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-live')
  const bobEmail = uniqueEmail('bob-live')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)
  const conv = await startDirectChat(alice.page, bob.userId)

  // Bob opens the empty conversation and waits there — nothing has been sent, and no group
  // exists yet.
  await bob.page.goto(`/chats/${conv}`)
  await expect(bob.page.getByText('No messages yet. Say hello.')).toBeVisible()

  // NOW Alice arrives and writes. The Welcome and the message both reach Bob live.
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'meet at the docks')

  await expect(bob.page.getByTestId('chat-message')).toContainText('meet at the docks', {
    timeout: 20_000,
  })

  // And Bob can reply, which means he really joined rather than merely rendering.
  await send(bob.page, 'on my way')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText('on my way', {
    timeout: 20_000,
  })

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * A device whose key material is destroyed gets back in — WITHOUT the conversation being
 * destroyed around it.
 *
 * This is the shape of the old catastrophe. A device that could not decrypt used to ask
 * the creator to tear the group down and build a new one, which threw away the key
 * material for every message anyone had ever sent — so one broken device made the whole
 * conversation unreadable for everybody. Now that device simply announces itself and is
 * ADDED to the existing group.
 *
 * Simulated by wiping the recipient's IndexedDB, which is exactly what iOS Safari's
 * storage eviction does to a real user.
 */
test('a device whose keys are destroyed rejoins without destroying the conversation', async ({
  browser,
}) => {
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
  await send(alice.page, 'can you hear me')
  await openChatAndJoin(bob.page, conv)
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('can you hear me', {
    timeout: 20_000,
  })

  // Bob's key material is destroyed under him.
  await bob.page.evaluate(async () => {
    await new Promise<void>((resolve) => {
      const req = indexedDB.deleteDatabase('pheme')
      req.onsuccess = () => resolve()
      req.onerror = () => resolve()
      req.onblocked = () => resolve()
    })
  })
  await bob.page.reload()

  // He opens the chat. His device is not in the group any more, so it announces itself,
  // and Alice — who has the chat open — admits it.
  await openChatAndJoin(bob.page, conv)

  await send(alice.page, 'second attempt')
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('second attempt', {
    timeout: 25_000,
  })
  await send(bob.page, 'i can hear you now')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText('i can hear you now', {
    timeout: 20_000,
  })

  // The crucial part: ALICE never lost anything. Under the old rebuild, repairing Bob threw
  // away the group that her history was encrypted to, and her own transcript went blank.
  await expect(alice.page.getByTestId('chat-message').first()).toContainText('can you hear me')
  await expect(alice.page.getByTestId('chat-sealed-divider')).toHaveCount(0)

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * An admin adds a third person to a running group, and they can read what is said next;
 * then the admin removes them, and they are cut off.
 *
 * This is what the MLS Commit relay is for: adding or removing a member of an existing
 * group advances everyone's epoch, and the existing members have to apply the relayed
 * Commit or they fall behind and stop being able to decrypt. Driven through the real
 * member-management UI.
 */
test('an admin adds and removes a group member, and encryption follows', async ({ browser }) => {
  const ownerEmail = uniqueEmail('owner-grp')
  const bobEmail = uniqueEmail('bob-grp')
  const carolEmail = uniqueEmail('carol-grp')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, ownerEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await createUserViaAdmin(admin, carolEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const carol = await signInOnNewDevice(browser, carolEmail, PASSWORD)
  // A name unique to THIS run. The in-memory test server never forgets, so a retry (or an earlier
  // test) that reused a fixed name would leave several "Carol Findme"s for the search to match, and
  // the "Add …" button would resolve to more than one element — a strict-mode violation, not a bug
  // in the app.
  const carolName = `Carol Findme ${Date.now()}`
  await setDisplayName(carol.page, carolName)
  const owner = await signInOnNewDevice(browser, ownerEmail, PASSWORD)

  const group = await createGroup(owner.page, `The Group ${Date.now()}`, [bob.userId])
  await openChatAndJoin(owner.page, group)
  await send(owner.page, 'kickoff')
  await openChatAndJoin(bob.page, group)
  await expect(bob.page.getByTestId('chat-message')).toContainText('kickoff', { timeout: 20_000 })

  // Owner adds Carol through the member-management UI.
  await owner.page.getByRole('button', { name: 'Conversation menu' }).click()
  await owner.page.getByRole('menuitem', { name: 'Members' }).click()
  await owner.page.getByPlaceholder('Search people by name or @username').fill(carolName)
  await owner.page.getByRole('button', { name: `Add ${carolName}` }).click()
  await expect(owner.page.getByText('Member added')).toBeVisible({ timeout: 20_000 })
  await owner.page.keyboard.press('Escape')

  // Carol opens the group and reads what is said AFTER she joined.
  await openChatAndJoin(carol.page, group)
  await send(owner.page, 'welcome carol')
  await expect(carol.page.getByTestId('chat-message').last()).toContainText('welcome carol', {
    timeout: 20_000,
  })
  // Bob, an existing member, applied the add Commit and still reads too.
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('welcome carol', {
    timeout: 20_000,
  })

  // Owner removes Carol. Bob applies the removal Commit and can still read; Carol, cut
  // off, can no longer decrypt what is said next.
  await owner.page.getByRole('button', { name: 'Conversation menu' }).click()
  await owner.page.getByRole('menuitem', { name: 'Members' }).click()
  await owner.page.getByRole('button', { name: 'Member actions: Carol Findme' }).click()
  await owner.page.getByRole('menuitem', { name: 'Remove from group' }).click()
  await expect(owner.page.getByText('Member removed')).toBeVisible({ timeout: 20_000 })
  await owner.page.keyboard.press('Escape')

  await send(owner.page, 'after carol left')
  await expect(bob.page.getByTestId('chat-message').last()).toContainText('after carol left', {
    timeout: 20_000,
  })
  // Carol must NOT see it — give the stream a moment, then assert absence.
  await carol.page.waitForTimeout(3000)
  await expect(carol.page.getByTestId('chat-message').last()).not.toContainText('after carol left')

  await Promise.all([owner.context.close(), bob.context.close(), carol.context.close()])
})
