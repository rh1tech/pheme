// Who is allowed to hand a newly-joined device its history.
//
// This is the bug these tests exist for: a second device of the same person signed in, joined the
// group by external commit, and then showed EVERY message — its owner's and the other party's —
// as undecryptable, forever. MLS forward secrecy means a device that joins at epoch N holds no key
// for anything sealed before it, so the past can only arrive out of band: device-to-device history
// sync. That handoff never happened, because the responder stood down on any request carrying its
// own user id — reasoning that "a co-member of another user answers".
//
// In a 1:1 conversation that leaves exactly one permitted responder: the other participant. If they
// are offline, or on another host in a federated chat, nobody answers at all and the new device
// never gets its history. The person's OWN other device — same transcript, same person, far and
// away the most likely to be online at that moment — was the one candidate excluded.
//
// The identical mistake had already been found and fixed once in useDeviceAdmission; it came back
// in the commit that introduced history sync, where nothing covered it. So the rule is pinned here:
// answering is decided by DEVICE identity, never by user identity.

import { describe, expect, it } from 'vitest'
import { shouldAnswer } from './useHistorySync'
import { readHistoryOffer, readHistoryRequest } from '../lib/historyHandoff'

/** Encodes a control body the way the client posts it: base64 of JSON. */
function encodeControl(body: Record<string, unknown>): string {
  return btoa(JSON.stringify(body))
}

describe('shouldAnswer', () => {
  const phone = 'mimi://test.example/d/bob/phone'
  const laptop = 'mimi://test.example/d/bob/laptop'

  it('answers our own user\'s OTHER device — the regression', () => {
    expect(shouldAnswer(laptop, phone, true)).toBe(true)
  })

  it('ignores a request from this very device', () => {
    expect(shouldAnswer(phone, phone, true)).toBe(false)
  })

  it('refuses another participant even though they hold the group', () => {
    expect(shouldAnswer('mimi://test.example/d/alice/phone', phone, true)).toBe(false)
  })

  it('refuses the same bare user on another host', () => {
    expect(shouldAnswer('mimi://other.example/d/bob/laptop', phone, true)).toBe(false)
  })

  it('does not answer without the group, since the seal is derived from it', () => {
    expect(shouldAnswer(laptop, phone, false)).toBe(false)
  })

  it('ignores a malformed request that names nobody', () => {
    expect(shouldAnswer('', phone, true)).toBe(false)
  })

  it('does not answer before this device has an identity', () => {
    expect(shouldAnswer(laptop, '', true)).toBe(false)
  })
})

// The wire bodies the election reads. These used to be read with a "pull one field out of whatever
// JSON this is" helper, which by construction could not tell a SIGNED v2 body from an unsigned v1
// one — and answering a v1 request means sealing a whole conversation to a key derived for an
// identity that may never have asked for it.
describe('reading history control bodies off the wire', () => {
  const requester = 'mimi://test.example/d/user-b/device-b'
  const offerer = 'mimi://test.example/d/user-a/device-a'

  function request(overrides: Record<string, unknown> = {}): string {
    return encodeControl({
      v: 2,
      id: requester,
      epoch: 4,
      nonce: 'AAECAw==',
      sig: 'c2ln',
      ...overrides,
    })
  }

  function offer(overrides: Record<string, unknown> = {}): string {
    return encodeControl({
      v: 2,
      from: offerer,
      to: requester,
      epoch: 4,
      historyId: 'blob-1',
      salt: 'c2FsdA==',
      nonce: 'bm9uY2U=',
      reqNonce: 'AAECAw==',
      sig: 'c2ln',
      ...overrides,
    })
  }

  it('reads the requester identity out of a signed v2 request', () => {
    expect(readHistoryRequest(request())?.id).toBe(requester)
  })

  it('reads the addressee out of a signed v2 offer', () => {
    // Offers are recorded per addressee so an offer to one requester does not stand every
    // candidate down for a different one.
    expect(readHistoryOffer(offer())?.to).toBe(requester)
  })

  it('REFUSES an unsigned v1 request — there is no downgrade path', () => {
    expect(readHistoryRequest(encodeControl({ id: requester, epoch: 4 }))).toBeNull()
  })

  it('REFUSES an unsigned v1 offer', () => {
    expect(
      readHistoryOffer(encodeControl({ to: requester, epoch: 4, historyId: 'blob-1', salt: 'c2FsdA==', nonce: 'bm9uY2U=' })),
    ).toBeNull()
  })

  it('refuses a v2 body with the signature stripped out', () => {
    expect(readHistoryRequest(request({ sig: '' }))).toBeNull()
    expect(readHistoryOffer(offer({ sig: '' }))).toBeNull()
  })

  it('refuses an offer that names no offerer, since there is no leaf key to verify against', () => {
    expect(readHistoryOffer(offer({ from: '' }))).toBeNull()
  })

  it('refuses an identity that is not a resolvable credential', () => {
    // A legacy `user:device` leaf, or anything else that does not parse: there is no user to
    // compare against the envelope's authenticated poster.
    expect(readHistoryRequest(request({ id: 'user-b:device-b' }))).toBeNull()
    expect(readHistoryOffer(offer({ from: 'nonsense' }))).toBeNull()
  })

  it('refuses a future version rather than reading it as v2', () => {
    expect(readHistoryRequest(request({ v: 3 }))).toBeNull()
    expect(readHistoryOffer(offer({ v: 3 }))).toBeNull()
  })

  it('is null on a non-string field rather than coercing it', () => {
    expect(readHistoryOffer(offer({ to: 42 }))).toBeNull()
  })

  it('is null on a body that is not valid base64 JSON', () => {
    expect(readHistoryRequest('not-base64-@@@')).toBeNull()
    expect(readHistoryRequest(btoa('{ not json'))).toBeNull()
  })
})
