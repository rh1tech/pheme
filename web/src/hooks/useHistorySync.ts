import { useEffect, useRef } from 'react'
import {
  MLS_HISTORY_OFFER,
  MLS_HISTORY_REQUEST,
  groupMemberIdentities,
  myIdentity,
  offerHistory,
  receiveHistoryOffer,
} from '../lib/mls'
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
 * ELECTION. Every co-member could answer; we want ~one. Each candidate waits a short delay keyed by
 * its RANK among the group's members (lowest identity answers soonest), and cancels if an offer for
 * the same requester has meanwhile appeared. First writer wins; the rest suppress. This tolerates the
 * lossy event stream without needing to know who else is online.
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
      // Record that this requester has an offer, so an in-flight election cancels. The body is
      // opaque here (the receiver parses it); a stable per-message key is enough to dedupe.
      offered.current.add(`${conversationId}:${msg.id}`)
      // Try to receive it — receiveHistoryOffer ignores offers not addressed to this device.
      void receiveHistoryOffer(conversationId, userId, msg.ciphertext).then((imported) => {
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
    // Our own request (this device asking) — nothing to answer.
    const key = `${conversationId}:${msg.id}`
    if (answering.current.has(key)) return

    void (async () => {
      const identity = await myIdentity(userId).catch(() => '')
      if (msg.senderId === userId) return // our own user's request — a co-member of another user answers
      const members = await groupMemberIdentities(conversationId, userId).catch(() => [])
      if (members.length === 0 || !identity) return // we do not hold the group; cannot help

      // Rank among the current members determines our election delay. Lower identity → sooner.
      const rank = [...members].sort().indexOf(identity)
      const delay = ELECTION_STEP_MS * (rank < 0 ? members.length : rank)
      const timer = setTimeout(() => {
        answering.current.delete(key)
        // Someone already answered this requester — stand down.
        for (const seen of offered.current) if (seen.startsWith(`${conversationId}:`)) return
        void offerHistory(conversationId, userId, requesterIdentityOf(msg.ciphertext))
      }, delay)
      answering.current.set(key, timer)
    })()
  })
}

/** Pulls the requester's identity out of a history-request control body. Empty on a malformed one. */
function requesterIdentityOf(ciphertextBase64: string): string {
  try {
    const json = atob(ciphertextBase64)
    const bytes = new Uint8Array(json.length)
    for (let i = 0; i < json.length; i++) bytes[i] = json.charCodeAt(i)
    const parsed = JSON.parse(new TextDecoder().decode(bytes)) as { id?: string }
    return parsed.id ?? ''
  } catch {
    return ''
  }
}
