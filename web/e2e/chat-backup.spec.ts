import { expect, test } from '@playwright/test'
import { createUserViaAdmin, login, loginAsAdmin, uniqueEmail } from './helpers'
import { openChatAndJoin, send, signInOnNewDevice, startDirectChat } from './chat-helpers'

const PASSWORD = 'Sup3rSecret!'
const PASSPHRASE = 'correct horse battery pheme'

// A one-pixel PNG — enough to ride the whole photo path: sealed on the client, uploaded as
// an opaque blob, and on the far side re-fetched and opened with the key from the message.
const TEST_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAgAAAAICAIAAABLbSncAAAAHElEQVR4nGJhYKhQYGDARCwgAhsYnBKAAAAA//9knwJZeWr4nQAAAABJRU5ErkJggg==',
  'base64',
)

// Real crypto between real people on real devices. Chromium is enough; none of this is
// about rendering.
test.skip(({ browserName }) => browserName !== 'chromium', 'crypto round-trip: chromium only')

// Sign-ins, a full exchange, a backup and a restore with a reload — more than the suite's
// default 30 seconds of honest work.
test.describe.configure({ timeout: 120_000 })

/**
 * The recovery passphrase's whole promise, walked end to end.
 *
 * Decryption is one-shot: everything a device has read exists nowhere but its local cache.
 * So "back up your chats" must mean the WORDS, not just the keys — a user who loses their
 * phone and types their passphrase into a new one expects their conversations back, not an
 * empty timeline that can merely send. The backup carries the transcript cache, sealed
 * under the same passphrase the keys are; the server stores only ciphertext it cannot
 * open. This test is that promise: set a passphrase, lose the device, restore on a fresh
 * one, and the history is simply there.
 */
test('a recovery passphrase carries the whole transcript to a new device', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-backup')
  const bobEmail = uniqueEmail('bob-backup')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const doomed = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  // A real conversation happens — both directions, so the transcript holds messages Bob
  // decrypted AND messages he wrote (which he could never decrypt again himself).
  const conv = await startDirectChat(alice.page, doomed.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'first, from alice, before the backup')
  await openChatAndJoin(doomed.page, conv)
  await expect(doomed.page.getByTestId('chat-message').last()).toContainText(
    'first, from alice, before the backup',
    { timeout: 25_000 },
  )
  await send(doomed.page, 'second, from bob, also before')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText(
    'second, from bob, also before',
    { timeout: 25_000 },
  )

  // A photo, too. The encrypted blob lives on the server; its key travels inside the
  // message — so what the transcript backup must carry for a photo to survive is that key,
  // not the pixels. Alice sends it; Bob's device reads it (and caches the decrypted body,
  // key and all) before the backup is taken.
  const aliceComposer = alice.page.getByTestId('composer')
  await aliceComposer.locator('input[type="file"]').setInputFiles({
    name: 'before.png',
    mimeType: 'image/png',
    buffer: TEST_PNG,
  })
  // The picked photo is sealed and shows as a preview thumbnail before it can be sent.
  await expect(aliceComposer.locator('.pheme-attachment img')).toBeVisible({ timeout: 15_000 })
  await aliceComposer.getByRole('button', { name: 'Send' }).click()
  // Bob sees the picture decrypt and render (a real object URL, not a broken image).
  await expect(doomed.page.locator('.pheme-photo img').last()).toBeVisible({ timeout: 25_000 })

  // Bob sets his recovery passphrase — through the real menu, like a person.
  await doomed.page.goto('/')
  await doomed.page.getByRole('button', { name: 'Menu' }).click()
  await doomed.page.getByRole('menuitem', { name: 'Chat backup' }).click()
  await doomed.page.getByLabel('Recovery passphrase', { exact: true }).fill(PASSPHRASE)
  await doomed.page.getByLabel('Confirm passphrase').fill(PASSPHRASE)
  await doomed.page.getByRole('button', { name: 'Back up now' }).click()
  await expect(doomed.page.getByText('Chats backed up')).toBeVisible({ timeout: 20_000 })

  // The phone falls in a lake.
  await doomed.context.close()

  // A brand-new device signs in. It has no keys — and the restore prompt must offer the
  // backup rather than quietly minting a fresh identity.
  const restoredContext = await browser.newContext()
  const restored = await restoredContext.newPage()
  await login(restored, bobEmail, PASSWORD)
  await expect(restored.getByText('Restore your chats')).toBeVisible({ timeout: 20_000 })
  await restored.getByLabel('Recovery passphrase', { exact: true }).fill(PASSPHRASE)
  await restored.getByRole('button', { name: 'Restore', exact: true }).click()

  // The restore reloads the app into the recovered identity. Wait for the prompt itself to
  // go — it survives until the reload lands and the now-present keys keep the gate shut —
  // rather than for the sidebar, which is visible BEHIND the modal and would let the test
  // race ahead into a navigation the reload then interrupts.
  await expect(restored.getByText('Restore your chats')).toBeHidden({ timeout: 30_000 })
  await expect(restored.getByTestId('chat-sidebar')).toBeVisible({ timeout: 30_000 })

  // THE PROMISE: the history is there. Not sealed, not blank — the words.
  await openChatAndJoin(restored, conv)
  await expect(
    restored.getByTestId('chat-message').filter({ hasText: 'first, from alice, before the backup' }),
  ).toHaveCount(1, { timeout: 25_000 })
  await expect(
    restored.getByTestId('chat-message').filter({ hasText: 'second, from bob, also before' }),
  ).toHaveCount(1)
  await expect(restored.getByTestId('chat-sealed-divider')).toHaveCount(0)

  // THE PHOTO PROMISE: the picture is back too. The restored device never uploaded it and
  // has no local photo cache — it re-fetches the server's encrypted blob and opens it with
  // the key the backup carried inside the message. A blank or broken image here means the
  // key did not survive the round trip.
  await expect(restored.locator('.pheme-photo img').first()).toBeVisible({ timeout: 25_000 })
  const photoSrc = await restored.locator('.pheme-photo img').first().getAttribute('src')
  expect(photoSrc).toMatch(/^blob:/)

  // And the conversation continues, in both directions, as the same device it always was.
  await send(alice.page, 'third, after the restore')
  await expect(restored.getByTestId('chat-message').last()).toContainText(
    'third, after the restore',
    { timeout: 25_000 },
  )
  await send(restored, 'fourth, from the restored device')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText(
    'fourth, from the restored device',
    { timeout: 25_000 },
  )

  await Promise.all([alice.context.close(), restoredContext.close()])
})
