import { test, expect, type Page } from '@playwright/test'
import { login, gotoStable } from './helpers'

/**
 * Channel screenshots.
 *
 * Separate from shots.spec.ts because channel posts are plaintext: any signed-in
 * session can read them, so this needs none of the same-device juggling the
 * encrypted chats do.
 */

const PW = 'orchard-lantern-97'
const OUT = '../screenshots/web'

async function shoot(page: Page, name: string) {
  await page.waitForLoadState('domcontentloaded')
  await page.waitForTimeout(2000)
  await page.addStyleTag({ content: '.mantine-Alert-root { display: none !important; }' }).catch(() => {})
  await page.screenshot({ path: `${OUT}/${name}.png`, animations: 'disabled' })
}

async function openChannelsTab(page: Page) {
  const tab = page.getByText('Channels', { exact: true }).first()
  await tab.click()
  await page.waitForTimeout(1200)
}

test('web — channels', async ({ browser }) => {
  test.setTimeout(180_000)
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    permissions: ['notifications'],
  })
  ctx.addInitScript(() => {
    const w = window as { __phemeSkipRecoveryPrompt?: boolean; __phemeAutoStartFresh?: boolean }
    w.__phemeSkipRecoveryPrompt = true
    w.__phemeAutoStartFresh = true
  })
  const page = await ctx.newPage()
  await login(page, 'maya@pheme.test', PW)
  await gotoStable(page, '/')
  await openChannelsTab(page)
  await expect(page.getByText('Deploys').first()).toBeVisible({ timeout: 20_000 })
  await shoot(page, '05-channels-list')

  await page.getByText('Deploys').first().click()
  await shoot(page, '06-channel-deploys')

  await openChannelsTab(page)
  const allotment = page.getByText('Allotment 14').first()
  if (await allotment.count()) {
    await allotment.click()
    await shoot(page, '08-channel-allotment')
  }
  await ctx.close()
})

test('web — channels on a phone', async ({ browser }) => {
  test.setTimeout(180_000)
  const ctx = await browser.newContext({
    viewport: { width: 390, height: 844 },
    deviceScaleFactor: 3,
    isMobile: true,
    hasTouch: true,
    permissions: ['notifications'],
  })
  ctx.addInitScript(() => {
    const w = window as { __phemeSkipRecoveryPrompt?: boolean; __phemeAutoStartFresh?: boolean }
    w.__phemeSkipRecoveryPrompt = true
    w.__phemeAutoStartFresh = true
  })
  const page = await ctx.newPage()
  await login(page, 'sam@pheme.test', PW)
  await gotoStable(page, '/')
  await openChannelsTab(page)
  await expect(page.getByText('Deploys').first()).toBeVisible({ timeout: 20_000 })
  await shoot(page, '13-mobile-channels')
  await page.getByText('Deploys').first().click()
  await shoot(page, '14-mobile-channel')
  await ctx.close()
})
