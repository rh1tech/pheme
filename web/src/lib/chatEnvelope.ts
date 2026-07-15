// Local cache of a conversation's message envelope — the ordered list of message
// metadata (id, sender, timestamp, content type, ciphertext), oldest-first.
//
// The decrypted BODIES are cached separately (lib/chatCache); this stores the list
// they hang off. Together they let a chat render instantly from disk on open — the
// same transcript that was on screen last time — instead of a skeleton while the
// network round-trips. The server is still asked for the newest page and the two are
// reconciled, so the cache only ever races the network to first paint; it is never
// the sole source of truth.
//
// Lives in the same IndexedDB store as the bodies and the MLS state, so logout's
// single-transaction wipe (idb.ts idbClearExcept) takes it too.

import { idbDelete, idbGet, idbSet } from './idb'
import type { ChatMessage } from './types'

const envelopeKey = (conversationId: string) => `envelope:${conversationId}`

// A cap so a long-lived conversation cannot grow the cache without bound. Only the
// newest window is kept; older history still lives in the body cache and pages back
// in from the server on scroll. Comfortably larger than one page (PAGE_SIZE = 50).
const MAX_CACHED = 200

const encoder = new TextEncoder()
const decoder = new TextDecoder()

/** The cached ordered (oldest-first) envelope, or [] when nothing is cached. */
export async function loadEnvelope(conversationId: string): Promise<ChatMessage[]> {
  const bytes = await idbGet(envelopeKey(conversationId))
  if (!bytes) return []
  try {
    const raw: unknown = JSON.parse(decoder.decode(bytes))
    if (!Array.isArray(raw)) return []
    return raw as ChatMessage[]
  } catch {
    return []
  }
}

/**
 * Persists the newest window of an ordered (oldest-first) message list. Called
 * whenever the on-screen list settles — after load, pagination, a live arrival, or a
 * send — so the next open of this chat has the transcript to hand.
 */
export async function saveEnvelope(conversationId: string, messages: ChatMessage[]): Promise<void> {
  const window = messages.length > MAX_CACHED ? messages.slice(messages.length - MAX_CACHED) : messages
  await idbSet(envelopeKey(conversationId), encoder.encode(JSON.stringify(window)))
}

/** Drops one conversation's cached envelope — on delete, clear-history, or a 404. */
export async function forgetEnvelope(conversationId: string): Promise<void> {
  await idbDelete(envelopeKey(conversationId))
}
