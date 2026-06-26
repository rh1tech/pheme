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
