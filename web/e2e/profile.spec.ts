import { test, expect } from '@playwright/test'
import {
  createUserViaAdmin,
  login,
  loginAsAdmin,
  uniqueEmail,
} from './helpers'

test('user can set a username and contact fields', async ({ page }) => {
  await loginAsAdmin(page)
  await page.goto('/profile')

  const username = `admin_${Date.now()}`
  await page.getByLabel('Username').fill(username)
  await page.getByLabel('Display name').fill('Site Admin')
  await page.getByLabel('Website').fill('https://pheme.test')
  await page.getByRole('button', { name: 'Save profile' }).click()

  await expect(page.getByText('Profile saved')).toBeVisible()
  // The value persists after a reload.
  await page.reload()
  await expect(page.getByLabel('Username')).toHaveValue(username)
})

test('a duplicate username is rejected', async ({ page }) => {
  // The admin claims a username first.
  await loginAsAdmin(page)
  await page.goto('/profile')
  const taken = `dup_${Date.now()}`
  await page.getByLabel('Username').fill(taken)
  await page.getByRole('button', { name: 'Save profile' }).click()
  await expect(page.getByText('Profile saved')).toBeVisible()

  // A second user cannot claim the same handle. Clear the session via storage
  // rather than the navbar logout button (which is hidden behind the burger on
  // the mobile project).
  const email = uniqueEmail('dupe')
  await createUserViaAdmin(page, email, 'abcd1234')
  await page.evaluate(() => localStorage.clear())
  await login(page, email, 'abcd1234')

  await page.goto('/profile')
  await page.getByLabel('Username').fill(taken)
  await page.getByRole('button', { name: 'Save profile' }).click()
  await expect(page.getByText('That username is already taken.')).toBeVisible()
})

test('the send panel exposes an allow-comments toggle', async ({ page }) => {
  await loginAsAdmin(page)
  await page.goto('/')
  await page.getByRole('button', { name: 'New channel' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Channel name').fill(`Comments ${Date.now()}`)
  await dialog.getByRole('button', { name: 'Create channel' }).click()
  await expect(page).toHaveURL(/\/channels\//)

  await page.getByRole('tab', { name: 'Send' }).click()
  const toggle = page.getByLabel('Allow comments')
  await expect(toggle).toBeChecked() // default on
  await toggle.click()
  await expect(toggle).not.toBeChecked()

  await page.getByLabel('Title').fill('No comments here')
  await page.getByRole('button', { name: 'Send' }).click()
  await expect(page.getByText('Message sent')).toBeVisible()
})
