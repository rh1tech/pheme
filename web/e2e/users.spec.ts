import { test, expect, type Page } from '@playwright/test'
import { loginAsAdmin, uniqueEmail } from './helpers'

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
})

// Filters the users list to a single email via the search bar.
async function searchFor(page: Page, email: string): Promise<void> {
  const search = page.getByPlaceholder('Search by email')
  await search.fill(email)
  await search.press('Enter')
}

test('admin can create a user', async ({ page }) => {
  const email = uniqueEmail('created')
  await page.goto('/admin/users')

  await page.getByRole('button', { name: 'Add user' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Email').fill(email)
  await dialog.getByLabel('Password', { exact: true }).fill('Created12345')
  await dialog.getByRole('button', { name: 'Add user' }).click()

  await expect(page.getByText('User created')).toBeVisible()
  await searchFor(page, email)
  await expect(page.getByRole('cell', { name: email })).toBeVisible()
})

test('admin can delete a user', async ({ page }) => {
  const email = uniqueEmail('victim')
  await page.goto('/admin/users')

  // Create the user to delete.
  await page.getByRole('button', { name: 'Add user' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Email').fill(email)
  await dialog.getByLabel('Password', { exact: true }).fill('Victim12345')
  await dialog.getByRole('button', { name: 'Add user' }).click()
  await expect(page.getByText('User created')).toBeVisible()

  // Find its row, open the actions menu, and delete it.
  await searchFor(page, email)
  const row = page.getByRole('row', { name: new RegExp(email.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')) })
  await row.getByRole('button').click()
  await page.getByRole('menuitem', { name: 'Delete' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()

  await expect(page.getByText('User deleted')).toBeVisible()
  await searchFor(page, email)
  await expect(page.getByText('No users.')).toBeVisible()
})
