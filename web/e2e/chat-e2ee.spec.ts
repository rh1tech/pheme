import { expect, test, type Page } from '@playwright/test'
import { API_URL } from './constants'
import { createUserViaAdmin, login, loginAsAdmin, uniqueEmail } from './helpers'

const PASSWORD = 'Sup3rSecret!'

// Real crypto between two real people. Chromium is enough; this is not about rendering.
test.skip(({ browserName }) => browserName !== 'chromium', 'crypto round-trip: chromium only')

/** The signed-in user's own id, taken from their access token. */
function userId(page: Page): Promise<string> {
  return page.evaluate(() => {
    const token = localStorage.getItem('pheme.accessToken') ?? ''
    const payload = JSON.parse(atob(token.split('.')[1] ?? '')) as { sub?: string }
    return payload.sub ?? ''
  })
}

/** Starts a direct chat via the API, so the test does not depend on the user picker. */
function startDirectChat(page: Page, otherId: string, apiBase: string): Promise<string> {
  return page.evaluate(
    async ([other, base]) => {
      const res = await fetch(`${base}/v1/conversations`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}`,
        },
        body: JSON.stringify({ kind: 'direct', memberIds: [other] }),
      })
      const conv = (await res.json()) as { id: string }
      return conv.id
    },
    [otherId, apiBase] as const,
  )
}

/**
 * Two people exchange an encrypted message and can both read it.
 *
 * This is the whole feature. Everything else — key packages, Welcomes, the ratchet,
 * the plaintext cache — exists to make this one thing work, and none of the other
 * tests actually check it end to end: they test the pieces. A Welcome that never
 * arrives, or a join that silently fails, leaves the recipient looking at "…" and
 * unable to reply, with every unit test still green.
 */
test('two people exchange an encrypted message and both can read it', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice')
  const bobEmail = uniqueEmail('bob')

  const setup = await browser.newContext()
  const adminPage = await setup.newPage()
  await loginAsAdmin(adminPage)
  await createUserViaAdmin(adminPage, aliceEmail, PASSWORD)
  await createUserViaAdmin(adminPage, bobEmail, PASSWORD)
  await setup.close()

  const aliceCtx = await browser.newContext()
  const bobCtx = await browser.newContext()
  const alice = await aliceCtx.newPage()
  const bob = await bobCtx.newPage()

  // Bob signs in first so his KeyPackages are published — Alice needs one to add him.
  await login(bob, bobEmail, PASSWORD)
  const bobId = await userId(bob)
  await expect
    .poll(async () => keyPackageCount(bob), { timeout: 20_000 })
    .toBeGreaterThan(0)

  await login(alice, aliceEmail, PASSWORD)

  // Alice starts the chat and sends. This provisions the MLS group and relays the
  // Welcome that lets Bob join.
  const convId = await startDirectChat(alice, bobId, API_URL)
  await alice.goto(`/chats/${convId}`)
  await send(alice, 'the eagle lands at dawn')
  await expect(alice.getByTestId('chat-message')).toContainText('the eagle lands at dawn')

  // Bob opens the chat. He must join from the Welcome and READ the message — not sit
  // looking at a placeholder.
  await bob.goto(`/chats/${convId}`)
  await expect(bob.getByTestId('chat-message')).toContainText('the eagle lands at dawn', {
    timeout: 20_000,
  })

  // And he must be able to reply — which he cannot do unless he actually joined.
  await send(bob, 'acknowledged')
  await expect(bob.getByTestId('chat-message').last()).toContainText('acknowledged')

  // Alice reads Bob's reply.
  await expect(alice.getByTestId('chat-message').last()).toContainText('acknowledged', {
    timeout: 20_000,
  })

  await aliceCtx.close()
  await bobCtx.close()
})

/**
 * The recipient already has the chat OPEN when the sender first writes.
 *
 * This is what actually happens between two people: the conversation exists, the
 * recipient is sitting in it, and the Welcome that lets them join arrives over the
 * live stream rather than being fetched with the history. If joining only works on the
 * history path, the recipient is left staring at "…" and unable to reply — with the
 * previous test still green, because there the recipient arrived afterwards.
 */
test('the recipient can read a message that arrives while they already have the chat open', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-live')
  const bobEmail = uniqueEmail('bob-live')

  const setup = await browser.newContext()
  const adminPage = await setup.newPage()
  await loginAsAdmin(adminPage)
  await createUserViaAdmin(adminPage, aliceEmail, PASSWORD)
  await createUserViaAdmin(adminPage, bobEmail, PASSWORD)
  await setup.close()

  const aliceCtx = await browser.newContext()
  const bobCtx = await browser.newContext()
  const alice = await aliceCtx.newPage()
  const bob = await bobCtx.newPage()

  await login(bob, bobEmail, PASSWORD)
  const bobId = await userId(bob)
  await expect.poll(async () => keyPackageCount(bob), { timeout: 20_000 }).toBeGreaterThan(0)

  await login(alice, aliceEmail, PASSWORD)
  const convId = await startDirectChat(alice, bobId, API_URL)

  // Bob opens the empty conversation and waits there — nothing has been sent, and no
  // group exists yet.
  await bob.goto(`/chats/${convId}`)
  await expect(bob.getByText('No messages yet. Say hello.')).toBeVisible()

  // NOW Alice arrives and writes. The Welcome and the message both reach Bob live.
  await alice.goto(`/chats/${convId}`)
  await send(alice, 'meet at the docks')

  await expect(bob.getByTestId('chat-message')).toContainText('meet at the docks', {
    timeout: 20_000,
  })

  // And Bob can reply, which means he really joined rather than merely rendering.
  await send(bob, 'on my way')
  await expect(alice.getByTestId('chat-message').last()).toContainText('on my way', {
    timeout: 20_000,
  })

  await aliceCtx.close()
  await bobCtx.close()
})

/**
 * A conversation whose recipient was locked out repairs itself.
 *
 * Some already exist: a race let the creator build two groups and send two Welcomes, so
 * the other person joined one nobody encrypts to — or none at all — and sat looking at
 * "…", unable to reply. They cannot escape it either: the creator sees a group and
 * never sends another Welcome, and a direct chat between two people is deduplicated, so
 * there is no "start again". The locked-out member now says so, and the creator builds
 * the group afresh.
 *
 * Simulated here by destroying the recipient's keys after the Welcome was addressed to
 * them — which is exactly what being locked out means: they hold a Welcome naming a
 * KeyPackage whose private half they no longer have.
 */
test('a conversation whose recipient is locked out repairs itself', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-heal')
  const bobEmail = uniqueEmail('bob-heal')

  const setup = await browser.newContext()
  const adminPage = await setup.newPage()
  await loginAsAdmin(adminPage)
  await createUserViaAdmin(adminPage, aliceEmail, PASSWORD)
  await createUserViaAdmin(adminPage, bobEmail, PASSWORD)
  await setup.close()

  const aliceCtx = await browser.newContext()
  const bobCtx = await browser.newContext()
  const alice = await aliceCtx.newPage()
  const bob = await bobCtx.newPage()

  await login(bob, bobEmail, PASSWORD)
  const bobId = await userId(bob)
  await expect.poll(async () => keyPackageCount(bob), { timeout: 20_000 }).toBeGreaterThan(0)

  await login(alice, aliceEmail, PASSWORD)
  const convId = await startDirectChat(alice, bobId, API_URL)
  await alice.goto(`/chats/${convId}`)
  await send(alice, 'can you hear me')
  await expect(alice.getByTestId('chat-message')).toContainText('can you hear me')

  // Bob's identity is destroyed before he ever opens the chat: the Welcome waiting for
  // him names a KeyPackage he no longer holds. He is locked out of his own conversation.
  await bob.evaluate(async () => {
    await new Promise<void>((resolve) => {
      const req = indexedDB.deleteDatabase('pheme')
      req.onsuccess = () => resolve()
      req.onerror = () => resolve()
      req.onblocked = () => resolve()
    })
  })
  await bob.reload()

  // He opens the chat, cannot join, and asks for the group to be rebuilt. Alice's client
  // does so. Bob joins the new group — and a message sent now gets through.
  await bob.goto(`/chats/${convId}`)
  await alice.waitForTimeout(3000) // let the rejoin reach Alice and the new Welcome land
  await send(alice, 'second attempt')

  await expect(bob.getByTestId('chat-message').last()).toContainText('second attempt', {
    timeout: 25_000,
  })
  await send(bob, 'i can hear you now')
  await expect(alice.getByTestId('chat-message').last()).toContainText('i can hear you now', {
    timeout: 20_000,
  })

  await aliceCtx.close()
  await bobCtx.close()
})

function keyPackageCount(page: Page): Promise<number> {
  return page.evaluate(async (base: string) => {
    const deviceId = localStorage.getItem('pheme.webDeviceId') ?? ''
    const res = await fetch(
      `${base}/v1/mls/key-packages/count?deviceId=${encodeURIComponent(deviceId)}`,
      { headers: { Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}` } },
    )
    if (!res.ok) return 0
    return ((await res.json()) as { count: number }).count
  }, API_URL)
}

async function send(page: Page, text: string): Promise<void> {
  const composer = page.getByTestId('composer')
  await composer.getByTestId('composer-body').fill(text)
  await composer.getByRole('button', { name: 'Send' }).click()
}

/** Sets a display name so the user is findable in the people search. */
function setDisplayName(page: Page, name: string): Promise<void> {
  return page.evaluate(
    async ([apiBase, displayName]) => {
      await fetch(`${apiBase}/v1/me`, {
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

function createGroup(page: Page, title: string, memberIds: string[]): Promise<string> {
  return page.evaluate(
    async ([apiBase, groupTitle, ids]) => {
      const res = await fetch(`${apiBase}/v1/conversations`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${localStorage.getItem('pheme.accessToken') ?? ''}`,
        },
        body: JSON.stringify({ kind: 'group', title: groupTitle, memberIds: ids }),
      })
      return ((await res.json()) as { id: string }).id
    },
    [API_URL, title, memberIds] as const,
  )
}

/**
 * An admin adds a third person to a running group, and they can read what is said
 * next; then the admin removes them, and they are cut off.
 *
 * This is the whole point of the MLS Commit relay: adding or removing a member of an
 * existing group advances everyone's epoch, and the existing members have to apply the
 * relayed Commit or they fall behind and stop being able to decrypt. It is driven
 * through the real member-management UI.
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

  const ownerCtx = await browser.newContext()
  const bobCtx = await browser.newContext()
  const carolCtx = await browser.newContext()
  const owner = await ownerCtx.newPage()
  const bob = await bobCtx.newPage()
  const carol = await carolCtx.newPage()

  // Everyone signs in so their key packages are published.
  await login(bob, bobEmail, PASSWORD)
  const bobId = await userId(bob)
  await login(carol, carolEmail, PASSWORD)
  await userId(carol) // ensure carol's session is up before publishing keys
  await setDisplayName(carol, 'Carol Findme')
  await expect.poll(() => keyPackageCount(bob), { timeout: 20_000 }).toBeGreaterThan(0)
  await expect.poll(() => keyPackageCount(carol), { timeout: 20_000 }).toBeGreaterThan(0)

  // Owner creates a group with Bob and gets it going.
  await login(owner, ownerEmail, PASSWORD)
  const groupId = await createGroup(owner, 'The Group', [bobId])
  await owner.goto(`/chats/${groupId}`)
  await send(owner, 'kickoff')
  await bob.goto(`/chats/${groupId}`)
  await expect(bob.getByTestId('chat-message')).toContainText('kickoff', { timeout: 20_000 })

  // Owner adds Carol through the member-management UI.
  await owner.getByRole('button', { name: 'Conversation menu' }).click()
  await owner.getByRole('menuitem', { name: 'Members' }).click()
  await owner.getByPlaceholder('Search people by name or @username').fill('Carol Findme')
  await owner.getByRole('button', { name: 'Add Carol Findme' }).click()
  await expect(owner.getByText('Member added')).toBeVisible({ timeout: 20_000 })
  await owner.keyboard.press('Escape')

  // Carol opens the group and can read what is said AFTER she joined. Give the add's
  // Welcome a moment to land so her first load already has it.
  await owner.waitForTimeout(1500)
  await carol.goto(`/chats/${groupId}`)
  await carol.waitForTimeout(1500)
  await send(owner, 'welcome carol')
  await expect(carol.getByTestId('chat-message').last()).toContainText('welcome carol', {
    timeout: 20_000,
  })
  // Bob, an existing member, applied the add Commit and still reads too.
  await expect(bob.getByTestId('chat-message').last()).toContainText('welcome carol', {
    timeout: 20_000,
  })

  // Owner removes Carol. Bob applies the removal Commit and can still read; Carol,
  // cut off, can no longer decrypt what is said next.
  await owner.getByRole('button', { name: 'Conversation menu' }).click()
  await owner.getByRole('menuitem', { name: 'Members' }).click()
  await owner.getByRole('button', { name: 'Member actions: Carol Findme' }).click()
  await owner.getByRole('menuitem', { name: 'Remove from group' }).click()
  await expect(owner.getByText('Member removed')).toBeVisible({ timeout: 20_000 })
  await owner.keyboard.press('Escape')

  await send(owner, 'after carol left')
  await expect(bob.getByTestId('chat-message').last()).toContainText('after carol left', {
    timeout: 20_000,
  })
  // Carol must NOT see it — give the stream a moment, then assert absence.
  await carol.waitForTimeout(3000)
  await expect(carol.getByTestId('chat-message').last()).not.toContainText('after carol left')

  await ownerCtx.close()
  await bobCtx.close()
  await carolCtx.close()
})
