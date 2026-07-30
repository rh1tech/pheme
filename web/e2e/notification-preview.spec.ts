import { expect, test } from '@playwright/test'
import { WEB_URL } from './constants'

// The service worker decrypting a push, which is the one part of message previews that nothing
// else can cover.
//
// It shipped broken and stayed broken through three releases, and every layer around it was
// green the whole time: the Rust decrypt had tests, the WASM binding had tests, the server's
// payload had tests. The failure lived in the gap between them — the worker loaded preview.js
// with importScripts() from inside the push handler, and a service worker may only importScripts
// a URL already in its script resource map (Service Workers §6.3.2). It threw NetworkError, the
// handler caught it, and every preview quietly became "New message" with nothing anywhere to say
// why. Nobody could see it without a real worker.
//
// So this drives a REAL one: registers it, seeds IndexedDB with genuine MLS state, and calls the
// worker's own decrypt function inside the worker. No stubs — a stub would have passed throughout.
//
// It needs no API and no login, which is why it is quick despite living in the E2E suite.

// The wasm-pack "no-modules" glue declares `let wasm_bindgen`, which is a SCRIPT-scoped binding
// rather than a property of globalThis — reading it off globalThis gets undefined. Typed here so
// the bare identifier resolves the way it does at runtime, and so this file needs no `any`.
interface MlsClientHandle {
  createGroup(groupId: Uint8Array): void
  stageAdd(groupId: Uint8Array, keyPackages: Uint8Array[]): { welcome: Uint8Array }
  commitAccepted(groupId: Uint8Array): void
  joinFromWelcome(welcome: Uint8Array): void
  keyPackage(): Uint8Array
  encrypt(groupId: Uint8Array, plaintext: Uint8Array): Uint8Array
  exportState(): Uint8Array
}
interface MlsBindgen {
  (options: { module_or_path: string }): Promise<unknown>
  MlsClient: new (userId: string, deviceId: string) => MlsClientHandle
}
declare const wasm_bindgen: MlsBindgen

const GROUP_ID = 'grp-preview-spec'
const CONVERSATION_ID = 'conv-preview-spec'
const MESSAGE = 'decrypted in the service worker'

test('the service worker decrypts a push into the notification body', async ({ page, context, browserName }) => {
  // Chromium only, because Playwright exposes service workers on Chromium ALONE: context
  // .serviceWorkers() is empty everywhere else and waitForEvent('serviceworker') never fires. Under
  // WebKit this failed on every run, on every retry, from the day it was written.
  //
  // It was called flake twice before anyone read the error. It was not flake — it was a test that
  // could not pass, and the retries dutifully proved it three times each run. Skipping here is not
  // papering over a real failure; it is saying out loud which browser the test can observe.
  //
  // What that leaves uncovered is real: Safari is where these previews matter most, and no test
  // here can see its worker. That gap belongs to on-device testing.
  test.skip(browserName !== 'chromium', 'Playwright exposes service workers on Chromium only')

  await page.goto(WEB_URL, { waitUntil: 'domcontentloaded' })

  // Build a real group and a real ciphertext with the same WASM the worker loads, and store the
  // recipient's state exactly where the app keeps it.
  const fixture = await page.evaluate(
    async ([groupId, conversationId, body]) => {
      await new Promise<void>((resolve, reject) => {
        const s = document.createElement('script')
        s.src = '/mls/pheme_mls_nomodules.js'
        s.onload = () => resolve()
        s.onerror = () => reject(new Error('wasm glue failed to load'))
        document.head.appendChild(s)
      })
      await wasm_bindgen({ module_or_path: '/mls/pheme_mls_bg.wasm' })

      const enc = new TextEncoder()
      // MlsClient takes (domain, userId, deviceId). The domain arrived with federation, and this
      // fixture was left on the old two-argument call — so the first argument was read as the
      // domain, the second as the user, and the device id was missing, which the constructor
      // rejects outright. It failed on every run and every retry, which reads like flake and was
      // not. The domain only shapes the identity string here: the worker restores bob wholesale
      // from exportState(), so it never reconstructs one.
      const alice = new wasm_bindgen.MlsClient('pheme.test', 'alice', 'dev-a')
      const bob = new wasm_bindgen.MlsClient('pheme.test', 'bob', 'dev-b')
      alice.createGroup(enc.encode(groupId))
      const staged = alice.stageAdd(enc.encode(groupId), [bob.keyPackage()])
      alice.commitAccepted(enc.encode(groupId))
      bob.joinFromWelcome(staged.welcome)

      const ciphertext = alice.encrypt(enc.encode(groupId), enc.encode(JSON.stringify({ body })))
      const state = bob.exportState()

      await new Promise<void>((resolve, reject) => {
        const open = indexedDB.open('pheme', 1)
        open.onupgradeneeded = () => open.result.createObjectStore('mls')
        open.onsuccess = () => {
          const db = open.result
          const tx = db.transaction('mls', 'readwrite')
          tx.objectStore('mls').put(state, 'client-state')
          tx.objectStore('mls').put(
            enc.encode(JSON.stringify({ [conversationId]: [groupId] })),
            'group-ids',
          )
          tx.oncomplete = () => {
            db.close()
            resolve()
          }
          tx.onerror = () => reject(tx.error)
        }
        open.onerror = () => reject(open.error)
      })

      let binary = ''
      for (const byte of ciphertext) binary += String.fromCharCode(byte)
      return btoa(binary)
    },
    [GROUP_ID, CONVERSATION_ID, MESSAGE] as const,
  )

  await page.evaluate(() => navigator.serviceWorker.register('/sw.js'))
  await page.evaluate(() => navigator.serviceWorker.ready.then(() => undefined))

  let worker = context.serviceWorkers()[0]
  if (!worker) worker = await context.waitForEvent('serviceworker', { timeout: 20_000 })

  // The regression that actually happened: the worker evaluated, but preview.js never loaded, so
  // this was undefined and every push fell back to the generic body.
  await expect
    .poll(() => worker.evaluate(() => typeof (self as never as Record<string, unknown>).phemeDecryptPreview), {
      timeout: 15_000,
    })
    .toBe('function')

  const decrypted = await worker.evaluate(
    ([conversationId, ciphertext]) =>
      (
        self as never as {
          phemeDecryptPreview: (
            c: string,
            ct: string,
          ) => Promise<{ body: string; senderUserId: string } | null>
        }
      ).phemeDecryptPreview(conversationId, ciphertext),
    [CONVERSATION_ID, fixture] as const,
  )

  expect(decrypted?.body).toBe(MESSAGE)
  // And the worker knows WHO signed it, from the credential MLS authenticated — not from the push
  // payload, which the untrusted server composes.
  expect(decrypted?.senderUserId).toBe('alice')
})
