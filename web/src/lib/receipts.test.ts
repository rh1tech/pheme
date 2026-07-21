import { describe, expect, it } from 'vitest'
import { applyReceipt, messageReceipt } from './receipts'
import type { ConversationMember } from './types'

const ME = 'me'
// Message sequences: strictly increasing, mirroring the hub's per-conversation counter.
const S = {
  early: 10,
  mid: 11,
  late: 12,
}

function member(userId: string, deliveredSeq?: number, readSeq?: number): ConversationMember {
  return {
    id: `m-${userId}`,
    conversationId: 'c1',
    userId,
    role: 'user',
    joinedAt: '2026-07-16T10:00:00Z',
    user: { id: userId, displayName: userId },
    deliveredSeq,
    readSeq,
  } as ConversationMember
}

describe('messageReceipt', () => {
  it('is sent while the other side has not reported at all', () => {
    const members = [member(ME), member('bob')]
    expect(messageReceipt(S.mid, members, ME)).toBe('sent')
  })

  it('is delivered once they have received up to it', () => {
    const members = [member(ME), member('bob', S.late)]
    expect(messageReceipt(S.mid, members, ME)).toBe('delivered')
  })

  it('is read once they have read up to it', () => {
    const members = [member(ME), member('bob', S.late, S.late)]
    expect(messageReceipt(S.mid, members, ME)).toBe('read')
  })

  it('is still only delivered for a message NEWER than what they have read', () => {
    const members = [member(ME), member('bob', S.late, S.early)]
    expect(messageReceipt(S.mid, members, ME)).toBe('delivered')
  })

  it('never waits on yourself — your own watermark is irrelevant', () => {
    // We have read nothing; they have read everything. It is read.
    const members = [member(ME), member('bob', S.late, S.late)]
    expect(messageReceipt(S.mid, members, ME)).toBe('read')
  })

  // The rule the user asked for: in a group, two ticks mean EVERYONE read it.
  describe('groups wait for the slowest member', () => {
    it('is read only once every member has read it', () => {
      const all = [member(ME), member('bob', S.late, S.late), member('carol', S.late, S.late)]
      expect(messageReceipt(S.mid, all, ME)).toBe('read')
    })

    it('is delivered, not read, while one member has only received it', () => {
      const members = [member(ME), member('bob', S.late, S.late), member('carol', S.late, S.early)]
      expect(messageReceipt(S.mid, members, ME)).toBe('delivered')
    })

    it('is sent while one member has not received it at all', () => {
      const members = [member(ME), member('bob', S.late, S.late), member('carol')]
      expect(messageReceipt(S.mid, members, ME)).toBe('sent')
    })
  })

  it('claims nothing in a conversation with nobody else left in it', () => {
    expect(messageReceipt(S.mid, [member(ME)], ME)).toBe('sent')
  })
})

describe('applyReceipt', () => {
  it('moves a member forward', () => {
    const members = [member(ME), member('bob', S.early, S.early)]
    const next = applyReceipt(members, { userId: 'bob', deliveredSeq: S.late, readSeq: S.mid })
    expect(next[1].deliveredSeq).toBe(S.late)
    expect(next[1].readSeq).toBe(S.mid)
  })

  // Out-of-order receipts are normal — two devices, a retry, a catch-up. An older one must not
  // un-read what is already read, exactly as the server refuses to.
  it('never moves a member backwards', () => {
    const members = [member(ME), member('bob', S.late, S.late)]
    const next = applyReceipt(members, { userId: 'bob', deliveredSeq: S.early, readSeq: S.early })
    expect(next[1].deliveredSeq).toBe(S.late)
    expect(next[1].readSeq).toBe(S.late)
  })

  it('leaves everyone else untouched', () => {
    const members = [member(ME), member('bob', S.early), member('carol', S.early)]
    const next = applyReceipt(members, { userId: 'bob', readSeq: S.late })
    expect(next[2]).toBe(members[2])
  })
})
