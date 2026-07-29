import { test, expect } from '@playwright/test'
import {
  signInOnNewDevice,
  startDirectChat,
  createGroup,
  send,
  openChatAndJoin,
  renderedMessages,
  type Device,
} from './chat-helpers'

/**
 * Seeds encrypted conversations for the screenshot set.
 *
 * Chat messages are MLS ciphertext, so there is no server-side way to create
 * them: only a real client holds the keys. This drives real browsers, which is
 * why it lives in the Playwright suite rather than in the API seed script.
 *
 * Not a test. It asserts only enough to fail loudly if the seeding did not
 * actually happen, because a screenshot of an empty app is worse than no
 * screenshot.
 */

const PW = 'orchard-lantern-97'
const at = (slug: string) => `${slug}@pheme.test`

test.describe.configure({ mode: 'serial' })

test('seed encrypted conversations', async ({ browser }) => {
  test.setTimeout(300_000)

  const devices: Record<string, Device> = {}
  for (const slug of ['maya', 'tomas', 'kenji', 'nadia', 'ruth']) {
    devices[slug] = await signInOnNewDevice(browser, at(slug), PW)
  }

  // --- a direct chat: two people mid-conversation, not mid-introduction ------
  const dm = await startDirectChat(devices.maya.page, devices.tomas.userId)
  await openChatAndJoin(devices.maya.page, dm)
  await openChatAndJoin(devices.tomas.page, dm)

  const direct: Array<[keyof typeof devices, string]> = [
    ['maya', 'did the rollback actually take? i see 0.14.0 in the header still'],
    ['tomas', 'header is cached. check /version.json, it says 0.13.0'],
    ['maya', 'ok yes. 0.13.0. so the rollback is fine and the header is lying'],
    ['tomas', 'the header has always lied'],
    ['maya', 'i will add that to the docs'],
    ['tomas', 'sam will love that'],
    ['maya', 'anyway. do you want to look at the android push thing tomorrow or shall i just keep poking it'],
    ['tomas', 'tomorrow. i am done for today, my brain is soup'],
    ['maya', 'fair. 10am?'],
    ['tomas', '10 is fine 👍'],
  ]
  for (const [who, text] of direct) {
    await send(devices[who].page, text)
    await devices[who].page.waitForTimeout(220)
  }

  // --- a group: four people, one thread, some overlap -----------------------
  const group = await createGroup(devices.kenji.page, 'Incident 2026-07-24', [
    devices.maya.userId,
    devices.tomas.userId,
    devices.nadia.userId,
  ])
  for (const slug of ['kenji', 'maya', 'tomas', 'nadia']) {
    await openChatAndJoin(devices[slug].page, group)
  }

  const groupThread: Array<[keyof typeof devices, string]> = [
    ['kenji', 'starting a thread for this one so it does not live in on-call'],
    ['kenji', 'symptom: android devices stopped getting pushes at about 14:10'],
    ['maya', 'ios is fine? because that would point at fcm and not at us'],
    ['kenji', 'ios fine. web fine. only android'],
    ['tomas', 'we shipped the device-id split in 0.14.0. that touched the fcm token path'],
    ['maya', 'it did. and the migration backfills mlsDeviceId, which the android client reads'],
    ['nadia', 'is that why the settings screen shows two devices for me'],
    ['maya', 'almost certainly the same bug, yes'],
    ['tomas', 'rolling back 0.14.0 now'],
    ['kenji', 'pushes are landing again. confirmed on my pixel'],
    ['nadia', 'still two devices in settings though'],
    ['maya', 'that one is cosmetic, i will pick it up in the morning'],
    ['kenji', 'closing this out then. writeup tomorrow'],
  ]
  for (const [who, text] of groupThread) {
    await send(devices[who].page, text)
    await devices[who].page.waitForTimeout(220)
  }

  // --- a second, quieter direct chat, so the sidebar is not two rows --------
  const dm2 = await startDirectChat(devices.nadia.page, devices.ruth.userId)
  await openChatAndJoin(devices.nadia.page, dm2)
  await openChatAndJoin(devices.ruth.page, dm2)
  for (const [who, text] of [
    ['ruth', 'are you coming saturday? there will be a fire and questionable sausages'],
    ['nadia', 'yes. can i bring anything'],
    ['ruth', 'something to burn'],
    ['nadia', 'i have a chest of drawers that has wronged me'],
    ['ruth', 'perfect'],
  ] as Array<[keyof typeof devices, string]>) {
    await send(devices[who].page, text)
    await devices[who].page.waitForTimeout(220)
  }

  // Prove it actually encrypted and round-tripped, rather than silently no-oping.
  await expect
    .poll(async () => (await renderedMessages(devices.tomas.page)).length, { timeout: 30_000 })
    .toBeGreaterThan(3)

  // Hand the ids to the screenshot pass.
  const out = { dm, dm2, group, users: Object.fromEntries(
    Object.entries(devices).map(([k, d]) => [k, d.userId]),
  ) }
  // eslint-disable-next-line no-console
  console.log('SEEDED_CHATS ' + JSON.stringify(out))
})
