import { describe, expect, it } from 'vitest'
import { applyReceipt, messageReceipt } from './receipts'
import type { ConversationMember } from './types'

const ME = 'me'
const T = {
  early: '2026-07-16T10:00:00Z',
  mid: '2026-07-16T11:00:00Z',
  late: '2026-07-16T12:00:00Z',
}

function member(userId: string, deliveredAt?: string, readAt?: string): ConversationMember {
  return {
    id: `m-${userId}`,
    conversationId: 'c1',
    userId,
    role: 'user',
    joinedAt: T.early,
    user: { id: userId, displayName: userId },
    deliveredAt,
    readAt,
  } as ConversationMember
}

describe('messageReceipt', () => {
  it('is sent while the other side has not reported at all', () => {
    const members = [member(ME), member('bob')]
    expect(messageReceipt(T.mid, members, ME)).toBe('sent')
  })

  it('is delivered once they have received up to it', () => {
    const members = [member(ME), member('bob', T.late)]
    expect(messageReceipt(T.mid, members, ME)).toBe('delivered')
  })

  it('is read once they have read up to it', () => {
    const members = [member(ME), member('bob', T.late, T.late)]
    expect(messageReceipt(T.mid, members, ME)).toBe('read')
  })

  it('is still only delivered for a message NEWER than what they have read', () => {
    const members = [member(ME), member('bob', T.late, T.early)]
    expect(messageReceipt(T.mid, members, ME)).toBe('delivered')
  })

  it('never waits on yourself — your own watermark is irrelevant', () => {
    // We have read nothing; they have read everything. It is read.
    const members = [member(ME), member('bob', T.late, T.late)]
    expect(messageReceipt(T.mid, members, ME)).toBe('read')
  })

  // The rule the user asked for: in a group, two ticks mean EVERYONE read it.
  describe('groups wait for the slowest member', () => {
    it('is read only once every member has read it', () => {
      const all = [member(ME), member('bob', T.late, T.late), member('carol', T.late, T.late)]
      expect(messageReceipt(T.mid, all, ME)).toBe('read')
    })

    it('is delivered, not read, while one member has only received it', () => {
      const members = [member(ME), member('bob', T.late, T.late), member('carol', T.late, T.early)]
      expect(messageReceipt(T.mid, members, ME)).toBe('delivered')
    })

    it('is sent while one member has not received it at all', () => {
      const members = [member(ME), member('bob', T.late, T.late), member('carol')]
      expect(messageReceipt(T.mid, members, ME)).toBe('sent')
    })
  })

  it('claims nothing in a conversation with nobody else left in it', () => {
    expect(messageReceipt(T.mid, [member(ME)], ME)).toBe('sent')
  })
})

describe('applyReceipt', () => {
  it('moves a member forward', () => {
    const members = [member(ME), member('bob', T.early, T.early)]
    const next = applyReceipt(members, { userId: 'bob', deliveredAt: T.late, readAt: T.mid })
    expect(next[1].deliveredAt).toBe(T.late)
    expect(next[1].readAt).toBe(T.mid)
  })

  // Out-of-order receipts are normal — two devices, a retry, a catch-up. An older one must not
  // un-read what is already read, exactly as the server refuses to.
  it('never moves a member backwards', () => {
    const members = [member(ME), member('bob', T.late, T.late)]
    const next = applyReceipt(members, { userId: 'bob', deliveredAt: T.early, readAt: T.early })
    expect(next[1].deliveredAt).toBe(T.late)
    expect(next[1].readAt).toBe(T.late)
  })

  it('leaves everyone else untouched', () => {
    const members = [member(ME), member('bob', T.early), member('carol', T.early)]
    const next = applyReceipt(members, { userId: 'bob', readAt: T.late })
    expect(next[2]).toBe(members[2])
  })
})
