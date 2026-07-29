import { execFileSync } from 'node:child_process'
import { test, expect, type Browser, type Page } from '@playwright/test'
import { gotoStable, login } from '../e2e/helpers'
import { trackContext } from '../e2e/fixtures'
import {
  userId,
  deviceId,
  keyPackageCount,
  startDirectChat,
  createGroup,
  send,
  openChatAndJoin,
  renderedMessages,
  type Device,
} from '../e2e/chat-helpers'

/**
 * Seeds encrypted conversations AND captures the web screenshot set.
 *
 * The two have to happen in one place, and in the same browser contexts. A chat
 * message is MLS ciphertext, so only a device that was in the group when it was
 * sent can read it — screenshot from a fresh login and every sidebar row says
 * "Encrypted message", which is true, correct, and useless as a picture of the
 * product. The devices that send the messages are the ones that photograph them.
 */

const PW = 'orchard-lantern-97'
const OUT = '../screenshots/web'
const at = (slug: string) => `${slug}@pheme.test`

async function settle(page: Page) {
  // NOT networkidle: the app holds an SSE stream open for live events, so the
  // network never goes idle and waiting on it times out every time.
  await page.waitForLoadState('domcontentloaded')
  await page.waitForTimeout(2000)
}

async function shoot(page: Page, name: string) {
  await settle(page)
  // Drop the push-permission banner. Headless Chromium can never actually grant
  // the permission, so it is always up — a statement about the test browser
  // rather than about the product. It renders as a Mantine Alert, which is a
  // precise enough handle; matching on text hid whole layout containers.
  await page
    .addStyleTag({ content: '.mantine-Alert-root { display: none !important; }' })
    .catch(() => {})
  await page.screenshot({ path: `${OUT}/${name}.png`, animations: 'disabled' })
}

/**
 * A signed-in device whose context is sized and permissioned for photography.
 *
 * This is signInOnNewDevice with control over the context, which the shared
 * helper does not expose (its callers are tests and do not care how big the
 * window is). Same shape of thing: a fresh context is a fresh device, with its
 * own storage and therefore its own MLS identity.
 */
async function photogenic(
  browser: Browser,
  slug: string,
  viewport: { width: number; height: number },
  opts: { scale?: number; mobile?: boolean; dark?: boolean } = {},
): Promise<Device> {
  const context = await browser.newContext({
    viewport,
    deviceScaleFactor: opts.scale ?? 2,
    isMobile: opts.mobile ?? false,
    hasTouch: opts.mobile ?? false,
    // Otherwise every screenshot carries a "Notifications are blocked in this
    // browser" banner, which is a fact about Chromium's defaults rather than
    // anything about the app.
    permissions: ['notifications'],
    colorScheme: opts.dark ? 'dark' : 'light',
  })
  trackContext(context)
  await context.addInitScript(() => {
    const w = window as { __phemeSkipRecoveryPrompt?: boolean; __phemeAutoStartFresh?: boolean }
    w.__phemeSkipRecoveryPrompt = true
    w.__phemeAutoStartFresh = true
    // Headless Chromium reports notifications as denied whatever the context
    // grants, and the app quite correctly shows a banner about it. That banner
    // is a fact about the browser, not about Pheme, so it does not belong in a
    // picture of Pheme.
    try {
      Object.defineProperty(Notification, 'permission', { get: () => 'granted' })
    } catch {
      /* older engines: leave it alone */
    }
  })
  if (opts.dark) {
    await context.addInitScript(() =>
      localStorage.setItem('mantine-color-scheme-value', 'dark'),
    )
  }
  const page = await context.newPage()
  await login(page, at(slug), PW)
  // A device with no published KeyPackages cannot be added to a group, so
  // waiting here is the difference between seeding a conversation and seeding a
  // race.
  await expect.poll(() => keyPackageCount(page), { timeout: 30_000 }).toBeGreaterThan(0)
  return { context, page, userId: await userId(page), deviceId: await deviceId(page) }
}

test.describe.configure({ mode: 'serial' })

/** Everyone in the cast. Order matters only in that maya is the one we photograph. */
const CAST = ['maya', 'tomas', 'kenji', 'nadia', 'ruth', 'gabriel', 'ines', 'olav', 'priya', 'sam']

/**
 * Direct chats, each a short exchange that reads like the middle of something.
 * maya is in most of them, because hers is the sidebar in the screenshots and an
 * app with three rows in it looks like a demo rather than a product.
 */
const DIRECTS: Array<{ a: string; b: string; lines: Array<[string, string]> }> = [
  { a: 'maya', b: 'tomas', lines: [
    ['maya', 'did the rollback actually take? i still see 0.14.0 in the header'],
    ['tomas', 'header is cached. /version.json says 0.13.0'],
    ['maya', 'ok. so the rollback is fine and the header is lying'],
    ['tomas', 'the header has always lied'],
    ['maya', 'i will put that in the docs'],
    ['tomas', 'sam will be thrilled'],
    ['maya', 'do you want to look at the android push thing tomorrow, or shall i keep poking it'],
    ['tomas', 'tomorrow. my brain is soup'],
    ['maya', 'fair. 10am?'],
    ['tomas', '10 is fine 👍'],
  ]},
  { a: 'maya', b: 'kenji', lines: [
    ['kenji', 'paged at 04:12, back asleep by 04:20, it was the backup job again'],
    ['maya', 'we should just move the backup'],
    ['kenji', 'i have been saying this since march'],
    ['maya', 'i know. i am agreeing with you'],
    ['kenji', 'oh. then yes. moving it'],
  ]},
  { a: 'maya', b: 'nadia', lines: [
    ['nadia', 'the spacing on the channel rows is 12 and it should be 16, it is bugging me'],
    ['maya', 'send a patch, i will merge it'],
    ['nadia', 'it is four characters'],
    ['maya', 'best kind of patch'],
  ]},
  { a: 'maya', b: 'priya', lines: [
    ['priya', 'ios build is green again. it was the deployment target in the generated podspec'],
    ['maya', 'the one we fixed last month?'],
    ['priya', 'the one we thought we fixed last month'],
    ['maya', 'ah'],
  ]},
  { a: 'maya', b: 'sam', lines: [
    ['sam', 'i am rewriting the federation page, the old one assumed you already knew what a hub was'],
    ['maya', 'good. it also says "coming soon" about something we shipped in march'],
    ['sam', 'found that. found three of those'],
  ]},
  { a: 'maya', b: 'olav', lines: [
    ['olav', 'mirror in the basement is up, 0.14.2, nodelist serial 41'],
    ['maya', 'nice. does it see us?'],
    ['olav', 'liveness both ways. i sent myself a message from your host, it arrived'],
    ['maya', 'that is the whole feature working then'],
  ]},
  { a: 'maya', b: 'ines', lines: [
    ['ines', 'query for weekly actives is done, it is 40 lines and i am not proud of any of them'],
    ['maya', 'does it work'],
    ['ines', 'yes'],
    ['maya', 'then ship it and be ashamed later'],
  ]},
  { a: 'maya', b: 'gabriel', lines: [
    ['gabriel', 'are you free thursday, we are short a person for load-in'],
    ['maya', 'what time'],
    ['gabriel', '6ish. there is pizza in it for you'],
    ['maya', 'sold'],
  ]},
  { a: 'maya', b: 'ruth', lines: [
    ['ruth', 'your tomatoes are doing better than mine and i want you to know i resent it'],
    ['maya', 'i water them'],
    ['ruth', 'unnecessary'],
  ]},
  { a: 'priya', b: 'nadia', lines: [
    ['nadia', 'can you check the ios spacing on the channel row, i only have the android here'],
    ['priya', 'looks right. 16 top and bottom'],
    ['nadia', 'good. android was 12'],
    ['priya', 'android is always 12'],
  ]},
  { a: 'priya', b: 'kenji', lines: [
    ['kenji', 'did the pixel get the push in the end'],
    ['priya', 'yes, about forty seconds after the rollback'],
    ['kenji', 'forty seconds is fine'],
  ]},
  { a: 'priya', b: 'sam', lines: [
    ['sam', 'is the ios setup page still accurate'],
    ['priya', 'the xcode version is two behind but the steps are the same'],
    ['sam', 'i will bump the version and leave the rest'],
  ]},
  { a: 'nadia', b: 'ruth', lines: [
    ['ruth', 'coming saturday? there will be a fire and questionable sausages'],
    ['nadia', 'yes. can i bring anything'],
    ['ruth', 'something to burn'],
    ['nadia', 'i have a chest of drawers that has wronged me'],
    ['ruth', 'perfect'],
  ]},
]

/** Groups. maya is in all but one, so the sidebar mixes rooms with people. */
const GROUPS: Array<{ title: string; owner: string; members: string[]; lines: Array<[string, string]> }> = [
  { title: 'Incident 2026-07-24', owner: 'kenji', members: ['maya', 'tomas', 'nadia', 'priya'], lines: [
    ['kenji', 'thread for this one so it does not live in on-call'],
    ['kenji', 'android devices stopped getting pushes around 14:10'],
    ['maya', 'ios fine? that would point at fcm rather than at us'],
    ['priya', 'ios is fine, i have three devices here and all of them get pushes'],
    ['kenji', 'ios fine. web fine. android only'],
    ['tomas', 'we shipped the device-id split in 0.14.0, that touched the fcm token path'],
    ['maya', 'and the migration backfills mlsDeviceId, which the android client reads'],
    ['nadia', 'is that why settings shows two devices for me'],
    ['maya', 'almost certainly the same bug'],
    ['tomas', 'rolling back 0.14.0 now'],
    ['kenji', 'pushes landing again, confirmed on my pixel'],
    ['nadia', 'still two devices in settings though'],
    ['maya', 'cosmetic, i will pick it up in the morning'],
    ['kenji', 'closing this out. writeup tomorrow'],
  ]},
  { title: 'Platform', owner: 'maya', members: ['tomas', 'kenji', 'ines', 'olav', 'priya'], lines: [
    ['maya', 'standup in 10, i will keep it short'],
    ['tomas', 'i have one thing and it is the push bug'],
    ['ines', 'nothing from me, still on the analytics query'],
    ['olav', 'mirror is up. that is my whole update'],
    ['kenji', 'quiet night, one page, resolved itself'],
    ['priya', 'ios build is unblocked, that is me done'],
    ['maya', 'shortest standup we have ever had. see you tomorrow'],
  ]},
  { title: 'Release 0.15', owner: 'tomas', members: ['maya', 'priya', 'sam', 'nadia'], lines: [
    ['tomas', 'cutting 0.15 friday unless someone objects'],
    ['priya', 'ios needs one more day, the podspec fix is not merged'],
    ['sam', 'changelog is written but i need the final list'],
    ['maya', 'monday then. friday releases are how we end up working saturdays'],
    ['tomas', 'monday it is'],
    ['nadia', 'i will have the spacing fix in by then'],
  ]},
  { title: 'Allotment committee', owner: 'ruth', members: ['gabriel', 'sam', 'nadia'], lines: [
    ['ruth', 'agenda: the gate, the bees, and whether we are doing the fair this year'],
    ['gabriel', 'the gate again'],
    ['ruth', 'the gate is still broken gabriel'],
    ['sam', 'i can look at the gate on sunday'],
    ['ruth', 'noted and minuted'],
  ]},
  { title: 'Thursday band', owner: 'gabriel', members: ['nadia', 'ruth', 'maya'], lines: [
    ['gabriel', 'hall is free thursday after all'],
    ['nadia', 'i can do 7'],
    ['ruth', 'same'],
    ['maya', 'i will be late, standup runs over'],
    ['gabriel', 'your standup is at 9am'],
    ['maya', 'it runs VERY over'],
  ]},
]

/** Spreads the seeded messages back over the last fortnight, in Mongo. */
function backdate() {
  const script = `
    const now = new Date(), M = 60e3, H = 3600e3, D = 24*H;
    // Each conversation gets its own age, so the sidebar shows a mix of Today,
    // Yesterday, weekday names and dates rather than one wall of timestamps.
    const ages = [2*H, 6*H, 26*H, 30*H, 2.2*D, 3.1*D, 4.5*D, 5.4*D, 6.8*D, 8*D, 9.5*D, 11*D, 12.5*D, 13*D, 14*D];
    let i = 0;
    db.chatMessages.distinct("conversationId").forEach(cid => {
      let t = now.getTime() - ages[i++ % ages.length];
      db.chatMessages.find({conversationId: cid}).sort({createdAt:1, seq:1}).toArray()
        .forEach((m, idx) => {
          t += idx === 0 ? 0 : (idx % 5 === 0 ? 9*M : idx % 3 === 0 ? 4*M : 45e3);
          db.chatMessages.updateOne({_id: m._id}, {$set: {createdAt: new Date(t)}});
        });
    });
  `
  execFileSync('docker', [
    'exec', 'pheme-shots-mongo-1', 'mongosh',
    '-u', 'pheme', '-p', 'pheme', '--authenticationDatabase', 'admin',
    'pheme', '--quiet', '--eval', script,
  ], { stdio: 'pipe', env: { ...process.env, PATH: `${process.env.PATH}:${process.env.HOME}/.rd/bin` } })
}

test('seed conversations and capture the web set', async ({ browser }) => {
  test.setTimeout(1_800_000)

  // --- the front door, before anyone is signed in ---------------------------
  {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 })
    const page = await ctx.newPage()
    await gotoStable(page, '/login')
    await expect(page.getByRole('button', { name: /sign in/i }).first()).toBeVisible()
    await shoot(page, '01-login')
    await ctx.close()
  }

  // --- the whole cast, each on its own device -------------------------------
  const desktop = { width: 1440, height: 900 }
  const phone = { width: 390, height: 844 }
  const people: Record<string, Device> = {}
  for (const slug of CAST) {
    const opts =
      slug === 'nadia' ? { scale: 3, mobile: true } : slug === 'kenji' ? { dark: true } : {}
    people[slug] = await photogenic(browser, slug, slug === 'nadia' ? phone : desktop, opts)
  }

  // --- direct chats ---------------------------------------------------------
  const ids: Record<string, string> = {}
  for (const d of DIRECTS) {
    const conv = await startDirectChat(people[d.a].page, people[d.b].userId)
    ids[`${d.a}-${d.b}`] = conv
    await openChatAndJoin(people[d.a].page, conv)
    await openChatAndJoin(people[d.b].page, conv)
    for (const [who, text] of d.lines) {
      await send(people[who].page, text)
      await people[who].page.waitForTimeout(150)
    }
  }

  // --- groups ---------------------------------------------------------------
  for (const g of GROUPS) {
    const conv = await createGroup(
      people[g.owner].page,
      g.title,
      g.members.map((m) => people[m].userId),
    )
    ids[g.title] = conv
    for (const slug of [g.owner, ...g.members]) await openChatAndJoin(people[slug].page, conv)
    for (const [who, text] of g.lines) {
      await send(people[who].page, text)
      await people[who].page.waitForTimeout(150)
    }
  }

  // Push the timestamps into the past before photographing anything. The server
  // stamps createdAt on arrival, so seeding in one burst gives every message the
  // same minute, which reads as a fixture rather than a conversation. Clients
  // re-read times from the server and the plaintext cache is keyed by message
  // id, so a reload afterwards still decrypts.
  backdate()

  // Fail loudly rather than photograph an empty app.
  await expect
    .poll(async () => (await renderedMessages(people.maya.page)).length, { timeout: 30_000 })
    .toBeGreaterThan(3)

  const maya = people.maya
  const kenji = people.kenji
  const nadia = people.nadia
  for (const d of [maya, kenji, nadia]) await d.page.reload()

  // --- desktop, light -------------------------------------------------------
  await gotoStable(maya.page, '/')
  await shoot(maya.page, '02-home')
  await openChatAndJoin(maya.page, ids['Incident 2026-07-24'])
  await shoot(maya.page, '03-group-chat')
  await openChatAndJoin(maya.page, ids['maya-tomas'])
  await shoot(maya.page, '04-direct-chat')

  await gotoStable(maya.page, '/settings')
  await shoot(maya.page, '07-settings')

  // --- desktop, dark --------------------------------------------------------
  await gotoStable(kenji.page, '/')
  await shoot(kenji.page, '20-dark-home')
  await openChatAndJoin(kenji.page, ids['Incident 2026-07-24'])
  await shoot(kenji.page, '21-dark-group')

  // --- phone viewport -------------------------------------------------------
  await gotoStable(nadia.page, '/')
  await shoot(nadia.page, '10-mobile-list')
  await openChatAndJoin(nadia.page, ids['Incident 2026-07-24'])
  await shoot(nadia.page, '11-mobile-group')
  await openChatAndJoin(nadia.page, ids['nadia-ruth'])
  await shoot(nadia.page, '12-mobile-direct')

  console.log('SEEDED ' + JSON.stringify(Object.keys(ids)))
})
