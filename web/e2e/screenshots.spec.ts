// A design-review harness, not an assertion suite.
//
// It stubs the channel/message READ endpoints with realistic data and captures
// the chat surface at each breakpoint in both themes. Stubbing is necessary
// because a sent message is only persisted by the dispatcher, which the e2e rig
// (in-memory drivers, app process only) does not run — so a live feed cannot be
// populated here. Auth still goes to the real API.
//
// Run: npx playwright test screenshots --project=chromium
import { test, type Page } from '@playwright/test'
import { loginAsAdmin } from './helpers'

const OUT = 'screenshots'
const WIDTHS = [320, 768, 1024, 1440]

const CHANNEL_ID = 'chan-deploys'

function iso(daysAgo: number, hour: number, minute: number): string {
  const d = new Date()
  d.setDate(d.getDate() - daysAgo)
  d.setHours(hour, minute, 0, 0)
  return d.toISOString()
}

const MESSAGES = [
  {
    id: 'm1',
    channelId: CHANNEL_ID,
    title: 'Release 2.12.0',
    body: 'Rolled out to eu-west-1. Canary held for 20 minutes, no error-rate change.',
    commentsAllowed: true,
    commentCount: 4,
    createdAt: iso(2, 9, 14),
  },
  {
    id: 'm2',
    channelId: CHANNEL_ID,
    title: '',
    body: 'Reminder: the on-call rotation changes at 18:00 UTC.',
    commentsAllowed: false,
    commentCount: 0,
    createdAt: iso(1, 11, 2),
  },
  {
    id: 'm3',
    channelId: CHANNEL_ID,
    title: 'Nightly build green',
    body: 'All 412 tests passing. Coverage 84.1% (+0.3).',
    commentsAllowed: true,
    commentCount: 0,
    createdAt: iso(1, 23, 47),
  },
  {
    id: 'm4',
    channelId: CHANNEL_ID,
    title: 'Latency spike',
    body: 'p99 on /v1/notify touched 840ms for ~4 minutes.\nCause: RabbitMQ connection churn after the node restart. Mitigated.',
    commentsAllowed: true,
    commentCount: 12,
    createdAt: iso(0, 8, 31),
  },
  {
    id: 'm5',
    channelId: CHANNEL_ID,
    title: 'Deploy finished',
    body: 'api v2.14.0 is live. 3 services updated, 0 rollbacks.',
    commentsAllowed: true,
    commentCount: 1,
    createdAt: iso(0, 14, 9),
  },
]

const CHANNELS = [
  {
    id: CHANNEL_ID,
    publicId: 'pub-deploys',
    ownerId: 'me',
    name: 'Deploys',
    alias: 'deploys',
    subscriptionMode: 'approval',
    status: 'active',
    createdAt: iso(30, 9, 0),
    lastMessage: {
      id: 'm5',
      title: 'Deploy finished',
      body: 'api v2.14.0 is live. 3 services updated, 0 rollbacks.',
      imageCount: 0,
      createdAt: iso(0, 14, 9),
    },
  },
  {
    id: 'chan-alerts',
    publicId: 'pub-alerts',
    ownerId: 'me',
    name: 'Production Alerts',
    subscriptionMode: 'approval',
    status: 'active',
    createdAt: iso(60, 9, 0),
    lastMessage: {
      id: 'a1',
      title: 'Disk usage 91%',
      body: 'db-primary /var is filling up.',
      imageCount: 0,
      createdAt: iso(0, 12, 40),
    },
  },
  {
    id: 'chan-billing',
    publicId: 'pub-billing',
    ownerId: 'me',
    name: 'Billing',
    subscriptionMode: 'open',
    status: 'active',
    createdAt: iso(90, 9, 0),
    lastMessage: {
      id: 'b1',
      title: '',
      body: '',
      imageCount: 3,
      createdAt: iso(1, 16, 5),
    },
  },
  {
    id: 'chan-status',
    publicId: 'pub-status',
    ownerId: 'other',
    name: 'Vendor Status',
    alias: 'vendorstatus',
    subscriptionMode: 'open',
    status: 'active',
    createdAt: iso(120, 9, 0),
    lastMessage: {
      id: 's1',
      title: 'Scheduled maintenance',
      body: 'CDN edge nodes in ap-south-1, Saturday 02:00–04:00 UTC.',
      imageCount: 0,
      createdAt: iso(4, 10, 22),
    },
  },
  {
    id: 'chan-quiet',
    publicId: 'pub-quiet',
    ownerId: 'me',
    name: 'Design Notes',
    subscriptionMode: 'approval',
    status: 'active',
    createdAt: iso(5, 9, 0),
  },
]

async function stubApi(page: Page) {
  await page.route('**/v1/channels', (route) =>
    route.fulfill({ json: { channels: CHANNELS.filter((c) => c.ownerId === 'me') } }),
  )
  await page.route('**/v1/channels/joined', (route) =>
    route.fulfill({
      json: {
        channels: CHANNELS.filter((c) => c.ownerId !== 'me').map((c) => ({
          ...c,
          role: 'user',
          memberStatus: 'active',
        })),
      },
    }),
  )
  await page.route(/\/v1\/channels\/[^/]+\/messages(\?|$)/, (route) =>
    // The API answers newest-first; the feed reverses it.
    route.fulfill({ json: { messages: [...MESSAGES].reverse(), nextCursor: '' } }),
  )
  await page.route(/\/v1\/channels\/[^/]+$/, (route) => {
    const id = route.request().url().split('/').pop() ?? ''
    const channel = CHANNELS.find((c) => c.id === id) ?? CHANNELS[0]
    route.fulfill({
      json: {
        channel,
        isOwner: channel.ownerId === 'me',
        role: channel.ownerId === 'me' ? 'admin' : 'user',
        status: 'active',
      },
    })
  })
  await page.route(/\/v1\/channels\/[^/]+\/members(\?|$)/, (route) =>
    route.fulfill({ json: { members: [], total: 0, offset: 0, limit: 50 } }),
  )
  await page.route(/\/v1\/channels\/[^/]+\/approvals$/, (route) =>
    route.fulfill({ json: { members: [], total: 0 } }),
  )
  await page.route(/\/v1\/channels\/[^/]+\/keys$/, (route) => route.fulfill({ json: { keys: [] } }))
  await page.route(/\/v1\/channels\/[^/]+\/messages\/[^/]+$/, (route) =>
    route.fulfill({ json: MESSAGES[MESSAGES.length - 1] }),
  )
  await page.route(/\/comments(\?|$)/, (route) =>
    route.fulfill({ json: { comments: [], nextCursor: '' } }),
  )
}

async function setTheme(page: Page, theme: 'light' | 'dark') {
  await page.evaluate((t) => {
    localStorage.setItem('mantine-color-scheme-value', t)
    document.documentElement.dataset.mantineColorScheme = t
  }, theme)
}

test('capture the chat surface', async ({ page }) => {
  // Opt-in: this writes files and asserts nothing, so it stays out of the suite.
  test.skip(!process.env.SCREENSHOTS, 'design-review harness — run with SCREENSHOTS=1')
  test.slow()
  await loginAsAdmin(page)
  await stubApi(page)

  for (const theme of ['light', 'dark'] as const) {
    await page.goto('/')
    await setTheme(page, theme)

    // Empty state + chat list.
    await page.setViewportSize({ width: 1440, height: 860 })
    await page.waitForTimeout(700)
    await page.screenshot({ path: `${OUT}/empty-1440-${theme}.png` })

    await page.setViewportSize({ width: 320, height: 860 })
    await page.waitForTimeout(400)
    await page.screenshot({ path: `${OUT}/list-320-${theme}.png` })

    // The feed, at every breakpoint.
    await page.goto(`/channels/${CHANNEL_ID}`)
    await page.waitForTimeout(900)
    for (const width of WIDTHS) {
      await page.setViewportSize({ width, height: 860 })
      await page.waitForTimeout(500)
      await page.screenshot({ path: `${OUT}/feed-${width}-${theme}.png` })
    }

    // Channel-info panel (owner view).
    await page.setViewportSize({ width: 1440, height: 860 })
    await page.getByRole('button', { name: 'Channel info' }).click()
    await page.waitForTimeout(700)
    await page.screenshot({ path: `${OUT}/info-1440-${theme}.png` })
    await page.getByTestId('channel-info').getByRole('button', { name: 'Close' }).click()

    // Discussion pane, desktop and phone.
    await page.goto(`/channels/${CHANNEL_ID}/messages/m5`)
    await page.waitForTimeout(900)
    await page.screenshot({ path: `${OUT}/discussion-1440-${theme}.png` })
    await page.setViewportSize({ width: 320, height: 860 })
    await page.waitForTimeout(500)
    await page.screenshot({ path: `${OUT}/discussion-320-${theme}.png` })
  }
})
