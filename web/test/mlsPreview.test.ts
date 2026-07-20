// The MLS preview binding, exercised against the REAL WASM the service worker runs.
//
// These tests are the web-side half of a property proven in Rust (see
// `a_preview_decrypt_leaves_the_real_client_able_to_read_the_message` in crates/pheme-mls). Proving
// it again here is not duplication: what is being checked is that the wasm-bindgen surface the
// worker actually calls carries the property across the boundary intact. A binding that quietly
// exposed the writable client, or that shared a provider between the two, would pass every Rust
// test and still lose messages in a browser.
//
// If any of this fails, previews must be turned off. The production symptom would be silent,
// permanent message loss: previewed messages rendering blank in the app, forever, with nothing in
// any log to say why.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { beforeAll, describe, expect, it } from 'vitest'
import init, { MlsClient, MlsPreviewClient } from '../src/crypto/pkg/pheme_mls.js'

const GROUP = 'grp-preview-test'
const enc = new TextEncoder()

beforeAll(async () => {
  // Node has no fetch-from-URL for local files, so hand the bytes in directly.
  const wasm = readFileSync(fileURLToPath(new URL('../src/crypto/pkg/pheme_mls_bg.wasm', import.meta.url)))
  await init({ module_or_path: wasm })
})

/** Alice and Bob in one group, with Bob joined from Alice's Welcome. */
function establishPair(): { alice: MlsClient; bob: MlsClient } {
  const alice = new MlsClient('test.example', 'alice', 'dev-a')
  const bob = new MlsClient('test.example', 'bob', 'dev-b')
  alice.createGroup(enc.encode(GROUP))
  const staged = alice.stageAdd(enc.encode(GROUP), [bob.keyPackage()])
  alice.commitAccepted(enc.encode(GROUP))
  bob.joinFromWelcome(staged.welcome)
  return { alice, bob }
}

function bodyOf(bytes: Uint8Array): string {
  return JSON.parse(new TextDecoder().decode(bytes)).body
}

describe('MlsPreviewClient', () => {
  it('reads a message from a snapshot without spending the real client key', () => {
    const { alice, bob } = establishPair()
    const ciphertext = alice.encrypt(enc.encode(GROUP), enc.encode(JSON.stringify({ body: 'hello there' })))

    // The worker wakes with a copy of Bob's state out of IndexedDB.
    const snapshot = bob.exportState()
    const preview = MlsPreviewClient.fromState(snapshot)
    const previewed = preview.decrypt(enc.encode(GROUP), ciphertext)
    expect(previewed).toBeTruthy()
    expect(bodyOf(previewed!)).toBe('hello there')
    preview.free() // the notification is shown; the worker dies

    // The tab opens. The message must still decrypt, for real, into the transcript.
    const real = bob.decrypt(enc.encode(GROUP), ciphertext)
    expect(real, 'the real client lost the message: previewing consumed the key it needed').toBeTruthy()
    expect(bodyOf(real!)).toBe('hello there')
  })

  it('refuses a commit instead of advancing the epoch', () => {
    const { alice, bob } = establishPair()
    const carol = new MlsClient('test.example', 'carol', 'dev-c')
    const staged = alice.stageAdd(enc.encode(GROUP), [carol.keyPackage()])
    alice.commitAccepted(enc.encode(GROUP))

    const preview = MlsPreviewClient.fromState(bob.exportState())
    // Nothing in a commit is a message, and merging one in state that is then discarded would
    // leave the real client behind by an epoch it never saw.
    expect(preview.decrypt(enc.encode(GROUP), staged.commit)).toBeFalsy()
    preview.free()

    // Bob has still never seen it, so applying it for real works.
    bob.applyCommit(enc.encode(GROUP), staged.commit)
    const after = alice.encrypt(enc.encode(GROUP), enc.encode(JSON.stringify({ body: 'after carol' })))
    expect(bodyOf(bob.decrypt(enc.encode(GROUP), after)!)).toBe('after carol')
  })

  it('has no way to persist state', () => {
    const { bob } = establishPair()
    const preview = MlsPreviewClient.fromState(bob.exportState())
    // The guarantee is the absence of this method, not a rule anyone has to remember. If a future
    // binding adds it, the worker becomes a second writer to the key store and the single-client
    // rule is broken — so the absence is asserted rather than assumed.
    expect((preview as unknown as Record<string, unknown>).exportState).toBeUndefined()
    preview.free()
  })

  it('survives repeated previews of the same message', () => {
    const { alice, bob } = establishPair()
    const ciphertext = alice.encrypt(enc.encode(GROUP), enc.encode(JSON.stringify({ body: 'twice' })))
    const snapshot = bob.exportState()

    // A retried push, or two devices previewing the same message.
    for (let i = 0; i < 3; i++) {
      const preview = MlsPreviewClient.fromState(snapshot)
      expect(bodyOf(preview.decrypt(enc.encode(GROUP), ciphertext)!)).toBe('twice')
      preview.free()
    }

    expect(bodyOf(bob.decrypt(enc.encode(GROUP), ciphertext)!)).toBe('twice')
  })

  it('reports which groups it can read, so the worker need not guess', () => {
    const { bob } = establishPair()
    const preview = MlsPreviewClient.fromState(bob.exportState())
    expect(preview.hasGroup(enc.encode(GROUP))).toBe(true)
    expect(preview.hasGroup(enc.encode('grp-not-mine'))).toBe(false)
    preview.free()
  })
})
