// The signed history handoff, exercised against the REAL WASM the browser runs.
//
// The property under test is the one the exporter secret cannot give: WHICH member is asking, and
// WHICH member is answering. Every member of a group derives the same exporter secret — that is
// what makes it usable for a group-wide seal at all — so AEAD alone proves only "a member". Under
// it, any member can mint a request in another member's name and any member can mint an offer in
// another member's name, stuffed with a transcript of their own invention.
//
// v2 signs a canonical, domain-separated transcript with the member's MLS leaf key and verifies it
// against the leaf key the group's own ratchet tree holds for the claimed identity. These tests
// drive that through the exact wasm-bindgen surface web/src/lib/mls.ts calls, because a binding
// that dropped an argument or reordered two of them would pass every Rust test and still verify
// signatures over the wrong claim in a browser.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { beforeAll, describe, expect, it } from 'vitest'
import init, { MlsClient } from '../src/crypto/pkg/pheme_mls.js'
import {
  HISTORY_VERSION,
  parseOfferBody,
  parseRequestBody,
  posterMatchesClaim,
  readHistoryOffer,
  readHistoryRequest,
} from '../src/lib/historyHandoff'

const GROUP = 'grp-history-test'
const CONV = 'conv-history-test'
const enc = new TextEncoder()
const gid = () => enc.encode(GROUP)

const ALICE = 'mimi://test.example/d/alice/dev-a'
const BOB = 'mimi://test.example/d/bob/dev-b'

beforeAll(async () => {
  const wasm = readFileSync(fileURLToPath(new URL('../src/crypto/pkg/pheme_mls_bg.wasm', import.meta.url)))
  await init({ module_or_path: wasm })
})

/** Alice's group with Bob and Carol in it, all three holding the same ratchet tree. */
function establishTrio(): { alice: MlsClient; bob: MlsClient; carol: MlsClient } {
  const alice = new MlsClient('test.example', 'alice', 'dev-a')
  const bob = new MlsClient('test.example', 'bob', 'dev-b')
  const carol = new MlsClient('test.example', 'carol', 'dev-c')
  alice.createGroup(gid())
  const staged = alice.stageAdd(gid(), [bob.keyPackage(), carol.keyPackage()])
  alice.commitAccepted(gid())
  bob.joinFromWelcome(staged.welcome)
  carol.joinFromWelcome(staged.welcome)
  return { alice, bob, carol }
}

const NONCE = new Uint8Array([1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16])
const SALT = new Uint8Array([0xaa, 0xbb])
const AEAD_NONCE = new Uint8Array([0xcc, 0xdd])
const SEALED = enc.encode('a sealed transcript of everything that was said')

describe('signed history requests', () => {
  it('verifies a request against the requester\'s own leaf key', () => {
    const { alice, bob } = establishTrio()
    const sig = bob.signHistoryRequest(gid(), CONV, 1n, NONCE)
    // Alice, answering, checks it against the leaf key HER copy of the tree holds for Bob.
    expect(() => alice.verifyHistoryRequest(gid(), CONV, 1n, BOB, NONCE, sig)).not.toThrow()
  })

  // The forgery the signature exists to stop: Carol asking Alice to seal the conversation to a key
  // derived for Bob's identity — a transcript handed to a device that never asked for it.
  it('refuses a request one member signed in another member\'s name', () => {
    const { alice, carol } = establishTrio()
    const sig = carol.signHistoryRequest(gid(), CONV, 1n, NONCE)
    expect(() => alice.verifyHistoryRequest(gid(), CONV, 1n, BOB, NONCE, sig)).toThrow()
  })

  it('refuses an identity that holds no leaf in the group at all', () => {
    const { alice, bob } = establishTrio()
    const sig = bob.signHistoryRequest(gid(), CONV, 1n, NONCE)
    expect(() =>
      alice.verifyHistoryRequest(gid(), CONV, 1n, 'mimi://test.example/d/mallory/dev-m', NONCE, sig),
    ).toThrow()
  })

  // Every field is inside the transcript, so changing any of them changes the bytes signed. A
  // signature that survived a changed conversation id would be replayable into another chat.
  it('is bound to the conversation, the epoch and the nonce', () => {
    const { alice, bob } = establishTrio()
    const sig = bob.signHistoryRequest(gid(), CONV, 1n, NONCE)
    expect(() => alice.verifyHistoryRequest(gid(), 'another-conversation', 1n, BOB, NONCE, sig)).toThrow()
    expect(() => alice.verifyHistoryRequest(gid(), CONV, 2n, BOB, NONCE, sig)).toThrow()
    expect(() =>
      alice.verifyHistoryRequest(gid(), CONV, 1n, BOB, new Uint8Array(16), sig),
    ).toThrow()
  })
})

describe('signed history offers', () => {
  function offer(alice: MlsClient, ciphertext = SEALED): Uint8Array {
    return alice.signHistoryOffer(gid(), CONV, 1n, BOB, 'hist-1', SALT, AEAD_NONCE, NONCE, ciphertext)
  }

  it('verifies an offer against the offerer\'s leaf key and the blob it names', () => {
    const { alice, bob } = establishTrio()
    const sig = offer(alice)
    expect(() =>
      bob.verifyHistoryOffer(gid(), CONV, 1n, ALICE, 'hist-1', SALT, AEAD_NONCE, NONCE, SEALED, sig),
    ).not.toThrow()
  })

  // The signature covers a DIGEST of the ciphertext, not the id pointing at it. The server stores
  // the blob; without this it could swap the contents behind an otherwise perfect offer and hand a
  // fresh device somebody else's idea of the conversation.
  it('refuses a blob swapped behind a valid signature', () => {
    const { alice, bob } = establishTrio()
    const sig = offer(alice)
    const tampered = enc.encode('a sealed transcript of everything that was said!')
    expect(() =>
      bob.verifyHistoryOffer(gid(), CONV, 1n, ALICE, 'hist-1', SALT, AEAD_NONCE, NONCE, tampered, sig),
    ).toThrow()
  })

  it('refuses an offer Carol signed while claiming to be Alice', () => {
    const { bob, carol } = establishTrio()
    const sig = carol.signHistoryOffer(
      gid(), CONV, 1n, BOB, 'hist-1', SALT, AEAD_NONCE, NONCE, SEALED,
    )
    expect(() =>
      bob.verifyHistoryOffer(gid(), CONV, 1n, ALICE, 'hist-1', SALT, AEAD_NONCE, NONCE, SEALED, sig),
    ).toThrow()
  })

  // The requester is bound to the verifying device itself, never taken from the body — so an offer
  // addressed to one device cannot be replayed at another.
  it('refuses an offer addressed to a different device', () => {
    const { alice, carol } = establishTrio()
    const sig = offer(alice) // addressed to Bob
    expect(() =>
      carol.verifyHistoryOffer(gid(), CONV, 1n, ALICE, 'hist-1', SALT, AEAD_NONCE, NONCE, SEALED, sig),
    ).toThrow()
  })

  it('refuses an offer whose salt, nonce, history id or request nonce was altered', () => {
    const { alice, bob } = establishTrio()
    const sig = offer(alice)
    const other = new Uint8Array([9, 9])
    expect(() =>
      bob.verifyHistoryOffer(gid(), CONV, 1n, ALICE, 'hist-2', SALT, AEAD_NONCE, NONCE, SEALED, sig),
    ).toThrow()
    expect(() =>
      bob.verifyHistoryOffer(gid(), CONV, 1n, ALICE, 'hist-1', other, AEAD_NONCE, NONCE, SEALED, sig),
    ).toThrow()
    expect(() =>
      bob.verifyHistoryOffer(gid(), CONV, 1n, ALICE, 'hist-1', SALT, other, NONCE, SEALED, sig),
    ).toThrow()
    expect(() =>
      bob.verifyHistoryOffer(gid(), CONV, 1n, ALICE, 'hist-1', SALT, AEAD_NONCE, new Uint8Array(16), SEALED, sig),
    ).toThrow()
  })

  // Domain separation, from the other side: a request signature must never open an offer.
  it('will not accept a request signature as an offer signature', () => {
    const { alice, bob } = establishTrio()
    const requestSig = alice.signHistoryRequest(gid(), CONV, 1n, NONCE)
    expect(() =>
      bob.verifyHistoryOffer(gid(), CONV, 1n, ALICE, 'hist-1', SALT, AEAD_NONCE, NONCE, SEALED, requestSig),
    ).toThrow()
  })
})

// The wire bodies that carry those signatures. Everything here is what the app does around the
// crate: which bodies it will even look at, and the second, independent check that the identity a
// body claims matches the poster the server authenticated.
describe('the wire bodies around the signature', () => {
  const request = {
    v: 2,
    id: BOB,
    epoch: 1,
    nonce: 'AQIDBA==',
    sig: 'c2ln',
  }
  const offerBody = {
    v: 2,
    from: ALICE,
    to: BOB,
    epoch: 1,
    historyId: 'hist-1',
    salt: 'c2FsdA==',
    nonce: 'bm9uY2U=',
    reqNonce: 'AQIDBA==',
    sig: 'c2ln',
  }
  const b64 = (o: unknown) => btoa(JSON.stringify(o))

  it('round-trips a v2 request and a v2 offer', () => {
    expect(readHistoryRequest(b64(request))).toEqual(request)
    expect(readHistoryOffer(b64(offerBody))).toEqual(offerBody)
  })

  it('refuses v1 outright — there is no unsigned fallback', () => {
    expect(parseRequestBody(JSON.stringify({ id: BOB, epoch: 1 }))).toBeNull()
    expect(
      parseOfferBody(JSON.stringify({ to: BOB, epoch: 1, historyId: 'h', salt: 's', nonce: 'n' })),
    ).toBeNull()
  })

  it('accepts a claim only when it matches the server-authenticated poster', () => {
    // The server authenticates the SESSION that posted a control message and stamps its user id on
    // the envelope. An insider forging a body in somebody else's name then has to post it from
    // that person's account as well.
    expect(posterMatchesClaim(BOB, 'bob')).toBe(true)
    expect(posterMatchesClaim(BOB, 'alice')).toBe(false)
    // An older server sends no poster at all; the MLS signature is still the check that must hold.
    expect(posterMatchesClaim(BOB, '')).toBe(true)
    expect(posterMatchesClaim('not-a-credential', 'bob')).toBe(false)
  })
})

// The SAME file the Rust suite (crates/pheme-mls/tests/history_vectors.rs) and the Flutter suite
// (mobile/test/unit/history_handoff_test.dart) read.
//
// Web and mobile reach ONE canonical transcript through their bindings, so those bytes cannot drift
// by construction. What could drift is the body each client writes them from — a differently encoded
// nonce, a dropped `from` — and a body one client cannot reconstruct the other's transcript from is
// a signature that fails everywhere, silently, on the handoff path where a device is already showing
// a blank history.
describe('the cross-platform golden vectors', () => {
  const vectors = JSON.parse(
    readFileSync(fileURLToPath(new URL('../../test/fixtures/mls_history_vectors.json', import.meta.url)), 'utf8'),
  ) as {
    version: number
    request: Record<string, never> & { body: Record<string, unknown> } & Record<string, string | number>
    offer: Record<string, never> & { body: Record<string, unknown> } & Record<string, string | number>
  }

  const b64 = (hex: string) =>
    btoa(String.fromCharCode(...hex.match(/../g)!.map((h) => parseInt(h, 16))))

  it('speaks the version the vectors pin', () => {
    expect(HISTORY_VERSION).toBe(vectors.version)
  })

  it('builds a request body that matches the shared vector', () => {
    const v = vectors.request
    const built = {
      v: HISTORY_VERSION,
      id: v.requester as string,
      epoch: v.epoch as number,
      nonce: b64(v.nonceHex as string),
      sig: (v.body as Record<string, string>).sig,
    }
    expect(built).toEqual(v.body)
    // And it reads back: the encoder and the parser agree, which is what a round trip between two
    // clients actually is.
    expect(readHistoryRequest(btoa(JSON.stringify(built)))).toEqual(v.body)
  })

  it('builds an offer body that matches the shared vector', () => {
    const v = vectors.offer
    const built = {
      v: HISTORY_VERSION,
      from: v.offerer as string,
      to: v.requester as string,
      epoch: v.epoch as number,
      historyId: v.historyId as string,
      salt: b64(v.saltHex as string),
      nonce: b64(v.nonceHex as string),
      reqNonce: b64(vectors.request.nonceHex as string),
      sig: (v.body as Record<string, string>).sig,
    }
    expect(built).toEqual(v.body)
    expect(readHistoryOffer(btoa(JSON.stringify(built)))).toEqual(v.body)
  })
})
