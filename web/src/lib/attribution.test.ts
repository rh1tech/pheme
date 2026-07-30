// Who a cached message is attributed to, and how far that attribution can be trusted.
//
// This is the layer between "MLS authenticated a credential" and "a bubble renders a name", and it
// is where the bug that started all of this actually bit: the app had the authenticated sender
// nowhere, so every surface — the bubble, the quote, the sidebar row, the notification — answered
// "who wrote this?" from the envelope, which the untrusted server writes.
//
// Pure by construction (strings in, strings out), so every rule below is exercised without WASM, a
// session or a group.

import { describe, expect, it } from 'vitest'
import {
  authenticated,
  decodeCacheEntry,
  encodeCacheEntry,
  isOwnMessage,
  markRelayed,
  resolveAuthor,
  type Attribution,
} from './attribution'

const ALICE = 'mimi://test.example/d/alice/dev-a'
const BOB = 'mimi://test.example/d/bob/dev-b'

describe('authenticated', () => {
  it('reduces a credential to the bare user the roster is keyed by', () => {
    expect(authenticated(ALICE)).toEqual({ kind: 'mls', identity: ALICE, userId: 'alice' })
  })

  it('is legacy for anything that is not a resolvable credential', () => {
    // A pre-device-id leaf (`user:device`), or an empty sender. Neither names a user we could
    // compare against a membership list, and inventing one is how a message gets rendered under
    // somebody else's name.
    expect(authenticated('alice:dev-a').kind).toBe('legacy')
    expect(authenticated('').kind).toBe('legacy')
  })
})

describe('the cache entry', () => {
  const content = { body: 'hello', replyTo: 'm-0', photos: [] }

  it('carries the sender alongside the body', () => {
    const serialised = encodeCacheEntry(content, authenticated(ALICE))
    const back = decodeCacheEntry(serialised)
    expect(back?.content.body).toBe('hello')
    expect(back?.content.replyTo).toBe('m-0')
    expect(back?.attribution).toEqual({ kind: 'mls', identity: ALICE, userId: 'alice' })
  })

  // Every message anybody decrypted before this existed is one of these, and the MLS key that could
  // have re-derived the sender is long gone. Dropping them would destroy the only copy of that
  // plaintext there will ever be.
  it('reads a cache entry from before senders were stored, as legacy', () => {
    const legacy = JSON.stringify({ body: 'from an older build' })
    const back = decodeCacheEntry(legacy)
    expect(back?.content.body).toBe('from an older build')
    expect(back?.attribution.kind).toBe('legacy')
  })

  it('reads an ancient bare-string entry rather than losing it', () => {
    const back = decodeCacheEntry('just a body, no JSON at all')
    expect(back?.content.body).toBe('just a body, no JSON at all')
    expect(back?.attribution.kind).toBe('legacy')
  })

  it('does not treat an unparseable sender as an attribution', () => {
    const serialised = JSON.stringify({ body: 'hi', _s: 'alice:dev-a' })
    expect(decodeCacheEntry(serialised)?.attribution.kind).toBe('legacy')
  })

  // The extra fields ride on the same object, so a build that has never heard of `_s` still finds
  // body, replyTo and photos exactly where it expects them.
  it('keeps the content fields where an older reader looks for them', () => {
    const raw = JSON.parse(encodeCacheEntry(content, authenticated(ALICE))) as Record<string, unknown>
    expect(raw.body).toBe('hello')
    expect(raw.replyTo).toBe('m-0')
    expect(raw._s).toBe(ALICE)
  })
})

describe('imported history', () => {
  // A co-member signs the whole transfer with its leaf key, so the transcript is attributable to a
  // real member of the group. The per-message author inside it is still that member's WORD — this
  // device did not authenticate it — and the two must never become indistinguishable in the cache.
  it('marks an imported entry with who handed it over', () => {
    const original = encodeCacheEntry({ body: 'said before I joined' }, authenticated(ALICE))
    const imported = decodeCacheEntry(markRelayed(original, BOB))
    expect(imported?.attribution).toEqual({
      kind: 'relayed',
      identity: ALICE,
      userId: 'alice',
      relayedBy: BOB,
    })
    expect(imported?.content.body).toBe('said before I joined')
  })

  it('does not invent an author for an imported entry that names none', () => {
    const legacy = JSON.stringify({ body: 'no sender in here' })
    expect(decodeCacheEntry(markRelayed(legacy, BOB))?.attribution.kind).toBe('legacy')
  })

  it('never presents relayed authorship as verified', () => {
    const relayed: Attribution = { kind: 'relayed', identity: ALICE, userId: 'alice', relayedBy: BOB }
    expect(resolveAuthor(relayed, 'alice').verified).toBe(false)
  })
})

describe('resolveAuthor', () => {
  const mls = authenticated(ALICE)

  it('renders an MLS-authenticated message under the signer', () => {
    expect(resolveAuthor(mls, 'alice')).toEqual({ userId: 'alice', verified: true, tampered: false })
  })

  // THE ATTACK, caught. The server relayed Bob's ciphertext with Alice's id on the envelope — or
  // Alice's ciphertext under Bob's. Either way the two disagree, and picking one of them silently
  // is exactly what the authenticated sender exists to prevent.
  it('reports a mismatch between the signature and the envelope', () => {
    expect(resolveAuthor(mls, 'bob')).toEqual({ userId: 'alice', verified: false, tampered: true })
  })

  it('falls back to the envelope for a legacy entry, and never calls it verified', () => {
    expect(resolveAuthor({ kind: 'legacy' }, 'alice')).toEqual({
      userId: 'alice',
      verified: false,
      tampered: false,
    })
  })

  it('does not report a mismatch when there is no envelope claim to compare', () => {
    expect(resolveAuthor(mls, '')).toEqual({ userId: 'alice', verified: true, tampered: false })
  })
})

describe('isOwnMessage', () => {
  it('decides from the signature, not the envelope', () => {
    // The server claiming our own id on somebody else's message must not put it on our side of the
    // feed — which is where a reader assumes they wrote it themselves.
    expect(isOwnMessage(authenticated(BOB), 'alice', 'alice')).toBe(false)
    expect(isOwnMessage(authenticated(ALICE), 'bob', 'alice')).toBe(true)
  })

  it('uses the envelope only where there is no plaintext, and so no signature', () => {
    // A message this device cannot read at all has no attribution. The envelope is genuinely all
    // that exists for it, and the bubble still has to land on one side or the other.
    expect(isOwnMessage(undefined, 'alice', 'alice')).toBe(true)
    expect(isOwnMessage({ kind: 'legacy' }, 'alice', 'alice')).toBe(true)
    expect(isOwnMessage({ kind: 'legacy' }, 'bob', 'alice')).toBe(false)
  })

  it('is nobody\'s message before we know who we are', () => {
    expect(isOwnMessage(authenticated(ALICE), 'alice', '')).toBe(false)
  })
})
