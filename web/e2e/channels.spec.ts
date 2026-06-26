import { test, expect } from '@playwright/test'
import { createChannel, loginAsAdmin } from './helpers'

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
})

test('owner can create a channel', async ({ page }) => {
  await createChannel(page, `E2E Channel ${Date.now()}`)
  // The channel page exposes its trigger (public) id.
  await expect(page.getByText('Trigger ID')).toBeVisible()
})

test('owner can create an API key (token)', async ({ page }) => {
  await createChannel(page, `Keys ${Date.now()}`)

  await page.getByRole('tab', { name: 'API keys' }).click()
  await page.getByRole('button', { name: 'Create key' }).click()

  // The plaintext key is shown once in a modal with a copy button.
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('heading', { name: 'API key created' })).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Copy key' })).toBeVisible()
})

test('owner can send a message from the UI', async ({ page }) => {
  await createChannel(page, `Send ${Date.now()}`)

  await page.getByRole('tab', { name: 'Send' }).click()
  await page.getByLabel('Title').fill('Hello from E2E')
  await page.getByLabel('Body').fill('This is a test notification.')
  await page.getByRole('button', { name: 'Send' }).click()

  await expect(page.getByText('Message sent')).toBeVisible()
})
