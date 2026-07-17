import { expect, test } from '@playwright/test'
import { createUserViaAdmin, login, loginAsAdmin, uniqueEmail } from './helpers'
import {
  backupTranscriptLen,
  deviceId,
  keyPackageCount,
  openChatAndJoin,
  send,
  signInOnNewDevice,
  startDirectChat,
  userId,
} from './chat-helpers'
import type { Browser } from '@playwright/test'
import type { Device } from './chat-helpers'

/** Like signInOnNewDevice, but with auto-backup's debounce shortened so a test need not idle a minute. */
async function signInWithFastAutoBackup(browser: Browser, email: string): Promise<Device> {
  const context = await browser.newContext()
  await context.addInitScript(() => {
    ;(window as { __phemeAutoBackupMs?: number }).__phemeAutoBackupMs = 400
  })
  const page = await context.newPage()
  await login(page, email, PASSWORD)
  await expect.poll(() => keyPackageCount(page), { timeout: 20_000 }).toBeGreaterThan(0)
  return { context, page, userId: await userId(page), deviceId: await deviceId(page) }
}

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

/**
 * Auto-backup: once a passphrase is set, messages that arrive AFTER keep flowing into the
 * server copy on their own, so a new device does not land on a stale snapshot.
 *
 * The user's earlier question was exactly this — does a backup freeze at the moment you take
 * it, or stay current? With auto-backup it stays current for as long as the app is open: each
 * new message schedules a (debounced) re-seal. This proves a message sent long after the
 * manual backup, never manually re-backed-up, still restores on a fresh device.
 */
test('auto-backup keeps later messages recoverable without a manual re-backup', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-auto')
  const bobEmail = uniqueEmail('bob-auto')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const doomed = await signInWithFastAutoBackup(browser, bobEmail)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, doomed.userId)
  await openChatAndJoin(alice.page, conv)
  await send(alice.page, 'before the backup was set')
  await openChatAndJoin(doomed.page, conv)
  await expect(doomed.page.getByTestId('chat-message').last()).toContainText(
    'before the backup was set',
    { timeout: 25_000 },
  )

  // Bob sets the passphrase — the one and only manual backup he will ever do — from the menu
  // WITHOUT leaving the chat. The unlocked passphrase lives in memory for the session; a full
  // reload would (by design) drop it, so the test navigates the way a person does, by not
  // reloading at all.
  await doomed.page.getByTestId('chat-sidebar').getByRole('button', { name: 'Menu' }).click()
  await doomed.page.getByRole('menuitem', { name: 'Chat backup' }).click()
  await doomed.page.getByLabel('Recovery passphrase', { exact: true }).fill(PASSPHRASE)
  await doomed.page.getByLabel('Confirm passphrase').fill(PASSPHRASE)
  await doomed.page.getByRole('button', { name: 'Back up now' }).click()
  await expect(doomed.page.getByText('Chats backed up')).toBeVisible({ timeout: 20_000 })

  const beforeLen = await backupTranscriptLen(doomed.page)

  // A message arrives AFTER the manual backup. Bob reads it on the same live page — and
  // auto-backup, unprompted, folds it into the server copy.
  await send(alice.page, 'this arrived only after the backup')
  await expect(doomed.page.getByTestId('chat-message').last()).toContainText(
    'this arrived only after the backup',
    { timeout: 25_000 },
  )

  // The server's sealed transcript grows on its own — no menu, no button.
  await expect
    .poll(() => backupTranscriptLen(doomed.page), { timeout: 20_000, intervals: [500] })
    .toBeGreaterThan(beforeLen)

  // Bob's device is lost, and he never touched the backup menu again.
  await doomed.context.close()

  const restoredContext = await browser.newContext()
  const restored = await restoredContext.newPage()
  await login(restored, bobEmail, PASSWORD)
  await expect(restored.getByText('Restore your chats')).toBeVisible({ timeout: 20_000 })
  await restored.getByLabel('Recovery passphrase', { exact: true }).fill(PASSPHRASE)
  await restored.getByRole('button', { name: 'Restore', exact: true }).click()
  await expect(restored.getByText('Restore your chats')).toBeHidden({ timeout: 30_000 })
  await expect(restored.getByTestId('chat-sidebar')).toBeVisible({ timeout: 30_000 })

  // The post-backup message — captured only by auto-backup — is there.
  await openChatAndJoin(restored, conv)
  await expect(
    restored.getByTestId('chat-message').filter({ hasText: 'this arrived only after the backup' }),
  ).toHaveCount(1, { timeout: 25_000 })
  await expect(
    restored.getByTestId('chat-message').filter({ hasText: 'before the backup was set' }),
  ).toHaveCount(1)

  await Promise.all([alice.context.close(), restoredContext.close()])
})
