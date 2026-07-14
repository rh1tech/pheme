// The golden vector for call signalling — the web half.
//
// The same vector is asserted in the Flutter client, in mobile/test/unit/call_envelope_test.dart. It
// was generated from THIS file, and it exists because the failure it guards against is silent: if the
// two clients ever disagree by a single byte about how the header is serialised, every call between a
// phone and a browser fails to connect, the ciphertext simply does not open, and there is nothing in
// any log to say why. Both ends behave exactly as designed.
//
// So the vector is pinned on both sides. Change the wire format here and the mobile test fails; change
// it there and this one does. That is the whole point of duplicating it.

import { describe, expect, it } from 'vitest'
import { openSignal, sealControl, sealSignal, type CallBody, type CallHeader } from './callEnvelope'
import { base64ToBytes } from './mls'

/** 00 01 02 … 1f */
const KEY = new Uint8Array(32).map((_, i) => i)

const HEADER: CallHeader = {
  v: 1,
  callId: 'b0a1c2d3-e4f5-4607-8899-aabbccddeeff',
  epoch: 7,
  from: 'user-1:device-1',
  seq: 3,
}

const BODY: CallBody = {
  kind: 'invite',
  sdp: 'v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\n',
}

/** The canonical AAD for HEADER — a fixed-order ARRAY, never the header object. */
const AAD_CANONICAL = '[1,"b0a1c2d3-e4f5-4607-8899-aabbccddeeff",7,"user-1:device-1",3,""]'

/** What WebCrypto seals BODY to under KEY and the fixed nonce a0..ab. */
const CIPHERTEXT_HEX =
  '9d3a17442baf2085400ce9a56e0ea5fc5c8e2a74e295784eea3316da0df71b6e' +
  'ef5b67ce8f13737411bc4d983d5ab2cb7035766652fe2b223302657cd940f428' +
  '76b49e9b4544b53ef73e3fed30'

const hex = (bytes: Uint8Array): string =>
  Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')

const unhex = (s: string): Uint8Array =>
  new Uint8Array((s.match(/../g) ?? []).map((b) => parseInt(b, 16)))

/**
 * The AAD, recomputed here rather than imported.
 *
 * headerAAD is deliberately not exported — nothing outside that file has any business building one.
 * Restating it here means a change to the real one does NOT silently change the test alongside it:
 * the two have to be made to agree, which is exactly the property a golden vector needs.
 */
function expectedAAD(h: CallHeader): Uint8Array {
  return new TextEncoder().encode(
    JSON.stringify([h.v, h.callId, h.epoch, h.from, h.seq, h.control ?? '']),
  )
}

describe('call envelope golden vector', () => {
  it('serialises the header as the fixed-order array both clients agree on', () => {
    expect(new TextDecoder().decode(expectedAAD(HEADER))).toBe(AAD_CANONICAL)
  })

  it('writes an absent control as "" and not as null', () => {
    // A null here would change the AAD, and every signal would stop opening.
    expect(AAD_CANONICAL.endsWith(',""]')).toBe(true)
  })

  it('seals to the ciphertext the mobile client expects', async () => {
    const nonce = new Uint8Array(12).map((_, i) => 0xa0 + i)
    const key = await crypto.subtle.importKey('raw', KEY, 'AES-GCM', false, ['encrypt'])
    const sealed = await crypto.subtle.encrypt(
      { name: 'AES-GCM', iv: nonce, additionalData: expectedAAD(HEADER) },
      key,
      new TextEncoder().encode(JSON.stringify(BODY)),
    )
    expect(hex(new Uint8Array(sealed))).toBe(CIPHERTEXT_HEX)
  })

  it('round-trips a signal through the real seal and open', async () => {
    const wire = await sealSignal(KEY, HEADER, BODY)
    await expect(openSignal(KEY, wire)).resolves.toEqual(BODY)
  })

  it('refuses a signal whose header was tampered with', async () => {
    const wire = await sealSignal(KEY, HEADER, BODY)

    // A server that rewrote the epoch — to force a downgrade to a key it had already seen — would need
    // this to succeed. It cannot: the header is bound into the ciphertext as additional data.
    const envelope = JSON.parse(new TextDecoder().decode(base64ToBytes(wire)))
    envelope.h.epoch = 6
    const tampered = btoa(JSON.stringify(envelope))

    await expect(openSignal(KEY, tampered)).rejects.toThrow()
  })

  // The only signal ever sent in the clear, and the one case the vector above does not touch.
  //
  // A device AHEAD of the sender cannot derive the sender's key at all — MLS exports only from the
  // current epoch — so it cannot reply in a sealed envelope to say so. This control is that reply. If
  // the two clients disagree about a byte of it, two ends that have fallen out of step can never tell
  // each other, and the call rings out with nothing anywhere to say why.
  it('serialises the epoch-mismatch control exactly as the mobile client does', () => {
    const control: CallHeader = {
      v: 1,
      callId: 'b0a1c2d3-e4f5-4607-8899-aabbccddeeff',
      epoch: 9,
      from: 'user-2:device-2',
      seq: 1,
      control: 'epoch-mismatch',
    }

    expect(new TextDecoder().decode(expectedAAD(control))).toBe(
      '[1,"b0a1c2d3-e4f5-4607-8899-aabbccddeeff",9,"user-2:device-2",1,"epoch-mismatch"]',
    )

    const wire = sealControl(control)
    expect(new TextDecoder().decode(base64ToBytes(wire))).toBe(
      '{"h":{"v":1,"callId":"b0a1c2d3-e4f5-4607-8899-aabbccddeeff","epoch":9,' +
        '"from":"user-2:device-2","seq":1,"control":"epoch-mismatch"}}',
    )

    // Nothing sealed: there is nothing secret in "I am at epoch N".
    const envelope = JSON.parse(new TextDecoder().decode(base64ToBytes(wire)))
    expect(envelope.n).toBeUndefined()
    expect(envelope.c).toBeUndefined()
  })

  it('uses a fresh nonce for every signal', async () => {
    // Never a counter: every device in the conversation can derive every other device's key, so two of
    // them counting from zero would eventually collide — and an AES-GCM nonce collision leaks the
    // authentication key itself, not merely the two plaintexts.
    const nonces = new Set<string>()
    for (let i = 0; i < 50; i++) {
      const wire = await sealSignal(KEY, HEADER, BODY)
      const envelope = JSON.parse(new TextDecoder().decode(base64ToBytes(wire)))
      expect(nonces.has(envelope.n)).toBe(false)
      nonces.add(envelope.n)
    }
  })
})
