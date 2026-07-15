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

import { deserializeContent, serializeContent, type ChatContent } from './chatContent'
import { idbDelete, idbGet, idbSet } from './idb'

// One IndexedDB entry holds the whole per-conversation body map, which keeps
// reads and writes simple; conversations are not large enough to need per-message
// keys. Keyed distinctly from the MLS state (idb.ts STATE_KEY).
const cacheKey = (conversationId: string) => `bodies:${conversationId}`
const previewKey = (conversationId: string) => `pheme.chatPreview.${conversationId}`

const encoder = new TextEncoder()
const decoder = new TextDecoder()

/**
 * messageId -> the message's SERIALISED CONTENT, not just its body.
 *
 * The whole content, because a message is not only text: it may carry photos and a reply. Caching
 * just the body would mean a photo message came back as a bare caption the second time it was looked
 * at — and there is no second decrypt to recover the rest from.
 */
type ContentMap = Record<string, string>

async function loadMap(conversationId: string): Promise<ContentMap> {
  const bytes = await idbGet(cacheKey(conversationId))
  if (!bytes) return {}
  try {
    const raw: unknown = JSON.parse(decoder.decode(bytes))
    if (typeof raw !== 'object' || raw === null) return {}
    return raw as ContentMap
  } catch {
    return {}
  }
}

/** Everything this device has managed to read in a conversation, keyed by message id. */
export async function loadCachedContents(
  conversationId: string,
): Promise<Record<string, ChatContent>> {
  const map = await loadMap(conversationId)
  const out: Record<string, ChatContent> = {}

  for (const [id, serialised] of Object.entries(map)) {
    const content = deserializeContent(encoder.encode(serialised))
    // A cached entry from an older build is a bare body string rather than a JSON object. Reading it
    // as one keeps every message anybody has already decrypted, which is the only copy there is.
    out[id] = content ?? { body: serialised }
  }
  return out
}

/** Records a message's content, once, at decryption time. */
export async function cacheContent(
  conversationId: string,
  messageId: string,
  content: ChatContent,
): Promise<void> {
  const serialised = decoder.decode(serializeContent(content))

  const map = await loadMap(conversationId)
  if (map[messageId] === serialised) return

  map[messageId] = serialised
  await idbSet(cacheKey(conversationId), encoder.encode(JSON.stringify(map)))
}

/**
 * Drops one conversation's cached bodies — on delete, on clear-history, or when the
 * server reports it gone (404). The plaintext is destroyed here: MLS keys are
 * single-use, so a body forgotten from this cache can never be read again on this
 * device. That is the point — clearing history has to mean it.
 */
export async function forgetBodies(conversationId: string): Promise<void> {
  await idbDelete(cacheKey(conversationId))
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

/** Drops one conversation's cached preview — alongside its bodies and envelope. */
export function clearPreview(conversationId: string): void {
  try {
    localStorage.removeItem(previewKey(conversationId))
  } catch {
    // Storage unavailable: nothing cached to clear.
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
