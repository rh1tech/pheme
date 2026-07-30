import { useEffect, useRef } from 'react'
import {
  MLS_HISTORY_OFFER,
  MLS_HISTORY_REQUEST,
  groupMemberIdentities,
  myIdentity,
  offerHistory,
  readHistoryOffer,
  readHistoryRequest,
  receiveHistoryOffer,
} from '../lib/mls'
import { sameAccount } from '../lib/historyHandoff'
import { useEventStream } from './useEventStream'

/** Fired after history is imported for a conversation, so an open feed repaints without a reopen. */
export const HISTORY_IMPORTED_EVENT = 'pheme:history-imported'

/**
 * Hands a newly-joined device the history it holds none of, device to device.
 *
 * A device that joins a conversation posts a history REQUEST (see requestHistory). Every co-member
 * that holds the group hears it here; ONE of them answers with a sealed blob (see offerHistory), and
 * the requester opens it (receiveHistoryOffer). All of it is sealed under a key derived from the
 * group — the server relays pointers, never content.
 *
 * Mounted once, app-wide, so a request is answered whoever has the app open — not only someone
 * looking at the conversation it concerns.
 *
 * ELECTION. Only existing devices of the requester's own account may answer: a valid leaf
 * signature proves group membership, but any participant could otherwise sign invented history as
 * themselves. Each eligible device waits a short rank-based delay and cancels if one already won.
 */

/** How long each rank-step of the election waits, in ms. Small — the whole point is a prompt handoff. */
const ELECTION_STEP_MS = 400

export function useHistorySync(userId: string | null): void {
  // Requests we have already scheduled a response to, so a repeated event does not double-answer.
  const answering = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map())
  // Offers seen per requester, so an elected candidate can tell someone already answered.
  const offered = useRef<Set<string>>(new Set())

  useEffect(() => {
    const timers = answering.current
    const seen = offered.current
    return () => {
      for (const t of timers.values()) clearTimeout(t)
      timers.clear()
      seen.clear()
    }
  }, [userId])

  useEventStream((e) => {
    if (!userId || !e.conversationId || !e.chatMessage) return
    const msg = e.chatMessage
    const conversationId = e.conversationId

    if (msg.contentType === MLS_HISTORY_OFFER) {
      // Record that this REQUESTER has an offer, so an in-flight election for the same requester
      // cancels. Keyed by who the offer is addressed to, not by message id: keying it per message
      // meant any offer in the conversation stood every candidate down, so the next device to ask
      // was met with silence for the rest of the session.
      offered.current.add(`${conversationId}:${readHistoryOffer(msg.ciphertext)?.to ?? ''}`)
      // Try to receive it — receiveHistoryOffer refuses offers not addressed to this device, and
      // every offer whose signature does not verify against the offering member's leaf key. The
      // envelope's senderId goes with it: the server authenticates the POSTER, which is a second,
      // independent witness alongside the MLS signature.
      void receiveHistoryOffer(conversationId, userId, msg.ciphertext, msg.senderId).then((imported) => {
        // A fresh cache write does not re-render an open conversation on its own; nudge it so the
        // history that just arrived paints without a reopen.
        if (imported) {
          window.dispatchEvent(
            new CustomEvent(HISTORY_IMPORTED_EVENT, { detail: { conversationId } }),
          )
        }
      })
      return
    }

    if (msg.contentType !== MLS_HISTORY_REQUEST) return
    const key = `${conversationId}:${msg.id}`
    if (answering.current.has(key)) return

    void (async () => {
      const identity = await myIdentity(userId).catch(() => '')
      // v1 requests — unsigned — parse to null and are never answered. Answering one means sealing
      // a whole conversation to a key derived for an identity that may never have asked for it.
      const request = readHistoryRequest(msg.ciphertext)
      if (!request) return
      const requester = request.id
      const members = await groupMemberIdentities(conversationId, userId).catch(() => [])
      const eligible = members.filter((member) => sameAccount(member, requester))
      if (!shouldAnswer(requester, identity, eligible.length > 0)) return

      // Rank among this account's eligible devices determines the delay. Other participants are
      // intentionally excluded: their signatures authenticate them, not the transcript they claim.
      const rank = [...eligible].sort().indexOf(identity)
      const delay = ELECTION_STEP_MS * (rank < 0 ? eligible.length : rank)
      const timer = setTimeout(() => {
        answering.current.delete(key)
        // Someone already answered THIS requester — stand down. An offer made to somebody else
        // says nothing about whether this one still needs help.
        if (offered.current.has(`${conversationId}:${requester}`)) return
        void offerHistory(conversationId, userId, request, msg.senderId)
      }, delay)
      answering.current.set(key, timer)
    })()
  })
}

/**
 * Whether THIS device should stand as a candidate to answer a history request.
 *
 * Answering is decided by DEVICE within the SAME ACCOUNT. The request normally comes from our
 * other device; a different participant is never eligible because their valid leaf signature
 * cannot prove that the per-message plaintext they supply is historically accurate.
 */
export function shouldAnswer(
  requester: string,
  myIdentity: string,
  holdsGroup: boolean,
): boolean {
  if (!requester || !myIdentity) return false // malformed request, or we have no identity yet
  if (requester === myIdentity) return false // our own device asking; nothing to hand ourselves
  return holdsGroup && sameAccount(requester, myIdentity)
}
