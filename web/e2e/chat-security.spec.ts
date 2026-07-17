import { expect, test } from '@playwright/test'
import { createUserViaAdmin, loginAsAdmin, uniqueEmail } from './helpers'
import { signInOnNewDevice } from './chat-helpers'

const PASSWORD = 'Sup3rSecret!'

test.skip(({ browserName }) => browserName !== 'chromium', 'crypto round-trip: chromium only')
test.describe.configure({ timeout: 120_000 })

/**
 * "Devices & security" shows the user their own devices and their E2E status. A device registers
 * itself when it publishes its KeyPackages, so a signed-in device sees itself listed, flagged as the
 * current one, and — since auto-backup runs by default — its chats reported as backed up.
 */
test('the security panel lists this device and shows backup status', async ({ browser }) => {
  const email = uniqueEmail('sec')
  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, email, PASSWORD)
  await setup.close()

  const device = await signInOnNewDevice(browser, email, PASSWORD)

  // Open the panel from the sidebar menu.
  await device.page.getByTestId('chat-sidebar').getByRole('button', { name: 'Menu' }).click()
  await device.page.getByRole('menuitem', { name: 'Devices & security' }).click()

  // This device is listed and flagged as the current one.
  const rows = device.page.getByTestId('security-device')
  await expect(rows).toHaveCount(1, { timeout: 20_000 })
  await expect(rows.first()).toContainText('This device')
  // The label is a real browser/OS string, not a raw id.
  await expect(rows.first()).toContainText(/on (macOS|Windows|Linux|iOS|Android)|Chrome|Safari|Firefox/)

  // Auto-backup ran by default, so the panel reports the chats as backed up.
  await expect(device.page.getByText('Chats backed up')).toBeVisible({ timeout: 20_000 })

  await device.context.close()
})

/**
 * A user's TWO devices both appear in the panel — each sees both, and flags only itself as current.
 */
test('the security panel lists all of a user’s devices', async ({ browser }) => {
  const email = uniqueEmail('sec2')
  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, email, PASSWORD)
  await setup.close()

  const one = await signInOnNewDevice(browser, email, PASSWORD)
  const two = await signInOnNewDevice(browser, email, PASSWORD)

  // On the second device, both devices show; exactly one is "This device".
  await two.page.getByTestId('chat-sidebar').getByRole('button', { name: 'Menu' }).click()
  await two.page.getByRole('menuitem', { name: 'Devices & security' }).click()
  await expect(two.page.getByTestId('security-device')).toHaveCount(2, { timeout: 20_000 })
  await expect(two.page.getByText('This device')).toHaveCount(1)

  await Promise.all([one.context.close(), two.context.close()])
})
