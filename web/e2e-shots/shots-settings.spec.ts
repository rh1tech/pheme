import { test, expect, type Page } from '@playwright/test'
import { login, gotoStable } from '../e2e/helpers'

/**
 * The account and settings surfaces on web.
 *
 * There is no /settings route — the earlier shot named that was the router's
 * catch-all redirecting to the chat list, which is why it looked wrong. What the
 * mobile Settings screen covers is split on web across the sidebar menu, the
 * profile page and the devices/security dialog, so all three get photographed.
 *
 * None of these read encrypted history, so a fresh sign-in is fine here.
 */

const PW = 'orchard-lantern-97'
const OUT = '../screenshots/web'

async function shoot(page: Page, name: string) {
  await page.waitForLoadState('domcontentloaded')
  await page.waitForTimeout(1800)
  await page
    .addStyleTag({ content: '.mantine-Alert-root { display: none !important; }' })
    .catch(() => {})
  await page.screenshot({ path: `${OUT}/${name}.png`, animations: 'disabled' })
}

test.describe.configure({ mode: 'serial' })

test('web — profile, menu and security', async ({ browser }) => {
  test.setTimeout(240_000)
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    permissions: ['notifications'],
  })
  await ctx.addInitScript(() => {
    const w = window as { __phemeSkipRecoveryPrompt?: boolean; __phemeAutoStartFresh?: boolean }
    w.__phemeSkipRecoveryPrompt = true
    w.__phemeAutoStartFresh = true
  })
  const page = await ctx.newPage()
  await login(page, 'maya@pheme.test', PW)

  // The profile page: avatar, username, display name, bio.
  await gotoStable(page, '/profile')
  await expect(page.getByText(/username/i).first()).toBeVisible({ timeout: 20_000 })
  await shoot(page, '07-profile')

  // The sidebar menu, which is where the rest of the settings live.
  await gotoStable(page, '/')
  const burger = page.getByRole('button', { name: /menu/i }).first()
  await burger.click()
  await page.waitForTimeout(900)
  await shoot(page, '08-menu')

  // Devices & security, one of that menu's entries.
  const security = page.getByRole('menuitem', { name: /device|security/i }).first()
  if (await security.count()) {
    await security.click()
    await page.waitForTimeout(1500)
    await shoot(page, '09-devices-security')
  }

  await ctx.close()
})

test('web — admin', async ({ browser }) => {
  test.setTimeout(180_000)
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    permissions: ['notifications'],
  })
  await ctx.addInitScript(() => {
    const w = window as { __phemeSkipRecoveryPrompt?: boolean }
    w.__phemeSkipRecoveryPrompt = true
  })
  const page = await ctx.newPage()
  await login(page, 'admin@pheme.test', 'admin-password-1')
  await gotoStable(page, '/admin/users')
  await page.waitForTimeout(2500)
  if (await page.getByRole('table').count()) await shoot(page, '30-admin-users')
  await gotoStable(page, '/admin/channels')
  await page.waitForTimeout(2500)
  if (await page.getByRole('table').count()) await shoot(page, '31-admin-channels')
  await ctx.close()
})
