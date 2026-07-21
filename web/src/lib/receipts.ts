// What the ticks on your own message mean, worked out from the other members' watermarks.
//
// The server tracks how far each member has got — received up to deliveredSeq, read up to readSeq —
// rather than a state per message per member. Messages are ordered by their per-conversation `seq`,
// so "read up to N" already says everything about every message at or before N, and a message's
// ticks are then a comparison: delivered once EVERY other member has received it, read once every
// other member has read it. That is the "everyone has read it" rule, and in a 1:1 chat "everyone"
// is just them.

import type { ConversationMember } from './types'

/** Nothing yet, one tick (everyone received it), or two (everyone read it). */
export type Receipt = 'sent' | 'delivered' | 'read'

/**
 * The point every member OTHER than `userId` has reached — the lowest of their watermarks, because
 * a message is only through once the slowest of them has it.
 *
 * 0 when there is nobody else (everyone left): there is no one to deliver to, so nothing can be
 * claimed, and messageReceipt leaves such a message on 'sent'. Also 0 the moment any one member
 * has never reported (watermark absent or 0): nothing is through until the slowest catches up.
 */
function slowest(
  members: readonly ConversationMember[],
  userId: string,
  pick: (m: ConversationMember) => number | undefined,
): number {
  let lowest = 0
  let seen = false
  for (const member of members) {
    if (member.userId === userId) continue // never wait on yourself
    const seq = pick(member) ?? 0
    if (seq === 0) return 0 // one member has never reported: nothing is through
    if (!seen || seq < lowest) {
      lowest = seq
      seen = true
    }
  }
  return lowest
}

/**
 * The ticks for one of YOUR messages, comparing its `seq` against the other members' watermarks.
 *
 * Only ever called for your own messages: a tick on someone else's would be telling them what they
 * already know.
 */
export function messageReceipt(
  seq: number,
  members: readonly ConversationMember[],
  userId: string,
): Receipt {
  const others = members.filter((m) => m.userId !== userId)
  if (others.length === 0) return 'sent'

  const readUpTo = slowest(members, userId, (m) => m.readSeq)
  if (readUpTo !== 0 && seq <= readUpTo) return 'read'

  const deliveredUpTo = slowest(members, userId, (m) => m.deliveredSeq)
  if (deliveredUpTo !== 0 && seq <= deliveredUpTo) return 'delivered'

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
  receipt: { userId: string; deliveredSeq?: number; readSeq?: number },
): ConversationMember[] {
  return members.map((m) => {
    if (m.userId !== receipt.userId) return m
    const deliveredSeq = higher(m.deliveredSeq, receipt.deliveredSeq)
    const readSeq = higher(m.readSeq, receipt.readSeq)
    if (deliveredSeq === m.deliveredSeq && readSeq === m.readSeq) return m
    return { ...m, deliveredSeq, readSeq }
  })
}

function higher(a: number | undefined, b: number | undefined): number | undefined {
  if (a === undefined) return b
  if (b === undefined) return a
  return b > a ? b : a
}
