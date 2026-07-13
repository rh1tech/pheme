import { expect, test } from '@playwright/test'
import { createChannel, loginAsAdmin } from './helpers'

// A phone-sized viewport, so the width-driven half of the rule applies. The behaviour
// is about viewport width and standalone mode, not engine, so it runs on chromium.
test.use({ viewport: { width: 390, height: 844 } })

// Makes the page report itself as an installed, standalone home-screen app — the only
// context the redirect acts in. Injected before any app code runs.
async function fakeStandalone(context: import('@playwright/test').BrowserContext) {
  await context.addInitScript(() => {
    const realMatchMedia = window.matchMedia.bind(window)
    window.matchMedia = (q: string) =>
      q.includes('display-mode: standalone')
        ? ({
            matches: true,
            media: q,
            onchange: null,
            addEventListener() {},
            removeEventListener() {},
            addListener() {},
            removeListener() {},
            dispatchEvent() {
              return false
            },
          } as unknown as MediaQueryList)
        : realMatchMedia(q)
  })
}

/**
 * The installed app, on a phone, opening on a channel URL lands on the list.
 *
 * iOS restores the last-visited URL when a home-screen web app is relaunched, so
 * someone last reading a channel reopens straight into it — and mobile is single-pane,
 * with no address bar, so the list they need is nowhere in sight. A cold launch of the
 * installed app should start at the list; a notification tap is the exception.
 */
test('standalone mobile cold launch on a channel URL redirects to the list', async ({ page }) => {
  await fakeStandalone(page.context())

  await loginAsAdmin(page)
  await createChannel(page, `Cold ${Date.now()}`)
  const channelUrl = page.url()
  expect(channelUrl).toMatch(/\/channels\//)

  // A full navigation onto the channel is the cold-launch case: it reloads the document
  // and resets the once-per-load guard, exactly as relaunching the installed app would.
  await page.goto(channelUrl)
  await expect(page).toHaveURL(/\/$/, { timeout: 10_000 })
  await expect(page.getByTestId('chat-sidebar')).toBeVisible()

  // A deep link from a notification tap is honoured, not redirected.
  await page.goto(`${channelUrl}?from=push`)
  await expect(page).toHaveURL(/\/channels\//)
})

/**
 * A normal browser tab is NOT redirected — it has an address bar, and a shared link to
 * a channel must open that channel. (This is also the context the rest of the suite
 * runs in, so a redirect here would break unrelated flows that navigate to a channel.)
 */
test('a normal browser tab keeps the channel URL it was opened on', async ({ page }) => {
  await loginAsAdmin(page)
  await createChannel(page, `Tab ${Date.now()}`)
  const channelUrl = page.url()

  await page.goto(channelUrl)
  await expect(page).toHaveURL(/\/channels\//)
})
