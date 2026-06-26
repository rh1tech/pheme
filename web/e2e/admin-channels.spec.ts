import { test, expect } from '@playwright/test'
import { adminSearch, createChannel, loginAsAdmin, openRowMenu, rowFor } from './helpers'

const SEARCH = 'Search by name'

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
})

test('admin can disable and re-enable a channel', async ({ page }) => {
  const name = `Disable ${Date.now()}`
  await createChannel(page, name)

  await page.goto('/admin/channels')
  await adminSearch(page, SEARCH, name)
  await expect(rowFor(page, name).getByText('active', { exact: true })).toBeVisible()

  await openRowMenu(page, name)
  await page.getByRole('menuitem', { name: 'Disable' }).click()
  await expect(page.getByText('Channel updated')).toBeVisible()
  await expect(rowFor(page, name).getByText('disabled', { exact: true })).toBeVisible()

  await openRowMenu(page, name)
  await page.getByRole('menuitem', { name: 'Enable' }).click()
  await expect(rowFor(page, name).getByText('active', { exact: true })).toBeVisible()
})

test('admin can delete a channel', async ({ page }) => {
  const name = `Remove ${Date.now()}`
  await createChannel(page, name)

  await page.goto('/admin/channels')
  await adminSearch(page, SEARCH, name)

  await openRowMenu(page, name)
  await page.getByRole('menuitem', { name: 'Delete' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()

  await expect(page.getByText('Channel deleted')).toBeVisible()
  await adminSearch(page, SEARCH, name)
  await expect(page.getByText('No channels.')).toBeVisible()
})
