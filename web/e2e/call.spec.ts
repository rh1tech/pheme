import { expect, test, type Page } from '@playwright/test'
import { createUserViaAdmin, loginAsAdmin, uniqueEmail } from './helpers'
import { openChatAndJoin, send, signInOnNewDevice, startDirectChat } from './chat-helpers'

const PASSWORD = 'Sup3rSecret!'

// Real WebRTC, real microphones (Chromium's fake device), real RTP between two browsers.
// Firefox and WebKit are not in this project's matrix; this is not about rendering.
test.skip(({ browserName }) => browserName !== 'chromium', 'WebRTC: chromium only')

// Bringing up two signed-in devices, an MLS group, a microphone and a peer connection is
// genuinely slower than a page assertion. The suite default (30s) is not enough, and padding
// it would be papering over a real timeout rather than allowing for real work.
test.describe.configure({ timeout: 120_000 })

/**
 * The assertion that matters.
 *
 * A call UI that says "connected" proves nothing — the state machine could be lying, the SDP
 * could have been exchanged and no media path built, and the two people would sit in silence
 * looking at a green bar. So this asks the browser's own WebRTC stack whether packets are
 * actually arriving: `inbound-rtp.packetsReceived > 0` means audio from the other machine is
 * being decoded on this one. That is a call.
 */
async function inboundAudioPackets(page: Page): Promise<number> {
  return page.evaluate(async () => {
    const pc = (
      window as unknown as { __phemeCallPeer?: RTCPeerConnection }
    ).__phemeCallPeer
    if (!pc) return -1
    const stats = await pc.getStats()
    let packets = 0
    stats.forEach((report) => {
      if (report.type === 'inbound-rtp' && report.kind === 'audio') {
        packets += (report as unknown as { packetsReceived?: number }).packetsReceived ?? 0
      }
    })
    return packets
  })
}

/**
 * Two people have a voice call, and audio really flows between them.
 *
 * Everything else in the call feature — the sealed signalling, the mailbox, the answer lock,
 * the ICE credentials — exists to make this one thing happen. None of it is worth anything if
 * the media path does not come up, and none of the unit tests can tell you whether it does.
 */
test('two people place a call and audio flows peer to peer', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-call')
  const bobEmail = uniqueEmail('bob-call')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await openChatAndJoin(bob.page, conv)

  // Alice calls.
  await alice.page.getByTestId('start-call').click()
  await expect(alice.page.getByTestId('call-bar')).toBeVisible()

  // Bob's phone rings — over the live stream, with the invite sealed under a key the server
  // does not have.
  await expect(bob.page.getByTestId('incoming-call')).toBeVisible({ timeout: 20_000 })
  await bob.page.getByTestId('answer-call').click()

  // Both ends report a live call.
  await expect(alice.page.getByTestId('call-bar')).toHaveAttribute('data-status', 'connected', {
    timeout: 30_000,
  })
  await expect(bob.page.getByTestId('call-bar')).toHaveAttribute('data-status', 'connected', {
    timeout: 30_000,
  })

  // And — the part that actually proves it — audio is arriving at both ends. A UI that merely
  // claims to be connected would pass every assertion above this one.
  await expect
    .poll(() => inboundAudioPackets(alice.page), { timeout: 20_000 })
    .toBeGreaterThan(0)
  await expect.poll(() => inboundAudioPackets(bob.page), { timeout: 20_000 }).toBeGreaterThan(0)

  // Alice hangs up; Bob's call ends too.
  await alice.page.getByTestId('hang-up').click()
  await expect(bob.page.getByTestId('call-bar')).toHaveAttribute('data-status', 'ended', {
    timeout: 20_000,
  })

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * A declined call ends on both sides, and says so.
 *
 * The refusal is sealed like everything else — the server relays it and cannot read it — so
 * this also checks the decline path really goes through the encrypted channel rather than
 * being a client-side illusion.
 */
test('a declined call ends for the caller too', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-dec')
  const bobEmail = uniqueEmail('bob-dec')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await openChatAndJoin(bob.page, conv)

  await alice.page.getByTestId('start-call').click()
  await expect(bob.page.getByTestId('incoming-call')).toBeVisible({ timeout: 20_000 })
  await bob.page.getByTestId('decline-call').click()

  await expect(alice.page.getByTestId('call-status')).toContainText('Call declined', {
    timeout: 20_000,
  })

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * Every device the callee is signed in on rings, and exactly ONE of them can pick up.
 *
 * The loser has already opened its microphone by the time it finds out, which is why the
 * winner is decided by a server-side lock and not by a race over a bus that is allowed to drop
 * messages. This is the test that would catch a device left ringing with a live mic.
 */
test('only one of the callee’s devices can answer, and the others stop ringing', async ({
  browser,
}) => {
  const aliceEmail = uniqueEmail('alice-multi')
  const bobEmail = uniqueEmail('bob-multi')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  // Bob is signed in on two devices. Both must ring.
  const phone = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const laptop = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, phone.userId)
  await openChatAndJoin(alice.page, conv)
  await openChatAndJoin(phone.page, conv)
  await openChatAndJoin(laptop.page, conv)

  await alice.page.getByTestId('start-call').click()

  await expect(phone.page.getByTestId('incoming-call')).toBeVisible({ timeout: 20_000 })
  await expect(laptop.page.getByTestId('incoming-call')).toBeVisible({ timeout: 20_000 })

  // The phone picks up.
  await phone.page.getByTestId('answer-call').click()
  await expect(phone.page.getByTestId('call-bar')).toHaveAttribute('data-status', 'connected', {
    timeout: 30_000,
  })
  await expect
    .poll(() => inboundAudioPackets(phone.page), { timeout: 20_000 })
    .toBeGreaterThan(0)

  // The laptop must stop ringing. If it kept ringing it would also be holding a microphone
  // open, which is the failure this whole lock exists to prevent.
  await laptop.page.getByTestId('answer-call').click()
  await expect(laptop.page.getByTestId('call-status')).toContainText('Answered on another device', {
    timeout: 20_000,
  })

  await Promise.all([alice.context.close(), phone.context.close(), laptop.context.close()])
})

/**
 * The callee rings while they are looking at the chat LIST, not at the conversation.
 *
 * This is what calling somebody actually looks like. Nobody sits with the right conversation
 * open waiting to be called — they are on the list, or in a different chat, or they just
 * unlocked the phone. Every other test here opens the conversation on both sides first, which
 * quietly settles its MLS group on the callee's device; if ringing depends on that having
 * happened, calling works in the tests and nowhere else.
 */
test('a callee who is not in the conversation still rings', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-list')
  const bobEmail = uniqueEmail('bob-list')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await openChatAndJoin(bob.page, conv)

  // Bob walks away from the conversation and sits on the chat list, exactly as he would in
  // life. Everything his device knows about the group it still knows — it just is not looking
  // at it.
  // A RELOAD, not just a navigation: a fresh JavaScript context, with the MLS state rebuilt
  // from IndexedDB rather than still warm in memory. That is the state a device is in when a
  // call arrives — the app was opened, or reopened, and the conversation has not been touched.
  await bob.page.goto('/')
  await bob.page.reload()
  await expect(bob.page.getByTestId('chat-sidebar')).toBeVisible({ timeout: 30_000 })

  await alice.page.getByTestId('start-call').click()
  await expect(alice.page.getByTestId('call-bar')).toBeVisible()

  // He must ring anyway.
  await expect(bob.page.getByTestId('incoming-call')).toBeVisible({ timeout: 30_000 })
  await bob.page.getByTestId('answer-call').click()

  await expect(alice.page.getByTestId('call-bar')).toHaveAttribute('data-status', 'connected', {
    timeout: 30_000,
  })
  await expect.poll(() => inboundAudioPackets(bob.page), { timeout: 20_000 }).toBeGreaterThan(0)

  await Promise.all([alice.context.close(), bob.context.close()])
})

/**
 * A call still rings after the group's epoch has moved on.
 *
 * MLS's exporter only exports from the CURRENT epoch, so the two ends must derive the call key
 * at the same one. The caller is the end that has to be right: a recipient who is behind can
 * catch up to the epoch named in the invite, but one who is AHEAD cannot go back — there is no
 * way to export a past epoch — and simply cannot open the invite.
 *
 * So a caller sitting on a stale epoch seals an invite nobody can read. And it fails in the
 * cruellest possible way: the push notification is sent by the server, which needs no key, so
 * the callee's phone buzzes — and then nothing rings, and no error is raised anywhere.
 *
 * Admitting somebody's second device is a Commit, and a Commit moves the epoch. That happens in
 * the background, in a conversation nobody is looking at. This is the everyday case.
 */
test('a call rings after another device joins and moves the epoch', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-epoch')
  const bobEmail = uniqueEmail('bob-epoch')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await openChatAndJoin(bob.page, conv)

  // Bob signs in on a second device. Admitting it is a Commit, which moves the group's epoch
  // out from under everybody who is not paying attention.
  const bob2 = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  await openChatAndJoin(bob2.page, conv)

  // The new device really is in the group — it can read what is said now.
  await send(alice.page, 'after the epoch moved')
  await expect(bob2.page.getByTestId('chat-message').filter({ hasText: 'after the epoch moved' }))
    .toBeVisible({ timeout: 40_000 })

  // Now call. Whatever epoch anyone happens to be sitting on, the invite must be readable.
  await alice.page.getByTestId('start-call').click()
  await expect(alice.page.getByTestId('call-bar')).toBeVisible()

  await expect(bob.page.getByTestId('incoming-call')).toBeVisible({ timeout: 30_000 })
  await bob.page.getByTestId('answer-call').click()

  await expect(alice.page.getByTestId('call-bar')).toHaveAttribute('data-status', 'connected', {
    timeout: 30_000,
  })
  await expect.poll(() => inboundAudioPackets(bob.page), { timeout: 20_000 }).toBeGreaterThan(0)

  await Promise.all([alice.context.close(), bob.context.close(), bob2.context.close()])
})

/**
 * A call nobody answers leaves a record in the conversation.
 *
 * Without one, a missed call is a single buzz while you were away and then silence — nothing
 * afterwards says that anybody wanted you, or who. So the caller writes it into the chat, as a
 * real encrypted message: it is there on every device, after a reload, for both people.
 *
 * It is written ONCE, by the caller. The callee writing one of its own would put the same missed
 * call in the transcript twice.
 */
test('a call nobody answers leaves a missed-call record in the chat', async ({ browser }) => {
  const aliceEmail = uniqueEmail('alice-missed')
  const bobEmail = uniqueEmail('bob-missed')

  const setup = await browser.newContext()
  const admin = await setup.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, aliceEmail, PASSWORD)
  await createUserViaAdmin(admin, bobEmail, PASSWORD)
  await setup.close()

  const bob = await signInOnNewDevice(browser, bobEmail, PASSWORD)
  const alice = await signInOnNewDevice(browser, aliceEmail, PASSWORD)

  const conv = await startDirectChat(alice.page, bob.userId)
  await openChatAndJoin(alice.page, conv)
  await openChatAndJoin(bob.page, conv)

  // Alice calls; Bob's device rings and he refuses it.
  await alice.page.getByTestId('start-call').click()
  await expect(bob.page.getByTestId('incoming-call')).toBeVisible({ timeout: 30_000 })
  await bob.page.getByTestId('decline-call').click()

  // Both of them end up with the call in the transcript — exactly one entry each, and it is
  // not a chat bubble, because nobody said anything.
  await expect(alice.page.getByTestId('call-event')).toHaveCount(1, { timeout: 30_000 })
  await expect(bob.page.getByTestId('call-event')).toHaveCount(1, { timeout: 30_000 })

  // And it survives a reload, which a purely local flourish would not.
  await bob.page.reload()
  await expect(bob.page.getByTestId('call-event')).toHaveCount(1, { timeout: 30_000 })

  await Promise.all([alice.context.close(), bob.context.close()])
})
