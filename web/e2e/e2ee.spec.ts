import { expect, test, type Page } from '@playwright/test'
import { API_URL } from './constants'
import { createUserViaAdmin, login, loginAsAdmin, uniqueEmail } from './helpers'

const PASSWORD = 'Sup3rSecret!'

// These assert storage semantics (IndexedDB, Web Locks), not rendering. One engine is
// enough, and WebKit in CI is flaky enough that running them there only adds noise.
test.skip(({ browserName }) => browserName !== 'chromium', 'storage semantics: chromium only')

/**
 * The keys and cached plaintext this app stores locally, read from the page.
 *
 * Returns null while the page is navigating — logout redirects, and a read that lands
 * mid-navigation throws rather than telling us anything. Callers poll, so a null is
 * simply "ask again".
 */
async function localCryptoState(
  page: Page,
): Promise<{ keys: string[]; previews: number } | null> {
  try {
    return await readCryptoState(page)
  } catch {
    return null
  }
}

function readCryptoState(page: Page): Promise<{ keys: string[]; previews: number }> {
  return page.evaluate(async () => {
    const keys = await new Promise<string[]>((resolve) => {
      const req = indexedDB.open('pheme', 1)
      req.onsuccess = () => {
        const db = req.result
        if (!db.objectStoreNames.contains('mls')) return resolve([])
        const all = db.transaction('mls', 'readonly').objectStore('mls').getAllKeys()
        all.onsuccess = () => resolve(all.result.map(String))
        all.onerror = () => resolve([])
      }
      req.onerror = () => resolve([])
    })
    const previews = Object.keys(localStorage).filter((k) =>
      k.startsWith('pheme.chatPreview.'),
    ).length
    return { keys, previews }
  })
}

/**
 * Signing in must publish this device's KeyPackages.
 *
 * They are what someone else claims in order to add you to an encrypted group. With
 * none published, anyone trying to start a chat with you gets "no key package
 * available for that user" and simply cannot reach you. This has to happen on sign-in,
 * not when you first open a conversation — otherwise a new user is unreachable until
 * they happen to open one, which is backwards.
 */
test('signing in publishes key packages so others can start a chat with you', async ({ page }) => {
  const email = uniqueEmail('e2ee-keys')
  await loginAsAdmin(page)
  await createUserViaAdmin(page, email, PASSWORD)
  await login(page, email, PASSWORD)

  const stock = async () =>
    page.evaluate(async (apiBase: string) => {
      const accessToken = localStorage.getItem('pheme.accessToken') ?? ''
      const deviceId = localStorage.getItem('pheme.mlsDeviceId') ?? ''
      const res = await fetch(
        `${apiBase}/v1/mls/key-packages/count?deviceId=${encodeURIComponent(deviceId)}`,
        { headers: { Authorization: `Bearer ${accessToken}` } },
      )
      if (!res.ok) return { count: -1, hasLastResort: false }
      return (await res.json()) as { count: number; hasLastResort: boolean }
    }, API_URL)

  await expect
    .poll(async () => (await stock()).count, {
      message: 'signing in must publish single-use key packages',
      timeout: 20_000,
    })
    .toBeGreaterThan(0)

  // And the reusable one, without which a stranger could claim the stock in a loop and
  // leave this user permanently unreachable.
  expect((await stock()).hasLastResort, 'a last-resort key package must be published').toBe(true)
})

/**
 * Logging out must destroy this device's MLS private keys and every decrypted
 * message cached from them.
 *
 * They are exactly what the encryption exists to protect. Leaving them in IndexedDB
 * after sign-out means the next person to use the machine can read the previous
 * account's chats — which would make the whole feature a decoration. This regressed
 * once already (logout cleared only the auth tokens), so it is pinned here.
 */
test('logging out destroys the local encryption keys', async ({ page }) => {
  const email = uniqueEmail('e2ee')
  await loginAsAdmin(page)
  await createUserViaAdmin(page, email, PASSWORD)
  await login(page, email, PASSWORD)

  // Opening the chat surface creates this device's MLS identity and stores it.
  await expect
    .poll(async () => (await localCryptoState(page))?.keys ?? [], {
      message: 'the MLS client state should be stored locally after signing in',
      timeout: 15_000,
    })
    .toContain('client-state')

  await page.getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Log out' }).click()

  // Logout navigates to /login and, on the way, wipes the key store.
  await expect(page).toHaveURL(/\/login/)

  // Every trace of the identity and of any decrypted message must be gone. The one
  // thing that legitimately survives is `client-epoch`: it is a counter, not key
  // material, and clearing it would be a bug — a session still open in another tab
  // would see the epoch back at its own value, believe it was still live, and write
  // the keys we just destroyed straight back to disk.
  await expect
    .poll(
      async () => {
        const state = await localCryptoState(page)
        // Null while the redirect is in flight — not yet an answer, so keep asking.
        return state ? state.keys.filter((k) => k !== 'client-epoch') : ['pending']
      },
      { message: 'no MLS key material may survive a logout', timeout: 15_000 },
    )
    .toEqual([])

  const after = await localCryptoState(page)
  expect(after?.previews ?? 0, 'no decrypted message previews may survive a logout').toBe(0)
})

/**
 * Logging out in one tab must destroy the keys for EVERY tab.
 *
 * A second tab left open is ordinary — a forgotten background tab, a shared machine.
 * That tab still holds a live MLS client in memory with the private keys intact, and
 * it does not learn about the logout. If it is allowed to go on encrypting and
 * persisting, it writes the destroyed identity straight back to disk and the logout
 * was theatre. This is exactly the bug a review found in the first version of the
 * cross-tab guard, which used an in-memory counter that no other tab could see.
 */
test('logging out in one tab destroys the keys for another tab too', async ({ browser }) => {
  const email = uniqueEmail('e2ee-tabs')
  const context = await browser.newContext()
  const admin = await context.newPage()
  await loginAsAdmin(admin)
  await createUserViaAdmin(admin, email, PASSWORD)
  await admin.close()

  // Two tabs of the same account, sharing one origin — so one IndexedDB, one lock.
  const tabA = await context.newPage()
  await login(tabA, email, PASSWORD)
  const tabB = await context.newPage()
  await tabB.goto('/')
  await expect(tabB.getByTestId('chat-sidebar')).toBeVisible()

  await expect
    .poll(async () => (await localCryptoState(tabB))?.keys ?? [], { timeout: 15_000 })
    .toContain('client-state')

  // Tab A logs out. Tab B is never told.
  await tabA.getByRole('button', { name: 'Menu' }).click()
  await tabA.getByRole('menuitem', { name: 'Log out' }).click()
  await expect(tabA).toHaveURL(/\/login/)

  // Tab B's keys are gone from disk...
  await expect
    .poll(
      async () => {
        const state = await localCryptoState(tabB)
        return state ? state.keys.filter((k) => k !== 'client-epoch') : ['pending']
      },
      { message: 'the wipe must reach every tab, not just the one that logged out', timeout: 15_000 },
    )
    .toEqual([])

  // ...and the epoch was bumped, which is what refuses tab B's stale in-memory session
  // the right to write those keys back. The refusal itself (Session.assertLive) is not
  // reachable from here — driving a stale tab into an encrypt would need a live
  // conversation — so this pins the mechanism rather than the outcome. That gap is
  // deliberate and known.
  const epoch = await tabB.evaluate(async () => {
    return new Promise<number>((resolve) => {
      const req = indexedDB.open('pheme', 1)
      req.onsuccess = () => {
        const get = req.result
          .transaction('mls', 'readonly')
          .objectStore('mls')
          .get('client-epoch')
        get.onsuccess = () => {
          const v = get.result as Uint8Array | undefined
          resolve(v ? new DataView(v.buffer, v.byteOffset, 4).getUint32(0) : 0)
        }
        get.onerror = () => resolve(0)
      }
      req.onerror = () => resolve(0)
    })
  })
  expect(epoch, 'the wipe must advance the shared epoch that invalidates other tabs').toBeGreaterThan(0)

  await context.close()
})
