import { expect, test, type Page } from '@playwright/test'
import { createUserViaAdmin, login, loginAsAdmin, uniqueEmail } from './helpers'

const PASSWORD = 'Sup3rSecret!'

/** The keys and cached plaintext this app stores locally, read from the page. */
async function localCryptoState(page: Page): Promise<{ keys: string[]; previews: number }> {
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
    .poll(async () => (await localCryptoState(page)).keys, {
      message: 'the MLS client state should be stored locally after signing in',
      timeout: 15_000,
    })
    .toContain('client-state')

  await page.getByRole('button', { name: 'Menu' }).click()
  await page.getByRole('menuitem', { name: 'Log out' }).click()

  // Logout navigates to /login and, on the way, wipes the key store.
  await expect(page).toHaveURL(/\/login/)
  await expect
    .poll(async () => (await localCryptoState(page)).keys.length, {
      message: 'no MLS key material may survive a logout',
      timeout: 15_000,
    })
    .toBe(0)

  const after = await localCryptoState(page)
  expect(after.previews, 'no decrypted message previews may survive a logout').toBe(0)
})
