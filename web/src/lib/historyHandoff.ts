// The wire bodies of the device-to-device history handoff, and the rules for refusing one.
//
// ------------------------------------------------------------------------------------------------
// WHY THIS IS NOT JUST JSON
//
// A device that joins a conversation holds none of what was said before it arrived. Another device
// of the same account hands the transcript over: sealed under the group's exporter secret, uploaded
// as an opaque blob, pointed at by a control message.
//
// The seal proves the sender is A MEMBER of the group. It cannot prove WHICH member, because every
// member derives the same exporter secret — that is what makes it usable at all. So under
// exporter-AEAD alone any member can:
//
//   * mint a request that CLAIMS to come from a device that never asked, and so make a co-member
//     seal a whole conversation to a key derived for somebody else's identity;
//   * mint an offer that claims to come from another member, stuffed with a transcript of their own
//     invention — every message in a conversation, attributed to whoever they like, landing on a
//     fresh device that has nothing to compare it against.
//
// v2 authenticates the signer with a SENDER-authenticated layer over the same bytes: the member signs a
// canonical transcript with the same MLS leaf signature key the group already authenticates it by,
// and the receiver verifies that signature against the leaf key the ratchet tree holds for the
// identity being claimed (see crates/pheme-mls/src/history.rs — the transcript is built there, in
// Rust, so the browser and the phone canonicalise identically by construction rather than by two
// hand-written encoders kept in step by hope).
//
// v1 bodies carried no signature. They are REFUSED, not tolerated: accepting one would leave the
// forgery above wide open, and a fallback that quietly downgrades is the same as no signature at
// all. A valid member can still sign invented history as THEMSELVES, so the orchestration accepts a
// provider only when its domain-qualified account matches the requester. A device that gets no
// answer simply re-asks.
// ------------------------------------------------------------------------------------------------
//
// Everything here is pure — no session, no WASM, no network — so the rules that decide whether a
// transcript may be imported are testable on their own. The signature check itself lives in the
// crate; this decides everything around it.

import { domainOf, userOf } from './roster'

/** The wire version these bodies belong to. v1 was unsigned and is refused. */
export const HISTORY_VERSION = 2

/** "I hold none of this conversation's past — can someone who does send it?" */
export interface HistoryRequestBody {
  v: number
  /** The requesting device's MLS credential identity. */
  id: string
  /** The epoch the requester was at, so the offerer derives the matching exporter secret. */
  epoch: number
  /** base64. Fresh per request; quoted back by the offer, which ties the answer to the question. */
  nonce: string
  /** base64 signature over the canonical request transcript, by the requester's MLS leaf key. */
  sig: string
}

/** "Your history is sealed and waiting at this id." */
export interface HistoryOfferBody {
  v: number
  /** The OFFERING device's MLS credential identity — whose leaf key `sig` must verify against. */
  from: string
  /** The requesting device's MLS credential identity. An offer is for exactly one device. */
  to: string
  epoch: number
  historyId: string
  /** base64 AEAD salt. */
  salt: string
  /** base64 AEAD nonce. */
  nonce: string
  /** base64 — the nonce from the request this answers. */
  reqNonce: string
  /** base64 signature over the canonical offer transcript, by the offerer's MLS leaf key. */
  sig: string
}

function str(raw: Record<string, unknown>, field: string): string {
  const value = raw[field]
  return typeof value === 'string' ? value : ''
}

/**
 * Parses a request body, or null if it is not a well-formed **v2** one.
 *
 * A v1 body — no `v`, no `sig` — parses to null here and is therefore never answered. That is the
 * point: answering it means sealing a transcript to a key derived for an identity that may never
 * have asked for it.
 */
export function parseRequestBody(json: string): HistoryRequestBody | null {
  let raw: Record<string, unknown>
  try {
    const parsed: unknown = JSON.parse(json)
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return null
    raw = parsed as Record<string, unknown>
  } catch {
    return null
  }
  if (raw.v !== HISTORY_VERSION) return null
  const id = str(raw, 'id')
  const nonce = str(raw, 'nonce')
  const sig = str(raw, 'sig')
  const epoch = typeof raw.epoch === 'number' ? raw.epoch : -1
  if (!id || !nonce || !sig || epoch < 0) return null
  if (!userOf(id)) return null // not a credential we can resolve to a user; nothing to verify against
  return { v: HISTORY_VERSION, id, epoch, nonce, sig }
}

/** Parses an offer body, or null if it is not a well-formed **v2** one. */
export function parseOfferBody(json: string): HistoryOfferBody | null {
  let raw: Record<string, unknown>
  try {
    const parsed: unknown = JSON.parse(json)
    if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return null
    raw = parsed as Record<string, unknown>
  } catch {
    return null
  }
  if (raw.v !== HISTORY_VERSION) return null
  const from = str(raw, 'from')
  const to = str(raw, 'to')
  const historyId = str(raw, 'historyId')
  const salt = str(raw, 'salt')
  const nonce = str(raw, 'nonce')
  const reqNonce = str(raw, 'reqNonce')
  const sig = str(raw, 'sig')
  const epoch = typeof raw.epoch === 'number' ? raw.epoch : -1
  if (!from || !to || !historyId || !salt || !nonce || !reqNonce || !sig || epoch < 0) return null
  if (!userOf(from) || !userOf(to)) return null
  return { v: HISTORY_VERSION, from, to, epoch, historyId, salt, nonce, reqNonce, sig }
}

/**
 * Whether the identity a control body CLAIMS matches the poster the server authenticated.
 *
 * The server is untrusted for message CONTENT, but it does authenticate the session that posted a
 * message and stamps its user id on the envelope. That makes the envelope a second, independent
 * witness: an insider forging a body in somebody else's name has to get past the MLS signature AND
 * post it from that person's account. Checking both costs one comparison.
 *
 * A blank `posterId` — an older server, or a listing that does not carry one — is not a failure.
 * The MLS signature is the check that must hold; this one strengthens it where it is available.
 */
export function posterMatchesClaim(claimedIdentity: string, posterId: string): boolean {
  if (!posterId) return true
  const claimed = userOf(claimedIdentity)
  return claimed !== '' && claimed === posterId
}

/**
 * Whether two MLS device credentials belong to the same canonical account.
 *
 * A valid leaf signature proves which GROUP MEMBER signed a handoff, but every member owns a valid
 * leaf and could sign an invented transcript as themselves. History is therefore accepted only
 * from another device of the requester's own account. Domain and user must both match; bare user
 * ids are host-local and are not sufficient in a federated conversation.
 */
export function sameAccount(left: string, right: string): boolean {
  const leftUser = userOf(left)
  const rightUser = userOf(right)
  const leftDomain = domainOf(left)
  const rightDomain = domainOf(right)
  return (
    leftUser !== '' &&
    leftDomain !== '' &&
    leftUser === rightUser &&
    leftDomain === rightDomain
  )
}

/**
 * The JSON inside a control message's base64 body. Empty string on anything that is not base64,
 * which every caller treats as an unreadable body.
 */
function controlJson(ciphertextBase64: string): string {
  try {
    const binary = atob(ciphertextBase64)
    const bytes = new Uint8Array(binary.length)
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
    return new TextDecoder().decode(bytes)
  } catch {
    return ''
  }
}

/** Reads a history REQUEST off the wire. Null for anything that is not a well-formed **v2** body. */
export function readHistoryRequest(ciphertextBase64: string): HistoryRequestBody | null {
  return parseRequestBody(controlJson(ciphertextBase64))
}

/** Reads a history OFFER off the wire. Null for anything that is not a well-formed **v2** body. */
export function readHistoryOffer(ciphertextBase64: string): HistoryOfferBody | null {
  return parseOfferBody(controlJson(ciphertextBase64))
}
