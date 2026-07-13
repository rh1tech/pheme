import { test, expect } from '@playwright/test'
import { createChannel, loginAsAdmin, openChannelInfo, sendMessage } from './helpers'

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
})

test('owner can create a channel', async ({ page }) => {
  await createChannel(page, `E2E Channel ${Date.now()}`)
  // The trigger (public) id is exposed in the channel-info panel → Share this channel.
  await openChannelInfo(page)
  await expect(page.getByText('Trigger ID:', { exact: true })).toBeVisible()
})

test('owner can create an API key (token)', async ({ page }) => {
  await createChannel(page, `Keys ${Date.now()}`)

  await openChannelInfo(page)
  await page.getByRole('button', { name: 'Create key' }).click()

  // The plaintext key is shown once in a modal with a copy button.
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('heading', { name: 'API key created' })).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Copy key' })).toBeVisible()
})

test('owner can send a message from the composer', async ({ page }) => {
  await createChannel(page, `Send ${Date.now()}`)

  await sendMessage(page, 'Hello from E2E. This is a test notification.')

  await expect(page.getByText('Message sent')).toBeVisible()
})

// A small valid 8x8 PNG, uploaded and processed (re-encoded as JPEG) server-side.
const TEST_PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAgAAAAICAIAAABLbSncAAAAHElEQVR4nGJhYKhQYGDARCwgAhsYnBKAAAAA//9knwJZeWr4nQAAAABJRU5ErkJggg==',
  'base64',
)

test('owner can attach an image and send', async ({ page }) => {
  await createChannel(page, `Photos ${Date.now()}`)

  const composer = page.getByTestId('composer')

  // The composer's paperclip owns its (hidden) file input; set files directly.
  await composer.locator('input[type="file"]').setInputFiles({
    name: 'photo.png',
    mimeType: 'image/png',
    buffer: TEST_PNG,
  })

  // The selected image shows as a removable preview thumbnail.
  await expect(composer.getByRole('button', { name: 'Remove image' })).toBeVisible()

  await composer.getByRole('button', { name: 'Send' }).click()
  await expect(page.getByText('Message sent')).toBeVisible()
})
