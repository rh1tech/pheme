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
import { controlField, shouldAnswer } from './useHistorySync'

/** Encodes a control body the way the client posts it: base64 of JSON. */
function encodeControl(body: Record<string, unknown>): string {
  return btoa(JSON.stringify(body))
}

describe('shouldAnswer', () => {
  it('answers our own user\'s OTHER device — the regression', () => {
    // Both devices belong to one person. The old guard compared user ids and returned early here,
    // which is exactly how the new device was left unable to read anything.
    expect(shouldAnswer('device-b', 'device-a', true)).toBe(true)
  })

  it('ignores a request from this very device', () => {
    // We receive our own request off the stream. Handing ourselves our own history is a no-op at
    // best, and winning the election with it would starve a real responder.
    expect(shouldAnswer('device-a', 'device-a', true)).toBe(false)
  })

  it('does not answer without the group, since the seal is derived from it', () => {
    expect(shouldAnswer('device-b', 'device-a', false)).toBe(false)
  })

  it('ignores a malformed request that names nobody', () => {
    expect(shouldAnswer('', 'device-a', true)).toBe(false)
  })

  it('does not answer before this device has an identity', () => {
    expect(shouldAnswer('device-b', '', true)).toBe(false)
  })
})

describe('controlField', () => {
  it('reads the sender identity out of a request body', () => {
    expect(controlField(encodeControl({ id: 'device-b', epoch: 4 }), 'id')).toBe('device-b')
  })

  it('reads the addressee out of an offer body', () => {
    // Offers are recorded per addressee so an offer to one requester does not stand every
    // candidate down for a different one.
    expect(controlField(encodeControl({ to: 'device-b', historyId: 'blob-1' }), 'to')).toBe(
      'device-b',
    )
  })

  it('is empty when the field is absent, so no one is mistaken for the addressee', () => {
    expect(controlField(encodeControl({ id: 'device-b' }), 'to')).toBe('')
  })

  it('is empty on a non-string field rather than coercing it', () => {
    expect(controlField(encodeControl({ to: 42 }), 'to')).toBe('')
  })

  it('is empty on a body that is not valid base64 JSON', () => {
    expect(controlField('not-base64-@@@', 'id')).toBe('')
    expect(controlField(btoa('{ not json'), 'id')).toBe('')
  })
})
