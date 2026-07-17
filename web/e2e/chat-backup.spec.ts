import { expect, test } from '@playwright/test'
import type { Browser, Page } from '@playwright/test'
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
import type { Device } from './chat-helpers'

const PASSWORD = 'Sup3rSecret!'

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

/** Signs in on a fresh device with auto-backup's debounce shortened, so a test need not idle a minute. */
async function signInWithFastAutoBackup(browser: Browser, email: string): Promise<Device> {
  const context = await browser.newContext()
  await context.addInitScript(() => {
    ;(window as { __phemeAutoBackupMs?: number; __phemeSkipRecoveryPrompt?: boolean }).__phemeAutoBackupMs = 400
    ;(window as { __phemeSkipRecoveryPrompt?: boolean }).__phemeSkipRecoveryPrompt = true
  })
  const page = await context.newPage()
  await login(page, email, PASSWORD)
  await expect.poll(() => keyPackageCount(page), { timeout: 20_000 }).toBeGreaterThan(0)
  return { context, page, userId: await userId(page), deviceId: await deviceId(page) }
}

/**
 * Reads the recovery code the device auto-generated, via the sidebar "Recovery code" menu. Waits for
 * the silent first backup to land (so the code is stored) before opening the menu.
 */
async function readRecoveryCode(page: Page): Promise<string> {
  await expect.poll(() => backupTranscriptLen(page), { timeout: 25_000 }).toBeGreaterThan(0)
  await page.getByTestId('chat-sidebar').getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Recovery code' }).click()
  const code = (await page.getByTestId('recovery-code').innerText()).trim()
  await page.keyboard.press('Escape')
  return code
}

/**
 * Waits until the backing device's server backup includes everything it has decrypted, by sending
 * one more message through it and watching the sealed transcript grow — the transcript is cumulative,
 * so a seal that lands after the marker holds every earlier body too. Requires fast auto-backup.
 */
async function flushBackup(backing: Page, sender: Page): Promise<void> {
  const before = await backupTranscriptLen(backing)
  const marker = `flush-${Math.random().toString(36).slice(2, 8)}`
  await send(sender, marker)
  await expect(backing.getByTestId('chat-message').filter({ hasText: marker })).toHaveCount(1, {
    timeout: 25_000,
  })
  await expect
    .poll(() => backupTranscriptLen(backing), { timeout: 20_000, intervals: [400] })
    .toBeGreaterThan(before)
}

/** Drives the "Restore your chats" gate on a fresh device with the given recovery code. */
async function restoreWithCode(page: Page, code: string): Promise<void> {
  await expect(page.getByText('Restore your chats')).toBeVisible({ timeout: 20_000 })
  await page.getByLabel('Recovery code', { exact: true }).fill(code)
  await page.getByRole('button', { name: 'Restore', exact: true }).click()
  // The prompt survives until the reload lands and the now-present keys keep the gate shut — wait
  // for it to actually go, not for the sidebar behind it.
  await expect(page.getByText('Restore your chats')).toBeHidden({ timeout: 30_000 })
  await expect(page.getByTestId('chat-sidebar')).toBeVisible({ timeout: 30_000 })
}

/**
 * The recovery code's whole promise, walked end to end.
 *
 * Decryption is one-shot: everything a device has read exists nowhere but its local cache. So the
 * backup carries the transcript (and photo keys), sealed under an app-generated recovery code the
 * server can never read. This is that promise: the code is generated automatically, the device is
 * lost, a fresh device enters the code, and the full history — text AND photo — is simply there.
 */
test('a recovery code carries the whole transcript to a new device', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-backup')
  const bobEmail = uniqueEmail('bob-backup')

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

  // A photo, too — its key rides in the message, which the transcript backup carries.
  const aliceComposer = alice.page.getByTestId('composer')
  await aliceComposer.locator('input[type="file"]').setInputFiles({
    name: 'before.png',
    mimeType: 'image/png',
    buffer: TEST_PNG,
  })
  await expect(aliceComposer.locator('.pheme-attachment img')).toBeVisible({ timeout: 15_000 })
  await aliceComposer.getByRole('button', { name: 'Send' }).click()
  await expect(doomed.page.locator('.pheme-photo img').last()).toBeVisible({ timeout: 25_000 })

  // Make sure everything Bob has read is in the server backup before he loses the device.
  await flushBackup(doomed.page, alice.page)

  // Bob's device auto-generated a recovery code and backed up. Read it, then lose the device.
  const code = await readRecoveryCode(doomed.page)
  expect(code).toMatch(/[0-9A-Z]{5}-/)
  await doomed.context.close()

  // A fresh device signs in, is offered the restore gate, and recovers with the code.
  const restoredContext = await browser.newContext()
  const restored = await restoredContext.newPage()
  await login(restored, bobEmail, PASSWORD, { realRecovery: true })
  await restoreWithCode(restored, code)

  // The history is there — text and photo — not sealed, not blank.
  await openChatAndJoin(restored, conv)
  await expect(
    restored.getByTestId('chat-message').filter({ hasText: 'first, from alice, before the backup' }),
  ).toHaveCount(1, { timeout: 25_000 })
  await expect(
    restored.getByTestId('chat-message').filter({ hasText: 'second, from bob, also before' }),
  ).toHaveCount(1)
  await expect(restored.locator('.pheme-photo img').first()).toBeVisible({ timeout: 25_000 })
  const photoSrc = await restored.locator('.pheme-photo img').first().getAttribute('src')
  expect(photoSrc).toMatch(/^blob:/)

  // And the conversation continues, both ways.
  await send(alice.page, 'third, after the restore')
  await expect(restored.getByTestId('chat-message').last()).toContainText('third, after the restore', {
    timeout: 25_000,
  })
  await send(restored, 'fourth, from the restored device')
  await expect(alice.page.getByTestId('chat-message').last()).toContainText(
    'fourth, from the restored device',
    { timeout: 25_000 },
  )

  await Promise.all([alice.context.close(), restoredContext.close()])
})

/**
 * The forced one-time prompt. On a device's first chat open, the recovery code is generated and shown
 * in a modal the user MUST acknowledge — this is what stops the code from being silently lost. This
 * test does NOT suppress the prompt (unlike the shared sign-in helper), so it exercises the real UI.
 */
test('the recovery code is generated and shown once, and it restores', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-prompt')
  const bobEmail = uniqueEmail('bob-prompt')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  // Sign Bob in WITHOUT the suppression flag — the forced modal must appear on its own.
  const bobContext = await browser.newContext()
  const bob = await bobContext.newPage()
  await login(bob, bobEmail, PASSWORD, { realRecovery: true })

  // The one-time modal appears unprompted, carrying the code.
  await expect(bob.getByText('Save your recovery code')).toBeVisible({ timeout: 25_000 })
  const code = (await bob.getByTestId('recovery-code').innerText()).trim()
  expect(code).toMatch(/^[0-9A-Z]{5}(-[0-9A-Z]{5}){4}$/)

  // It cannot be dismissed until the user confirms they saved it.
  const done = bob.getByRole('button', { name: 'Done' })
  await expect(done).toBeDisabled()
  await bob.getByText('I have saved my recovery code').click()
  await expect(done).toBeEnabled()
  await done.click()
  await expect(bob.getByText('Save your recovery code')).toBeHidden()

  // The code really restores: a fresh device recovers Bob's account with it.
  await bobContext.close()
  const restoredContext = await browser.newContext()
  const restored = await restoredContext.newPage()
  await login(restored, bobEmail, PASSWORD, { realRecovery: true })
  await restoreWithCode(restored, code)
  await expect(restored.getByTestId('chat-sidebar')).toBeVisible()

  await restoredContext.close()
})

/**
 * Auto-backup: once set up, messages that arrive AFTER keep flowing into the server copy on their
 * own, so a new device does not land on a stale snapshot. No manual re-backup.
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
  await send(alice.page, 'before the code was saved')
  await openChatAndJoin(doomed.page, conv)
  await expect(doomed.page.getByTestId('chat-message').last()).toContainText(
    'before the code was saved',
    { timeout: 25_000 },
  )

  const code = await readRecoveryCode(doomed.page)

  // A message arrives AFTER; Bob reads it on the live page and auto-backup folds it in, unprompted.
  await send(alice.page, 'this arrived only after the backup')
  await expect(doomed.page.getByTestId('chat-message').last()).toContainText(
    'this arrived only after the backup',
    { timeout: 25_000 },
  )
  // Guarantee the backup actually includes it before losing the device — flushBackup sends a marker
  // AFTER 'this arrived' was read, then waits for the sealed transcript to grow past it, so a seal
  // that lands then holds 'this arrived' too. (A bare "grew at all" poll can pass on an intermediate
  // backup that predates the message, which flaked under CI load.)
  await flushBackup(doomed.page, alice.page)

  // Bob's device is lost; he never re-backed-up. A fresh device restores and has the later message.
  await doomed.context.close()
  const restoredContext = await browser.newContext()
  const restored = await restoredContext.newPage()
  await login(restored, bobEmail, PASSWORD, { realRecovery: true })
  await restoreWithCode(restored, code)

  await openChatAndJoin(restored, conv)
  await expect(
    restored.getByTestId('chat-message').filter({ hasText: 'this arrived only after the backup' }),
  ).toHaveCount(1, { timeout: 25_000 })
  await expect(
    restored.getByTestId('chat-message').filter({ hasText: 'before the code was saved' }),
  ).toHaveCount(1)

  await Promise.all([alice.context.close(), restoredContext.close()])
})

/**
 * THE PARALLEL-DEVICE TRAP, with recovery codes. Keep using device A and ALSO restore on device B
 * while A is live. Restoring must not make B a CLONE of A (same MLS leaf) — else neither can read the
 * other. A restored device must be its OWN leaf: history from the code, own identity for messaging.
 */
test('a device restored alongside a still-live one is its own leaf, not a clone', async ({
  browser,
}) => {
  const peerEmail = uniqueEmail('peer-par')
  const bobEmail = uniqueEmail('bob-par')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, peerEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const deviceA = await signInWithFastAutoBackup(browser, bobEmail)
  const peer = await signInOnNewDevice(browser, peerEmail, PASSWORD)

  const conv = await startDirectChat(peer.page, deviceA.userId)
  await openChatAndJoin(peer.page, conv)
  await send(peer.page, 'hello from the peer')
  await openChatAndJoin(deviceA.page, conv)
  await expect(deviceA.page.getByTestId('chat-message').last()).toContainText('hello from the peer', {
    timeout: 25_000,
  })

  await flushBackup(deviceA.page, peer.page)
  const code = await readRecoveryCode(deviceA.page)

  // Device B signs in and restores — while device A is STILL OPEN and in use.
  const bContext = await browser.newContext()
  const deviceB = await bContext.newPage()
  await login(deviceB, bobEmail, PASSWORD, { realRecovery: true })
  await restoreWithCode(deviceB, code)

  // B must be its OWN device, not a clone of A.
  const bDeviceId = await deviceB.evaluate(() => localStorage.getItem('pheme.mlsDeviceId') ?? '')
  expect(bDeviceId).not.toBe(deviceA.deviceId)

  await openChatAndJoin(deviceB, conv)
  await expect(
    deviceB.getByTestId('chat-message').filter({ hasText: 'hello from the peer' }),
  ).toHaveCount(1, { timeout: 25_000 })

  // The two devices of the same user now talk BOTH WAYS.
  await send(deviceA.page, 'from device A')
  await expect(deviceB.getByTestId('chat-message').filter({ hasText: 'from device A' })).toHaveCount(
    1,
    { timeout: 25_000 },
  )
  await send(deviceB, 'from device B')
  await expect(
    deviceA.page.getByTestId('chat-message').filter({ hasText: 'from device B' }),
  ).toHaveCount(1, { timeout: 25_000 })

  for (const said of ['from device A', 'from device B']) {
    await expect(peer.page.getByTestId('chat-message').filter({ hasText: said })).toHaveCount(1, {
      timeout: 25_000,
    })
  }

  await Promise.all([peer.context.close(), deviceA.context.close(), bContext.close()])
})
