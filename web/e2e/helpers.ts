import { expect, type Page } from '@playwright/test'
import { ADMIN_EMAIL, ADMIN_PASSWORD } from './constants'

/** A unique email per call so repeated runs / retries never collide in the
 *  in-memory store. */
export function uniqueEmail(prefix = 'user'): string {
  return `${prefix}-${Date.now()}-${Math.floor(Math.random() * 1e6)}@pheme.test`
}

/**
 * Navigates without tripping over the app's own boot-time redirects.
 *
 * A plain `page.goto(path)` waits for the `load` event, and if the SPA client-redirects while the
 * page is still loading — which it does during auth hydration, /login → / or / → /login — Playwright
 * reports the interrupted navigation as net::ERR_ABORTED and fails the test. It reads as flake but is
 * really the router doing its job mid-load. Waiting only for `commit` (the navigation is committed,
 * before the load event a redirect could interrupt) sidesteps it; a couple of retries cover the case
 * where the very first request is itself replaced. The caller still asserts on a real element after,
 * so "navigated but bounced elsewhere" cannot pass silently.
 */
export async function gotoStable(page: Page, path: string): Promise<void> {
  for (let attempt = 0; ; attempt++) {
    try {
      await page.goto(path, { waitUntil: 'commit' })
      return
    } catch (error) {
      // Two shapes of one thing: the app navigated while we were navigating. ERR_ABORTED is what
      // the browser reports when our request is replaced; "interrupted by another navigation" is
      // what Playwright reports when it notices the replacement itself. Both are the router doing
      // its job mid-load, and both deserve a retry. The caller still asserts on a real element
      // afterwards, so "navigated but bounced elsewhere" cannot pass silently.
      const message = error instanceof Error ? error.message : ''
      const raced =
        message.includes('ERR_ABORTED') || message.includes('interrupted by another navigation')
      if (!raced || attempt >= 3) throw error
    }
  }
}

/** Logs in through the UI as the seeded admin and waits for the dashboard. */
export async function loginAsAdmin(page: Page): Promise<void> {
  await login(page, ADMIN_EMAIL, ADMIN_PASSWORD)
}

/** Logs in through the login form and waits for the chat surface. */
/**
 * Signs in. By default it also suppresses the recovery-code UX that now auto-appears on a signed-in
 * device — the forced "save your code" modal (which would block the chat surface) and, when a backup
 * already exists, the restore prompt (a "new device" in most suites is an independent one that starts
 * fresh). Tests that specifically exercise the recovery UI pass `realRecovery: true` to see it.
 * Production sets neither flag.
 */
export async function login(
  page: Page,
  email: string,
  password: string,
  opts: { realRecovery?: boolean } = {},
): Promise<void> {
  if (!opts.realRecovery) {
    await page.addInitScript(() => {
      const w = window as { __phemeSkipRecoveryPrompt?: boolean; __phemeAutoStartFresh?: boolean }
      w.__phemeSkipRecoveryPrompt = true
      w.__phemeAutoStartFresh = true
    })
  }
  await gotoStable(page, '/login')
  await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password', { exact: true }).fill(password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByTestId('chat-sidebar')).toBeVisible()
  // Wait for the URL to settle, not merely for the surface to appear.
  //
  // Signing in ends with a client-side redirect from /login to /. The sidebar can become visible
  // while that navigation is still committing, so a caller that immediately calls page.goto races
  // the tail of it, and Playwright reports "Navigation to X is interrupted by another navigation
  // to /". That is what made membership, profile and users fail together on mobile-safari while
  // passing when run alone — WebKit is slower, so it loses the race more often.
  //
  // Asserting the URL makes login() mean "signed in and settled" rather than "the sidebar has
  // rendered".
  await expect(page).not.toHaveURL(/\/login/)
}

/** Opens the admin "Add user" modal and creates a user with the given role. */
export async function createUserViaAdmin(
  page: Page,
  email: string,
  password: string,
  role: 'user' | 'admin' = 'user',
): Promise<void> {
  await gotoStable(page, '/admin/users')
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

/** Creates a channel from the chat list's "+" menu and opens its conversation. */
export async function createChannel(page: Page, name: string): Promise<void> {
  await gotoStable(page, '/')
  await page.getByRole('button', { name: 'Create or subscribe to a channel' }).click()
  await page.getByRole('menuitem', { name: 'New channel' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Channel name').fill(name)
  await dialog.getByRole('button', { name: 'Create channel' }).click()
  // Creating navigates to /channels/:id.
  await expect(page).toHaveURL(/\/channels\//)
  await expect(page.getByTestId('chat-header')).toContainText(name)
}

/** Opens the channel-info panel (the ⋮ in the chat header) — the old Settings tab. */
export async function openChannelInfo(page: Page): Promise<void> {
  await page.getByRole('button', { name: 'Channel info' }).click()
  await expect(page.getByTestId('channel-info')).toBeVisible()
}

/**
 * Sends a message through the chat composer. There is one text box: the message's
 * title is its first sentence.
 */
export async function sendMessage(page: Page, text: string): Promise<void> {
  const composer = page.getByTestId('composer')
  await composer.getByTestId('composer-body').fill(text)
  await composer.getByRole('button', { name: 'Send' }).click()
}
