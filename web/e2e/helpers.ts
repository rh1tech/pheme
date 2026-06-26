import { expect, type Page } from '@playwright/test'
import { ADMIN_EMAIL, ADMIN_PASSWORD } from './constants'

/** A unique email per call so repeated runs / retries never collide in the
 *  in-memory store. */
export function uniqueEmail(prefix = 'user'): string {
  return `${prefix}-${Date.now()}-${Math.floor(Math.random() * 1e6)}@pheme.test`
}

/** Logs in through the UI as the seeded admin and waits for the dashboard. */
export async function loginAsAdmin(page: Page): Promise<void> {
  await login(page, ADMIN_EMAIL, ADMIN_PASSWORD)
}

/** Logs in through the login form and waits for the channels dashboard. */
export async function login(page: Page, email: string, password: string): Promise<void> {
  await page.goto('/login')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password', { exact: true }).fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('heading', { name: 'Your channels' })).toBeVisible()
}

/** Opens the admin "Add user" modal and creates a user with the given role. */
export async function createUserViaAdmin(
  page: Page,
  email: string,
  password: string,
  role: 'user' | 'admin' = 'user',
): Promise<void> {
  await page.goto('/admin/users')
  await page.getByRole('button', { name: 'Add user' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Email').fill(email)
  await dialog.getByLabel('Password', { exact: true }).fill(password)
  if (role === 'admin') {
    await dialog.getByLabel('Role').click()
    await page.getByRole('option', { name: 'admin' }).click()
  }
  await dialog.getByRole('button', { name: 'Add user' }).click()
  await expect(page.getByText('User created')).toBeVisible()
}

/** Filters an admin list to a single term via its search bar. */
export async function adminSearch(page: Page, placeholder: string, term: string): Promise<void> {
  const search = page.getByPlaceholder(placeholder)
  await search.fill(term)
  await search.press('Enter')
}

/** Returns the table row containing the given text (email or channel name). */
export function rowFor(page: Page, text: string) {
  const escaped = text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return page.getByRole('row', { name: new RegExp(escaped) })
}

/** Opens the actions (•••) menu on the row matching the given text. */
export async function openRowMenu(page: Page, text: string): Promise<void> {
  await rowFor(page, text).getByRole('button').click()
}

/** Creates a channel from the dashboard and lands on its page. */
export async function createChannel(page: Page, name: string): Promise<void> {
  await page.goto('/')
  await page.getByRole('button', { name: 'New channel' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Channel name').fill(name)
  await dialog.getByRole('button', { name: 'Create channel' }).click()
  // Creating navigates to /channels/:id.
  await expect(page).toHaveURL(/\/channels\//)
  await expect(page.getByRole('heading', { name })).toBeVisible()
}
