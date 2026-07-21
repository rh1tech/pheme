import { describe, expect, it } from 'vitest'
import {
  deviceIdentity,
  deviceOf,
  domainsByUser,
  missingDevices,
  remoteMemberRefs,
  staleLeaves,
  userKey,
  userOf,
} from './roster'

describe('remoteMemberRefs', () => {
  const members = [
    { userId: 'alice' }, // local (no domain)
    { userId: 'bob', domain: 'b.example' }, // remote, no leaf yet
    { userId: 'carol', domain: 'c.example' }, // remote, already a leaf
  ]

  it('returns only remote members that have no leaf yet, with a blank device id', () => {
    const leaves = ['mimi://c.example/d/carol/phone']
    expect(remoteMemberRefs(members, leaves, 'alice')).toEqual([{ userId: 'bob', deviceId: '' }])
  })

  it('skips ourselves and every local member', () => {
    expect(remoteMemberRefs([{ userId: 'me', domain: 'b.example' }], [], 'me')).toEqual([])
    expect(remoteMemberRefs([{ userId: 'x' }], [], 'me')).toEqual([])
  })

  it('is empty when nobody carries a domain (single-host)', () => {
    expect(remoteMemberRefs([{ userId: 'a' }, { userId: 'b' }], [], 'a')).toEqual([])
  })

  it('domainsByUser maps only the remote members', () => {
    expect(domainsByUser(members)).toEqual({ bob: 'b.example', carol: 'c.example' })
  })
})

// The rules that decide who can read a conversation.
//
// These were reachable only through a session that needs WASM, a server and a real group, so they
// were exercised end to end or not at all — and one of them shipped broken: a revoked device kept
// its leaf, because the check for it came after a bail that swallowed the case.
//
// Leaf identities are the qualified credential form, `mimi://<domain>/d/<user>/<device>`, built via
// deviceIdentity (default domain 'local'). Membership and the key-package directory stay keyed by
// the host-local user id, which is what userOf returns — distinctness across hosts is carried by
// the domain in the leaf, not by these keys.
const L = (user: string, device: string) => deviceIdentity(user, device)
const ME = L('me', 'my-device')

describe('identity halves', () => {
  it('splits a qualified leaf into its bare user and device', () => {
    const leaf = L('alice', 'phone')
    expect(leaf).toBe('mimi://local/d/alice/phone')
    expect(userOf(leaf)).toBe('alice')
    expect(deviceOf(leaf)).toBe('phone')
  })

  it('userKey builds the qualified removal-target form the crate matches', () => {
    expect(userKey('alice')).toBe('mimi://local/u/alice')
  })

  // An identity that does not parse — a legacy `user:device` leaf, or a bare user — reports both
  // halves as '' rather than guessing, because a mangled user id reads as a departed member and
  // would prune the wrong leaf.
  it('reports both halves as empty for an unparseable identity', () => {
    expect(userOf('alice:phone')).toBe('')
    expect(deviceOf('alice:phone')).toBe('')
    expect(userOf('alice')).toBe('')
  })
})

describe('staleLeaves', () => {
  it('never prunes our own leaf', () => {
    // Even when every other rule would say to: we are not on the roster and have published nothing.
    expect(staleLeaves(ME, [ME], [], {}, {})).toEqual([])
  })

  it('prunes an unparseable leaf that nobody can hold keys for', () => {
    expect(staleLeaves(ME, ['legacy'], ['alice'], { alice: ['phone'] })).toEqual(['legacy'])
  })

  it('prunes a departed member', () => {
    const stale = staleLeaves(ME, [L('gone', 'phone')], ['still-here'], { gone: ['phone'] })
    expect(stale).toEqual([L('gone', 'phone')])
  })

  // THE ONE THAT SHIPPED BROKEN. Terminating a device deletes its KeyPackages, so a revoked device
  // has nothing published — and the "cannot tell, leave them be" bail below would wave it through.
  // The revoked check has to come first.
  it('prunes a revoked device even though it has published nothing', () => {
    const stale = staleLeaves(
      ME,
      [L('alice', 'old-browser')],
      ['alice'],
      { alice: [] }, // its packages were deleted on termination
      { alice: ['old-browser'] },
    )
    expect(stale).toEqual([L('alice', 'old-browser')])
  })

  it('prunes a revoked device when the user has OTHER live devices', () => {
    const stale = staleLeaves(
      ME,
      [L('alice', 'phone'), L('alice', 'old-browser')],
      ['alice'],
      { alice: ['phone'] },
      { alice: ['old-browser'] },
    )
    expect(stale).toEqual([L('alice', 'old-browser')])
  })

  it('prunes a ghost device the member no longer publishes', () => {
    const stale = staleLeaves(ME, [L('alice', 'ghost')], ['alice'], { alice: ['phone'] })
    expect(stale).toEqual([L('alice', 'ghost')])
  })

  // A member who has never opened the app publishes nothing. Evicting them would make the app
  // unusable for anyone invited before they first sign in.
  it('leaves a member with no published devices alone', () => {
    expect(staleLeaves(ME, [L('newcomer', 'phone')], ['newcomer'], {})).toEqual([])
    expect(staleLeaves(ME, [L('newcomer', 'phone')], ['newcomer'], { newcomer: [] })).toEqual([])
  })

  it('keeps a member whose device is published', () => {
    expect(staleLeaves(ME, [L('alice', 'phone')], ['alice'], { alice: ['phone'] })).toEqual([])
  })

  it('keeps every device of a member with several', () => {
    const leaves = [L('alice', 'phone'), L('alice', 'laptop'), L('alice', 'tablet')]
    expect(staleLeaves(ME, leaves, ['alice'], { alice: ['phone', 'laptop', 'tablet'] })).toEqual([])
  })

  // A departed member's devices ALL go, not just the ones that stopped publishing.
  it('prunes every device of a departed member', () => {
    const stale = staleLeaves(
      ME,
      [L('gone', 'phone'), L('gone', 'laptop')],
      ['still-here'],
      { gone: ['phone', 'laptop'] },
    )
    expect(stale).toEqual([L('gone', 'phone'), L('gone', 'laptop')])
  })

  it('handles an empty group without inventing work', () => {
    expect(staleLeaves(ME, [], [], {}, {})).toEqual([])
  })

  // Revocation is per-device, not per-user: naming one device must not evict the others.
  it('does not prune a live device because a sibling was revoked', () => {
    const stale = staleLeaves(
      ME,
      [L('alice', 'phone')],
      ['alice'],
      { alice: ['phone'] },
      { alice: ['old-browser'] },
    )
    expect(stale).toEqual([])
  })

  it('mixes every reason in one pass', () => {
    const stale = staleLeaves(
      ME,
      [
        ME,
        'legacy',
        L('gone', 'phone'),
        L('alice', 'revoked'),
        L('alice', 'ghost'),
        L('alice', 'phone'),
        L('newcomer', 'phone'),
      ],
      ['alice', 'newcomer'],
      { alice: ['phone'] },
      { alice: ['revoked'] },
    )
    expect(stale).toEqual(['legacy', L('gone', 'phone'), L('alice', 'revoked'), L('alice', 'ghost')])
  })
})

describe('missingDevices', () => {
  it('finds a published device that is not yet a leaf', () => {
    expect(missingDevices(ME, { alice: ['phone'] }, [])).toEqual([{ userId: 'alice', deviceId: 'phone' }])
  })

  it('ignores devices that are already leaves', () => {
    expect(missingDevices(ME, { alice: ['phone'] }, [L('alice', 'phone')])).toEqual([])
  })

  // Claiming our own KeyPackage burns one for nothing: we hold the group already.
  it('never returns our own device', () => {
    expect(missingDevices(ME, { me: ['my-device'] }, [])).toEqual([])
  })

  // A zombie's package produces a leaf that does not answer to its own identity, so it stays
  // "missing" forever. Re-claiming it every round is half of an add/prune war.
  it('never returns a known zombie', () => {
    const zombies = new Set([L('alice', 'zombie')])
    expect(missingDevices(ME, { alice: ['zombie'] }, [], zombies)).toEqual([])
  })

  it('returns the live devices of a user who also has a zombie', () => {
    const zombies = new Set([L('alice', 'zombie')])
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
    expect(missingDevices(ME, { alice: ['phone'] }, [L('alice', 'phone'), ME])).toEqual([])
  })

  it('handles a user with no published devices', () => {
    expect(missingDevices(ME, { alice: [] }, [])).toEqual([])
  })
})
