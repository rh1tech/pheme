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
      // Record that this REQUESTER has an offer, so an in-flight election for the same requester
      // cancels. Keyed by who the offer is addressed to, not by message id: keying it per message
      // meant any offer in the conversation stood every candidate down, so the next device to ask
      // was met with silence for the rest of the session.
      offered.current.add(`${conversationId}:${controlField(msg.ciphertext, 'to')}`)
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
    const key = `${conversationId}:${msg.id}`
    if (answering.current.has(key)) return

    void (async () => {
      const identity = await myIdentity(userId).catch(() => '')
      const requester = requesterIdentityOf(msg.ciphertext)
      const members = await groupMemberIdentities(conversationId, userId).catch(() => [])
      if (!shouldAnswer(requester, identity, members.length > 0)) return

      // Rank among the current members determines our election delay. Lower identity → sooner.
      const rank = [...members].sort().indexOf(identity)
      const delay = ELECTION_STEP_MS * (rank < 0 ? members.length : rank)
      const timer = setTimeout(() => {
        answering.current.delete(key)
        // Someone already answered THIS requester — stand down. An offer made to somebody else
        // says nothing about whether this one still needs help.
        if (offered.current.has(`${conversationId}:${requester}`)) return
        void offerHistory(conversationId, userId, requester)
      }, delay)
      answering.current.set(key, timer)
    })()
  })
}

/**
 * Whether THIS device should stand as a candidate to answer a history request.
 *
 * Answering is decided by DEVICE, not by user — deliberately, and this is the whole point of the
 * function. A history request usually carries our OWN user id, because the device asking is our
 * other one, and the device most likely to be online when somebody signs in on a new phone is that
 * same person's existing device. Standing down on "our own user" left a 1:1 conversation with
 * exactly one permitted responder — the other participant — who may be on another host entirely
 * and offline; the new device then held the group and none of its history, indefinitely, showing
 * every message as undecryptable. That is the same mistake that was fixed in useDeviceAdmission.
 *
 * Only the request from THIS device is ours to ignore. Answering is otherwise safe even when we
 * turn out to have nothing useful: offerHistory no-ops on a device that does not hold the group,
 * holds no transcript, or is the requester itself.
 */
export function shouldAnswer(
  requester: string,
  myIdentity: string,
  holdsGroup: boolean,
): boolean {
  if (!requester || !myIdentity) return false // malformed request, or we have no identity yet
  if (requester === myIdentity) return false // our own device asking; nothing to hand ourselves
  return holdsGroup // without the group we cannot derive the seal, so we cannot help
}

/**
 * Reads one string field out of a history control body (base64 JSON). Empty on a malformed one,
 * which every caller treats as "not addressed to anyone we can act on".
 *
 * A request names its sender in `id`; an offer names its addressee in `to`.
 */
export function controlField(ciphertextBase64: string, field: 'id' | 'to'): string {
  try {
    const json = atob(ciphertextBase64)
    const bytes = new Uint8Array(json.length)
    for (let i = 0; i < json.length; i++) bytes[i] = json.charCodeAt(i)
    const parsed = JSON.parse(new TextDecoder().decode(bytes)) as Record<string, unknown>
    const value = parsed[field]
    return typeof value === 'string' ? value : ''
  } catch {
    return ''
  }
}

/** Pulls the requester's identity out of a history-request control body. */
function requesterIdentityOf(ciphertextBase64: string): string {
  return controlField(ciphertextBase64, 'id')
}
