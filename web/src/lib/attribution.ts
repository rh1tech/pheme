// Who actually wrote a message, and how well we know it.
//
// ------------------------------------------------------------------------------------------------
// THE PROBLEM THIS FILE EXISTS FOR
//
// A conversation message arrives with a `senderId` on its envelope. That field is written by the
// SERVER, which in MLS is the untrusted Delivery Service: it relays opaque bytes and can put any
// user id it likes on them. Rendering a message under that name means end-to-end encryption bought
// confidentiality and nothing else — nobody can read your conversation, and anybody who runs the
// server can write to it as you.
//
// MLS already knows the answer. Every application message is signed by the sending leaf's key,
// `process_message` verifies that signature against the ratchet tree, and the credential it hands
// back — `mimi://<domain>/d/<user>/<device>` — is authenticated. `Session.decrypt` now carries it
// out (see lib/mls.ts), and this file is where it is recorded, transferred and reduced to something
// a bubble can render.
// ------------------------------------------------------------------------------------------------
//
// A message can be attributed three ways, and they are NOT equally trustworthy:
//
//   * `mls` — this device decrypted the message itself and MLS authenticated the signer. The only
//     kind that is cryptographically ours. Also what a message WE sent gets: we wrote it.
//   * `relayed` — imported through the device-to-device history handoff. Another device of the same
//     account signed the whole transfer with its leaf key. The per-message author inside it is
//     still that device's word rather than a signature this device checked. Never presented as
//     verified.
//   * `legacy` — a cache entry written before any of this existed. There is no sender in it and
//     there never can be (the MLS key is long gone), so the envelope's `senderId` is all there is.
//     A compatibility fallback, explicitly unverified, never labelled otherwise.

import type { ChatContent } from './chatContent'
import { deserializeContent } from './chatContent'
import { userOf } from './roster'

/** The sender credential of a cached entry: `mimi://<domain>/d/<user>/<device>`. */
const SENDER_FIELD = '_s'
/** The credential of the member that handed this entry over, when it was imported rather than read. */
const RELAY_FIELD = '_r'

/** How a cached message's author was established. See the module docs. */
export type Attribution =
  | { kind: 'mls'; identity: string; userId: string }
  | { kind: 'relayed'; identity: string; userId: string; relayedBy: string }
  | { kind: 'legacy' }

/** The unattributed case, for entries that carry no sender. */
export const LEGACY: Attribution = { kind: 'legacy' }

/** Attribution for a message this device decrypted (or wrote) itself. */
export function authenticated(identity: string): Attribution {
  const userId = userOf(identity)
  if (!identity || !userId) return LEGACY
  return { kind: 'mls', identity, userId }
}

/**
 * The serialised cache entry for a message: its content plus who signed it.
 *
 * The sender rides INSIDE the entry rather than in a table beside it, and that is what makes the
 * history handoff and the key backup carry provenance for free — both copy these strings verbatim
 * (see chatCache.exportAllContents), so a device that imports a transcript imports the authorship
 * with it instead of re-deriving it from whatever the server says.
 *
 * `_s` and `_r` are extra fields on the same object, so an older build reading a newer cache still
 * finds `body`, `replyTo` and `photos` exactly where it expects them.
 */
export function encodeCacheEntry(content: ChatContent, attribution: Attribution): string {
  const out: Record<string, unknown> = { body: content.body }
  if (content.replyTo) out.replyTo = content.replyTo
  if (content.photos?.length) out.photos = content.photos
  if (attribution.kind === 'mls' || attribution.kind === 'relayed') {
    out[SENDER_FIELD] = attribution.identity
  }
  if (attribution.kind === 'relayed') out[RELAY_FIELD] = attribution.relayedBy
  return JSON.stringify(out)
}

/** A cache entry read back: its content, and how far its authorship can be trusted. */
export interface CachedEntry {
  content: ChatContent
  attribution: Attribution
}

/**
 * Parses a serialised cache entry. Returns null only when there is no content at all.
 *
 * An entry with no `_s` is LEGACY, not an error: every message anybody decrypted before this
 * existed is one, and they are the only copy of that plaintext there will ever be.
 */
export function decodeCacheEntry(serialised: string): CachedEntry | null {
  const bytes = new TextEncoder().encode(serialised)
  const content = deserializeContent(bytes)
  if (!content) {
    // A cached entry from a much older build is a bare body string rather than JSON. Keeping it
    // costs nothing and is the only copy of that message this device has.
    return { content: { body: serialised }, attribution: LEGACY }
  }

  let sender = ''
  let relayedBy = ''
  try {
    const raw = JSON.parse(serialised) as Record<string, unknown>
    if (typeof raw[SENDER_FIELD] === 'string') sender = raw[SENDER_FIELD]
    if (typeof raw[RELAY_FIELD] === 'string') relayedBy = raw[RELAY_FIELD]
  } catch {
    // Unreachable once deserializeContent succeeded, but a parse that throws must not cost the body.
  }

  const userId = userOf(sender)
  if (!sender || !userId) return { content, attribution: LEGACY }
  if (relayedBy) return { content, attribution: { kind: 'relayed', identity: sender, userId, relayedBy } }
  return { content, attribution: { kind: 'mls', identity: sender, userId } }
}

/**
 * Marks an entry as having arrived through the history handoff.
 *
 * Applied at IMPORT, over what the offering member sent, so an offerer cannot pass its transcript
 * off as something the receiving device authenticated for itself. An entry with no sender at all
 * stays legacy: there is nothing to relay a claim about.
 */
export function markRelayed(serialised: string, offerer: string): string {
  const entry = decodeCacheEntry(serialised)
  if (!entry || entry.attribution.kind === 'legacy' || !offerer) return serialised
  return encodeCacheEntry(entry.content, {
    kind: 'relayed',
    identity: entry.attribution.identity,
    userId: entry.attribution.userId,
    relayedBy: offerer,
  })
}

/** What a bubble needs to know about a message's author. */
export interface AuthorView {
  /**
   * The user id to render the message under. From MLS wherever there is an MLS answer; from the
   * envelope only for a legacy entry, which is the compatibility fallback and nothing more.
   */
  userId: string
  /** True only for `mls`: this device authenticated the signer itself. */
  verified: boolean
  /**
   * The envelope names a DIFFERENT user than the cryptography does.
   *
   * Not a rendering detail — it is the attack this whole file is about, caught. The UI must say so
   * rather than silently pick one of the two names.
   */
  tampered: boolean
}

/**
 * Reduces an attribution and the envelope's claim to what to show.
 *
 * `serverSenderId` is the envelope's `senderId`. It is never allowed to override an MLS answer; it
 * is used to render a legacy entry, and otherwise only to detect that it disagrees.
 */
export function resolveAuthor(attribution: Attribution, serverSenderId: string): AuthorView {
  if (attribution.kind === 'legacy') {
    return { userId: serverSenderId, verified: false, tampered: false }
  }
  const tampered = serverSenderId !== '' && serverSenderId !== attribution.userId
  return {
    userId: attribution.userId,
    verified: attribution.kind === 'mls' && !tampered,
    tampered,
  }
}

/**
 * Whether a message is our own.
 *
 * Decided by the AUTHENTICATED sender wherever there is one. Left to the envelope only for a legacy
 * entry and for a message this device could not read at all — in the second case there is no
 * plaintext, so there is no signature either, and the envelope is genuinely all that exists.
 */
export function isOwnMessage(
  attribution: Attribution | undefined,
  serverSenderId: string,
  myUserId: string,
): boolean {
  if (!myUserId) return false
  if (attribution && attribution.kind !== 'legacy') return attribution.userId === myUserId
  return serverSenderId === myUserId
}
