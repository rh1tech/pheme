import { test, expect } from '@playwright/test'
import { loginAsAdmin } from './helpers'

test('unauthenticated visit redirects to login', async ({ page }) => {
  await page.goto('/')
  await expect(page).toHaveURL(/\/login/)
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
})

test('seeded admin can log in and reach the admin panel', async ({ page }) => {
  await loginAsAdmin(page)
  await page.goto('/admin/users')
  await expect(page.getByRole('heading', { name: 'Users Control' })).toBeVisible()
})
