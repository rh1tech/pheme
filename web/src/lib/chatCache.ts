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

import { type ChatContent } from './chatContent'
import {
  LEGACY,
  decodeCacheEntry,
  encodeCacheEntry,
  markRelayed,
  type Attribution,
  type CachedEntry,
} from './attribution'
import { idbDelete, idbGet, idbKeys, idbSet } from './idb'

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

/**
 * Everything this device has managed to read in a conversation, keyed by message id — each with the
 * attribution it was stored under.
 *
 * The attribution comes back WITH the body deliberately. Reading a cached message and then asking
 * the envelope who wrote it is exactly the hole this closes: the envelope is the server's word, and
 * the server is the untrusted Delivery Service. See lib/attribution.
 */
export async function loadCachedEntries(
  conversationId: string,
): Promise<Record<string, CachedEntry>> {
  const map = await loadMap(conversationId)
  const out: Record<string, CachedEntry> = {}

  for (const [id, serialised] of Object.entries(map)) {
    // A cached entry from an older build is a bare body string rather than a JSON object. Reading it
    // as one keeps every message anybody has already decrypted, which is the only copy there is.
    out[id] = decodeCacheEntry(serialised) ?? { content: { body: serialised }, attribution: LEGACY }
  }
  return out
}

/**
 * Every conversation's cached bodies, raw — the transcript half of the key backup.
 *
 * Raw (still-serialised) on purpose: this is a copy of the cache, not a reading of it, and
 * round-tripping through parse/serialise here could only lose information.
 */
/**
 * How many message bodies a transcript holds, across every conversation in it.
 *
 * The number the server's shrink guard compares one upload against another by — it cannot open the
 * seal, so this is all it has to tell a device that has read everything from one that has read
 * nothing. In one place because the count and the blob must describe the same thing: this client
 * sent no count at all, so every upload looked like a device holding nothing and was refused.
 */
export function countBodies(all: Record<string, ContentMap>): number {
  return Object.values(all).reduce((total, map) => total + Object.keys(map).length, 0)
}

export async function exportAllContents(): Promise<Record<string, ContentMap>> {
  const out: Record<string, ContentMap> = {}
  for (const key of await idbKeys()) {
    if (!key.startsWith('bodies:')) continue
    const conversationId = key.slice('bodies:'.length)
    const map = await loadMap(conversationId)
    if (Object.keys(map).length > 0) out[conversationId] = map
  }
  return out
}

/**
 * Imports transcripts — a restored device adopting what its predecessor had read, or a newly-joined
 * one adopting a same-account device's history handoff.
 *
 * Merged UNDER what this device already holds: anything decrypted here was decrypted more
 * recently than the backup was taken, so on a collision the local copy wins.
 *
 * `offerer` is the device that handed these over, and it is what tells the two cases apart. A
 * HISTORY HANDOFF comes from another device of this account, so every entry is marked as relayed:
 * that device signed the transfer, but the author inside each message is still its claim, not
 * something this device authenticated. A KEY BACKUP passes no offerer, because it is
 * this account's own earlier transcript — the attributions in it were made by a device of ours,
 * from decrypts it performed itself, and re-labelling them as relayed would be false.
 */
export async function importContents(
  all: Record<string, ContentMap>,
  offerer = '',
): Promise<void> {
  for (const [conversationId, imported] of Object.entries(all)) {
    if (typeof imported !== 'object' || imported === null) continue
    const existing = await loadMap(conversationId)
    // Stamped with WHO handed each entry over, before it is merged. An offerer signed the transfer
    // with its leaf key, so the claim is attributable to a real member — but it is that member's
    // word about who wrote each message, not something this device authenticated, and the two must
    // never become indistinguishable in the cache. See attribution.markRelayed.
    const stamped: ContentMap = {}
    for (const [id, serialised] of Object.entries(imported)) {
      if (typeof serialised !== 'string') continue
      stamped[id] = offerer ? markRelayed(serialised, offerer) : serialised
    }
    const merged = { ...stamped, ...existing }
    await idbSet(cacheKey(conversationId), encoder.encode(JSON.stringify(merged)))
  }
}

/**
 * Records a message's content, once, at decryption time, together with the sender MLS
 * authenticated.
 *
 * `attribution` is not optional and has no default, on purpose: every call site has an answer (the
 * decrypt's, or "we wrote it"), and a default would let a new one quietly store a message with no
 * author at all — which is indistinguishable, later, from a legacy entry.
 */
export async function cacheContent(
  conversationId: string,
  messageId: string,
  content: ChatContent,
  attribution: Attribution,
): Promise<void> {
  // The UNPADDED form. Padding exists to hide a message's length from anything
  // watching the wire; nothing here reaches the wire, and padding the cache would
  // cost up to a bucket per message on disk for no privacy at all.
  const serialised = encodeCacheEntry(content, attribution)

  const map = await loadMap(conversationId)
  if (map[messageId] === serialised) return

  map[messageId] = serialised
  await idbSet(cacheKey(conversationId), encoder.encode(JSON.stringify(map)))
}

/**
 * The stored body EXACTLY as it is serialised, or null when this device does not hold it.
 *
 * Raw on purpose: this is what the backup tail seals and what a restore replays, and both sides
 * have to agree byte for byte with what exportAllContents produces. Round-tripping through parse
 * and re-serialise here could only lose whatever a future content version carried.
 */
export async function rawBody(
  conversationId: string,
  messageId: string,
): Promise<string | null> {
  const map = await loadMap(conversationId)
  const raw = map[messageId]
  return typeof raw === 'string' ? raw : null
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

/**
 * The newest message a conversation row can show, and who MLS says wrote it.
 *
 * The sender is stored with the body because the chat list has the same attribution problem the
 * chat itself does — "is the newest message mine?" decides whether the row counts as unread — and
 * the list cannot decrypt anything to find out. It can only read what the open conversation wrote
 * here. `id` pins the answer to one message, so a stale preview cannot be read as an answer about
 * a newer one.
 */
export interface ChatPreview {
  body: string
  /** The message the body and sender belong to. Empty on an entry from before this was stored. */
  id: string
  /** Bare user id from the AUTHENTICATED MLS credential. Empty when unknown (legacy entry). */
  senderUserId: string
}

const EMPTY_PREVIEW: ChatPreview = { body: '', id: '', senderUserId: '' }

export function setPreview(
  conversationId: string,
  body: string,
  messageId = '',
  senderUserId = '',
): void {
  try {
    localStorage.setItem(
      previewKey(conversationId),
      JSON.stringify({ body, id: messageId, senderUserId }),
    )
  } catch {
    // Storage full/blocked: the list just shows the encrypted placeholder.
  }
}

/**
 * Reads a conversation's preview.
 *
 * Tolerates the OLD format, which was the bare body string: those entries are real previews of real
 * messages and throwing them away would blank every existing user's chat list. They come back with
 * no id and no sender, which every caller treats as "unknown, fall back".
 */
export function getPreview(conversationId: string): ChatPreview {
  try {
    const raw = localStorage.getItem(previewKey(conversationId))
    if (!raw) return EMPTY_PREVIEW
    if (!raw.startsWith('{')) return { body: raw, id: '', senderUserId: '' }
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) return EMPTY_PREVIEW
    const p = parsed as Record<string, unknown>
    return {
      body: typeof p.body === 'string' ? p.body : '',
      id: typeof p.id === 'string' ? p.id : '',
      senderUserId: typeof p.senderUserId === 'string' ? p.senderUserId : '',
    }
  } catch {
    return EMPTY_PREVIEW
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
