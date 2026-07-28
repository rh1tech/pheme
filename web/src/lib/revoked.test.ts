// Whether a browser notices that its own device has been revoked.
//
// The failure this pins was live on prod: signing in on a phone revoked the browser's device, the
// browser kept its keys in IndexedDB, and nothing ever asked the server whether that identity was
// still alive. So it never prompted for a recovery code, every co-member had already pruned its
// leaf, and the result was a chat that looked open and worked in neither direction — sends failed
// with GroupStateError(UseAfterEviction), and messages from the other devices arrived, showed "…",
// and vanished.
//
// The rule under test is the ASYMMETRY. Only a clear "your id is in the revoked list" counts as
// revoked. Anything else — offline, an error, a device that simply never registered — must be
// treated as alive, because discarding a live identity destroys the keys to every conversation this
// browser can read, and it would do so on any launch that merely happened to be offline.

import { describe, expect, it } from 'vitest'
import { revokedLocally } from './revoked'

/** A registry that answers, naming the revoked ids. */
const answering = (revoked: string[]) => async () => ({ revoked })

/** A registry that cannot answer: offline, expired session, anything at all. */
const failing = () => async (): Promise<{ revoked: string[] }> => {
  throw new Error('unreachable')
}

describe('a device the server has revoked', () => {
  it('is recognised when the server names it', async () => {
    expect(await revokedLocally('dev-dead', answering(['dev-dead']))).toBe(true)
  })

  it('is not recognised from absence alone', async () => {
    // A device missing from the live list may equally never have registered — and that case must
    // register, not throw away its keys and start over.
    expect(await revokedLocally('dev-unregistered', answering([]))).toBe(false)
  })
})

describe('anything short of a clear answer keeps the identity', () => {
  it('survives the server being unreachable', async () => {
    expect(await revokedLocally('dev-live', failing())).toBe(false)
  })

  it('survives a reply with no revoked list at all', async () => {
    // An older server, which does not report them. It must read as "not revoked", never as
    // "revoked", or upgrading the client ahead of the server would wipe every browser.
    expect(await revokedLocally('dev-live', answering([]))).toBe(false)
  })

  it('does not even ask about a device with no id', async () => {
    let asked = false
    await revokedLocally('', async () => {
      asked = true
      return { revoked: [] }
    })
    expect(asked).toBe(false)
  })
})
