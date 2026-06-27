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

// A small valid 8x8 PNG, uploaded and processed (re-encoded as JPEG) server-side.
const TEST_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAgAAAAICAIAAABLbSncAAAAHElEQVR4nGJhYKhQYGDARCwgAhsYnBKAAAAA//9knwJZeWr4nQAAAABJRU5ErkJggg==',
  'base64',
)

test('owner can attach an image and send', async ({ page }) => {
  await createChannel(page, `Photos ${Date.now()}`)

  await page.getByRole('tab', { name: 'Send' }).click()

  // The Mantine FileButton renders a hidden file input; set files directly.
  await page.locator('input[type="file"]').setInputFiles({
    name: 'photo.png',
    mimeType: 'image/png',
    buffer: TEST_PNG,
  })

  // The selected image shows as a removable preview thumbnail.
  await expect(page.getByRole('button', { name: 'Remove image' })).toBeVisible()

  await page.getByRole('button', { name: 'Send' }).click()
  await expect(page.getByText('Message sent')).toBeVisible()
})
