// What the ticks on your own message mean, worked out from the other members' watermarks.
//
// The server tracks how far each member has got — received up to deliveredAt, read up to readAt —
// rather than a state per message per member. Messages are ordered by createdAt, so "read up to T"
// already says everything about every message at or before T, and a message's ticks are then a
// comparison: delivered once EVERY other member has received it, read once every other member has
// read it. That is the "everyone has read it" rule, and in a 1:1 chat "everyone" is just them.

import type { ConversationMember } from './types'

/** Nothing yet, one tick (everyone received it), or two (everyone read it). */
export type Receipt = 'sent' | 'delivered' | 'read'

/**
 * The point every member OTHER than `userId` has reached — the oldest of their watermarks, because
 * a message is only through once the slowest of them has it.
 *
 * Empty when there is nobody else (everyone left): there is no one to deliver to, so nothing can be
 * claimed, and messageReceipt leaves such a message on 'sent'.
 */
function slowest(
  members: readonly ConversationMember[],
  userId: string,
  pick: (m: ConversationMember) => string | undefined,
): string {
  let oldest = ''
  for (const member of members) {
    if (member.userId === userId) continue // never wait on yourself
    const at = pick(member) ?? ''
    if (at === '') return '' // one member has never reported: nothing is through
    if (oldest === '' || at < oldest) oldest = at
  }
  return oldest
}

/**
 * The ticks for one of YOUR messages. Timestamps are ISO-8601 and so compare as strings.
 *
 * Only ever called for your own messages: a tick on someone else's would be telling them what they
 * already know.
 */
export function messageReceipt(
  createdAt: string,
  members: readonly ConversationMember[],
  userId: string,
): Receipt {
  const others = members.filter((m) => m.userId !== userId)
  if (others.length === 0) return 'sent'

  const readUpTo = slowest(members, userId, (m) => m.readAt)
  if (readUpTo !== '' && createdAt <= readUpTo) return 'read'

  const deliveredUpTo = slowest(members, userId, (m) => m.deliveredAt)
  if (deliveredUpTo !== '' && createdAt <= deliveredUpTo) return 'delivered'

  return 'sent'
}

/**
 * Applies a live receipt to a conversation's members.
 *
 * Forward only, mirroring the server: receipts arrive out of order — two devices, a retry, a
 * catch-up — and an older one must not un-read what the list already shows as read.
 */
export function applyReceipt(
  members: readonly ConversationMember[],
  receipt: { userId: string; deliveredAt?: string; readAt?: string },
): ConversationMember[] {
  return members.map((m) => {
    if (m.userId !== receipt.userId) return m
    const deliveredAt = later(m.deliveredAt, receipt.deliveredAt)
    const readAt = later(m.readAt, receipt.readAt)
    if (deliveredAt === m.deliveredAt && readAt === m.readAt) return m
    return { ...m, deliveredAt, readAt }
  })
}

function later(a: string | undefined, b: string | undefined): string | undefined {
  if (!a) return b
  if (!b) return a
  return b > a ? b : a
}
