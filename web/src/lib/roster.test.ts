import { describe, expect, it } from 'vitest'
import { deviceIdentity, deviceOf, missingDevices, staleLeaves, userOf } from './roster'

// The rules that decide who can read a conversation.
//
// These were reachable only through a session that needs WASM, a server and a real group, so they
// were exercised end to end or not at all — and one of them shipped broken: a revoked device kept
// its leaf, because the check for it came after a bail that swallowed the case.

const ME = 'me:my-device'

describe('identity halves', () => {
  it('splits a leaf into its user and device', () => {
    expect(userOf('alice:phone')).toBe('alice')
    expect(deviceOf('alice:phone')).toBe('phone')
    expect(deviceIdentity('alice', 'phone')).toBe('alice:phone')
  })

  // A legacy identity is a bare user id with no device. Both halves must report '' rather than
  // guessing, because a mangled user id reads as a departed member and prunes the wrong leaf.
  it('reports both halves as empty for a legacy identity', () => {
    expect(userOf('alice')).toBe('')
    expect(deviceOf('alice')).toBe('')
  })

  // A device id is a uuid and contains no colon, but the user half must still be the FIRST field
  // if one ever did.
  it('splits on the first colon only', () => {
    expect(userOf('alice:has:colons')).toBe('alice')
    expect(deviceOf('alice:has:colons')).toBe('has:colons')
  })
})

describe('staleLeaves', () => {
  it('never prunes our own leaf', () => {
    // Even when every other rule would say to: we are not on the roster and have published nothing.
    expect(staleLeaves(ME, [ME], [], {}, {})).toEqual([])
  })

  it('prunes a legacy leaf that nobody can hold keys for', () => {
    expect(staleLeaves(ME, ['alice'], ['alice'], { alice: ['phone'] })).toEqual(['alice'])
  })

  it('prunes a departed member', () => {
    const stale = staleLeaves(ME, ['gone:phone'], ['still-here'], { 'gone': ['phone'] })
    expect(stale).toEqual(['gone:phone'])
  })

  // THE ONE THAT SHIPPED BROKEN. Terminating a device deletes its KeyPackages, so a revoked device
  // has nothing published — and the "cannot tell, leave them be" bail below would wave it through.
  // The revoked check has to come first.
  it('prunes a revoked device even though it has published nothing', () => {
    const stale = staleLeaves(
      ME,
      ['alice:old-browser'],
      ['alice'],
      { alice: [] }, // its packages were deleted on termination
      { alice: ['old-browser'] },
    )
    expect(stale).toEqual(['alice:old-browser'])
  })

  it('prunes a revoked device when the user has OTHER live devices', () => {
    const stale = staleLeaves(
      ME,
      ['alice:phone', 'alice:old-browser'],
      ['alice'],
      { alice: ['phone'] },
      { alice: ['old-browser'] },
    )
    expect(stale).toEqual(['alice:old-browser'])
  })

  it('prunes a ghost device the member no longer publishes', () => {
    const stale = staleLeaves(ME, ['alice:ghost'], ['alice'], { alice: ['phone'] })
    expect(stale).toEqual(['alice:ghost'])
  })

  // A member who has never opened the app publishes nothing. Evicting them would make the app
  // unusable for anyone invited before they first sign in.
  it('leaves a member with no published devices alone', () => {
    expect(staleLeaves(ME, ['newcomer:phone'], ['newcomer'], {})).toEqual([])
    expect(staleLeaves(ME, ['newcomer:phone'], ['newcomer'], { newcomer: [] })).toEqual([])
  })

  it('keeps a member whose device is published', () => {
    expect(staleLeaves(ME, ['alice:phone'], ['alice'], { alice: ['phone'] })).toEqual([])
  })

  it('keeps every device of a member with several', () => {
    const leaves = ['alice:phone', 'alice:laptop', 'alice:tablet']
    expect(staleLeaves(ME, leaves, ['alice'], { alice: ['phone', 'laptop', 'tablet'] })).toEqual([])
  })

  // A departed member's devices ALL go, not just the ones that stopped publishing.
  it('prunes every device of a departed member', () => {
    const stale = staleLeaves(
      ME,
      ['gone:phone', 'gone:laptop'],
      ['still-here'],
      { gone: ['phone', 'laptop'] },
    )
    expect(stale).toEqual(['gone:phone', 'gone:laptop'])
  })

  it('handles an empty group without inventing work', () => {
    expect(staleLeaves(ME, [], [], {}, {})).toEqual([])
  })

  // Revocation is per-device, not per-user: naming one device must not evict the others.
  it('does not prune a live device because a sibling was revoked', () => {
    const stale = staleLeaves(
      ME,
      ['alice:phone'],
      ['alice'],
      { alice: ['phone'] },
      { alice: ['old-browser'] },
    )
    expect(stale).toEqual([])
  })

  it('mixes every reason in one pass', () => {
    const stale = staleLeaves(
      ME,
      [ME, 'legacy', 'gone:phone', 'alice:revoked', 'alice:ghost', 'alice:phone', 'newcomer:phone'],
      ['alice', 'newcomer'],
      { alice: ['phone'] },
      { alice: ['revoked'] },
    )
    expect(stale).toEqual(['legacy', 'gone:phone', 'alice:revoked', 'alice:ghost'])
  })
})

describe('missingDevices', () => {
  it('finds a published device that is not yet a leaf', () => {
    expect(missingDevices(ME, { alice: ['phone'] }, [])).toEqual([{ userId: 'alice', deviceId: 'phone' }])
  })

  it('ignores devices that are already leaves', () => {
    expect(missingDevices(ME, { alice: ['phone'] }, ['alice:phone'])).toEqual([])
  })

  // Claiming our own KeyPackage burns one for nothing: we hold the group already.
  it('never returns our own device', () => {
    expect(missingDevices(ME, { me: ['my-device'] }, [])).toEqual([])
  })

  // A zombie's package produces a leaf that does not answer to its own identity, so it stays
  // "missing" forever. Re-claiming it every round is half of an add/prune war.
  it('never returns a known zombie', () => {
    const zombies = new Set(['alice:zombie'])
    expect(missingDevices(ME, { alice: ['zombie'] }, [], zombies)).toEqual([])
  })

  it('returns the live devices of a user who also has a zombie', () => {
    const zombies = new Set(['alice:zombie'])
    expect(missingDevices(ME, { alice: ['zombie', 'phone'] }, [], zombies)).toEqual([
      { userId: 'alice', deviceId: 'phone' },
    ])
  })

  it('spans several users', () => {
    const missing = missingDevices(ME, { alice: ['phone'], bob: ['laptop'] }, [])
    expect(missing).toHaveLength(2)
    expect(missing).toContainEqual({ userId: 'alice', deviceId: 'phone' })
    expect(missing).toContainEqual({ userId: 'bob', deviceId: 'laptop' })
  })

  it('finds nothing when everyone is already in', () => {
    expect(missingDevices(ME, { alice: ['phone'] }, ['alice:phone', ME])).toEqual([])
  })

  it('handles a user with no published devices', () => {
    expect(missingDevices(ME, { alice: [] }, [])).toEqual([])
  })
})
