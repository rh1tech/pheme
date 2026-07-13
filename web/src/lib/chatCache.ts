// Local plaintext cache for decrypted conversation messages.
//
// MLS gives forward secrecy: an application message can be decrypted exactly
// ONCE — the key is deleted afterwards. So message history cannot be recovered by
// re-decrypting on reload; the decrypted body must be cached the first (and only)
// time it is read. History then renders from this cache, never by decrypting
// again.
//
// The cache holds already-decrypted plaintext on the user's own device. It is not
// a weakening of E2EE: the plaintext is only ever on the devices that legitimately
// hold it, exactly as the messages themselves are.

import { idbGet, idbSet } from './idb'

// One IndexedDB entry holds the whole per-conversation body map, which keeps
// reads and writes simple; conversations are not large enough to need per-message
// keys. Keyed distinctly from the MLS state (idb.ts STATE_KEY).
const cacheKey = (conversationId: string) => `bodies:${conversationId}`
const previewKey = (conversationId: string) => `pheme.chatPreview.${conversationId}`

const encoder = new TextEncoder()
const decoder = new TextDecoder()

type BodyMap = Record<string, string>

async function loadMap(conversationId: string): Promise<BodyMap> {
  const bytes = await idbGet(cacheKey(conversationId))
  if (!bytes) return {}
  try {
    return JSON.parse(decoder.decode(bytes)) as BodyMap
  } catch {
    return {}
  }
}

/** The decrypted bodies of a conversation's messages, keyed by message id. */
export async function loadCachedBodies(conversationId: string): Promise<BodyMap> {
  return loadMap(conversationId)
}

/** Records the decrypted body of a message (once, at decryption time). */
export async function cacheBody(
  conversationId: string,
  messageId: string,
  body: string,
): Promise<void> {
  const map = await loadMap(conversationId)
  if (map[messageId] === body) return
  map[messageId] = body
  await idbSet(cacheKey(conversationId), encoder.encode(JSON.stringify(map)))
}

// The sidebar preview is stored separately in localStorage: it is tiny, read
// synchronously while rendering the list, and updated whenever a newer message is
// decrypted. Without it the list could not show a preview at all, since the last
// message cannot be re-decrypted.
export function setPreview(conversationId: string, body: string): void {
  try {
    localStorage.setItem(previewKey(conversationId), body)
  } catch {
    // Storage full/blocked: the list just shows the encrypted placeholder.
  }
}

export function getPreview(conversationId: string): string {
  try {
    return localStorage.getItem(previewKey(conversationId)) ?? ''
  } catch {
    return ''
  }
}

/** Drops every cached preview. Called on logout with the rest of the plaintext. */
export function clearPreviews(): void {
  try {
    const stale = Object.keys(localStorage).filter((k) => k.startsWith('pheme.chatPreview.'))
    for (const key of stale) localStorage.removeItem(key)
  } catch {
    // Storage unavailable: nothing cached to clear.
  }
}
