// Decrypting a message inside the service worker, to show it on a notification.
//
// Loaded by sw.js via importScripts, and deliberately NOT part of the app bundle: a service worker
// is a classic script here (module workers are still not universally supported, and a notification
// that fails to render in one browser is worse than one that renders plainly in all of them).
//
// ------------------------------------------------------------------------------------------------
// THE RULE THIS FILE EXISTS TO OBEY: IT NEVER WRITES.
//
// The worker is a SECOND context holding the same MLS key store as the page, and the single-client
// rule says there must never be two writers — two ratchets advancing independently and racing to
// disk would leave one of them saved over the other, which is every message after that point
// permanently unreadable.
//
// So this reads a SNAPSHOT of the state, decrypts inside it, shows the message, and throws the
// whole thing away. The page's own state is untouched and still holds an unconsumed key for that
// message, so it decrypts it again for real when the user opens the app. "A message decrypts
// exactly once" is a property of a copy of the state, not a global fact.
//
// The guarantee is structural, not a promise made here: MlsPreviewClient has no exportState, so
// there is nowhere for the advanced ratchet to go. See crates/pheme-mls/src/lib.rs.
// ------------------------------------------------------------------------------------------------

/* global wasm_bindgen */

const WASM_GLUE = '/mls/pheme_mls_nomodules.js'
const WASM_BINARY = '/mls/pheme_mls_bg.wasm'

const DB_NAME = 'pheme'
const DB_STORE = 'mls'
const STATE_KEY = 'client-state'
const GROUPS_KEY = 'group-ids'

// The wasm-bindgen GLUE, imported at top level for the same reason sw.js imports this file at top
// level: a service worker may only importScripts a URL already in its script resource map, and a
// URL only gets there during initial evaluation or install. Called later — from a push handler —
// it throws NetworkError. See Service Workers spec §6.3.2.
//
// 43 KB of JavaScript, which is the cost of being correct here. The 1.2 MB BINARY stays lazy: it
// is fetched by wasm_bindgen() below, and fetch() carries no such restriction.
importScripts(WASM_GLUE)

let wasmReady = null

// Instantiates the MLS WASM once per worker lifetime. The worker is short-lived and restarted
// freely, so this may run again on the next push; that costs a fetch the browser has cached.
function ensureWasm() {
  if (!wasmReady) {
    // The glue normally infers the binary's URL from document.currentScript, which does not exist
    // in a worker — so it is passed explicitly. Same binary the app uses: the two wasm-pack
    // targets differ only in their JavaScript glue, so there is one .wasm and no chance of the
    // worker running a different build of the crypto than the page.
    wasmReady = wasm_bindgen({ module_or_path: WASM_BINARY }).catch((e) => {
      wasmReady = null // let a later push retry rather than being poisoned by one bad load
      throw e
    })
  }
  return wasmReady
}

// Reads keys from the app's IndexedDB. Read-only, and the only contact this file has with storage.
function idbGetMany(keys) {
  return new Promise((resolve, reject) => {
    // Never onupgradeneeded: opening without a version cannot create or migrate anything. If the
    // database does not exist yet the app has never run here, and there is nothing to decrypt.
    const req = indexedDB.open(DB_NAME)
    req.onerror = () => reject(req.error)
    req.onsuccess = () => {
      const db = req.result
      if (!db.objectStoreNames.contains(DB_STORE)) {
        db.close()
        resolve([])
        return
      }
      const tx = db.transaction(DB_STORE, 'readonly')
      const store = tx.objectStore(DB_STORE)
      Promise.all(
        keys.map(
          (key) =>
            new Promise((res) => {
              const get = store.get(key)
              get.onsuccess = () => res(get.result)
              get.onerror = () => res(undefined)
            }),
        ),
      ).then((values) => {
        db.close()
        resolve(values)
      }, reject)
    }
  })
}

// The group ids this conversation might be readable under.
//
// A conversation can have more than one: a group is rebuilt when it is retired, and older messages
// still decrypt under the older id. The app keeps the map; this only reads it.
function groupsFor(rawGroups, conversationId) {
  try {
    const map = JSON.parse(new TextDecoder().decode(rawGroups))
    const ids = map?.[conversationId]
    if (Array.isArray(ids)) return ids.filter((id) => typeof id === 'string' && id)
    return typeof ids === 'string' && ids ? [ids] : []
  } catch {
    return []
  }
}

function base64ToBytes(b64) {
  const binary = atob(b64)
  const out = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i)
  return out
}

// The body text out of a decrypted message, or '' if there is none to show.
//
// Mirrors deserializeContent in lib/chatContent.ts. Only `body` is read: a preview is one line on a
// lock screen, and photos and reply references are not it. A photo with no caption yields '' and the
// caller falls back to the generic text, which is the right answer — "Photo" would be a nicer
// notification, but it is also a claim about content, and this file should make as few of those as
// it can get away with.
function bodyOf(bytes) {
  try {
    const parsed = JSON.parse(new TextDecoder().decode(bytes))
    return typeof parsed?.body === 'string' ? parsed.body : ''
  } catch {
    return ''
  }
}

/**
 * Decrypts a push's ciphertext for display, or returns null.
 *
 * Null is an ordinary outcome, not an error: no key material in this browser, a message for a group
 * this device cannot read, control traffic, a state blob written by a newer build. Every one of
 * those falls back to the server's generic body, which is why nothing here throws.
 *
 * @param {string} conversationId
 * @param {string} ciphertextBase64
 * @returns {Promise<string|null>} the message text, or null if it could not be read
 */
async function decryptPreview(conversationId, ciphertextBase64) {
  try {
    const [state, rawGroups] = await idbGetMany([STATE_KEY, GROUPS_KEY])
    if (!state || !rawGroups) return null

    const groups = groupsFor(rawGroups, conversationId)
    if (groups.length === 0) return null

    await ensureWasm()

    // Note the two keys are read in one transaction but are written by the app independently, so a
    // push arriving mid-write could see a fresh state and a stale group map. The consequence is a
    // lookup that finds no group and a preview that does not render — the generic notification still
    // does. Nothing is corrupted, because nothing here writes.
    const client = wasm_bindgen.MlsPreviewClient.fromState(new Uint8Array(state))
    try {
      const ciphertext = base64ToBytes(ciphertextBase64)
      const encoder = new TextEncoder()
      for (const groupId of groups) {
        const gid = encoder.encode(groupId)
        if (!client.hasGroup(gid)) continue
        let plaintext
        try {
          plaintext = client.decrypt(gid, ciphertext)
        } catch {
          continue // wrong group of the candidates, or undecryptable here; try the next
        }
        if (!plaintext) continue // control traffic: nothing to preview
        const body = bodyOf(plaintext)
        if (body) return body
      }
      return null
    } finally {
      // Free the WASM object promptly: it holds a full copy of the key store, and plaintext.
      client.free()
    }
  } catch {
    return null
  }
}

self.phemeDecryptPreview = decryptPreview
