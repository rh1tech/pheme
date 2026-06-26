import { test, expect } from '@playwright/test'
import { adminSearch, createUserViaAdmin, loginAsAdmin, openRowMenu, rowFor, uniqueEmail } from './helpers'

const SEARCH = 'Search by email'

test.beforeEach(async ({ page }) => {
  await loginAsAdmin(page)
})

test('admin can create a user', async ({ page }) => {
  const email = uniqueEmail('created')
  await createUserViaAdmin(page, email, 'Created12345')

  await adminSearch(page, SEARCH, email)
  await expect(page.getByRole('cell', { name: email })).toBeVisible()
  await expect(rowFor(page, email).getByText('user', { exact: true })).toBeVisible()
})

test('admin can create a user with the admin role', async ({ page }) => {
  const email = uniqueEmail('boss')
  await createUserViaAdmin(page, email, 'Boss12345', 'admin')

  await adminSearch(page, SEARCH, email)
  await expect(rowFor(page, email).getByText('admin', { exact: true })).toBeVisible()
})

test('admin can promote and demote a user', async ({ page }) => {
  const email = uniqueEmail('role')
  await createUserViaAdmin(page, email, 'Role12345')
  await adminSearch(page, SEARCH, email)

  await openRowMenu(page, email)
  await page.getByRole('menuitem', { name: 'Make admin' }).click()
  await expect(page.getByText('User updated')).toBeVisible()
  await expect(rowFor(page, email).getByText('admin', { exact: true })).toBeVisible()

  await openRowMenu(page, email)
  await page.getByRole('menuitem', { name: 'Make user' }).click()
  await expect(rowFor(page, email).getByText('user', { exact: true })).toBeVisible()
})

test('admin can block and unblock a user', async ({ page }) => {
  const email = uniqueEmail('block')
  await createUserViaAdmin(page, email, 'Block12345')
  await adminSearch(page, SEARCH, email)
  await expect(rowFor(page, email).getByText('active', { exact: true })).toBeVisible()

  await openRowMenu(page, email)
  await page.getByRole('menuitem', { name: 'Block' }).click()
  await expect(page.getByText('User updated')).toBeVisible()
  await expect(rowFor(page, email).getByText('blocked', { exact: true })).toBeVisible()

  await openRowMenu(page, email)
  await page.getByRole('menuitem', { name: 'Unblock' }).click()
  await expect(rowFor(page, email).getByText('active', { exact: true })).toBeVisible()
})

test('a blocked user cannot log in', async ({ page }) => {
  const email = uniqueEmail('locked')
  const password = 'Locked12345'
  await createUserViaAdmin(page, email, password)
  await adminSearch(page, SEARCH, email)

  // Block them.
  await openRowMenu(page, email)
  await page.getByRole('menuitem', { name: 'Block' }).click()
  await expect(rowFor(page, email).getByText('blocked', { exact: true })).toBeVisible()

  // Attempting to log in as the blocked user is rejected.
  await page.goto('/login')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password', { exact: true }).fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText(/blocked/i)).toBeVisible()
  await expect(page).toHaveURL(/\/login/)
})

test('admin can reset a user password', async ({ page }) => {
  const email = uniqueEmail('reset')
  await createUserViaAdmin(page, email, 'Reset12345')
  await adminSearch(page, SEARCH, email)

  await openRowMenu(page, email)
  await page.getByRole('menuitem', { name: 'Reset password' }).click()

  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('New password').fill('Brandnew99')
  await dialog.getByRole('button', { name: 'Reset password' }).click()
  await expect(page.getByText('Password reset')).toBeVisible()
})

test('admin can delete a user', async ({ page }) => {
  const email = uniqueEmail('victim')
  await createUserViaAdmin(page, email, 'Victim12345')
  await adminSearch(page, SEARCH, email)

  await openRowMenu(page, email)
  await page.getByRole('menuitem', { name: 'Delete' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Delete' }).click()

  await expect(page.getByText('User deleted')).toBeVisible()
  await adminSearch(page, SEARCH, email)
  await expect(page.getByText('No users.')).toBeVisible()
})
