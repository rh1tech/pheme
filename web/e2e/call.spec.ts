import { expect, test, type Page } from '@playwright/test'
import { createUserViaAdmin, loginAsAdmin, uniqueEmail } from './helpers'
import { openChatAndJoin, signInOnNewDevice, startDirectChat } from './chat-helpers'

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
