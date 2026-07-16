import { useEffect, useRef } from 'react'
import { MLS_DEVICE, admitAnnouncedDevice } from '../lib/mls'
import { useEventStream } from './useEventStream'

/**
 * Lets somebody's newly signed-in device into the encrypted groups it belongs to — from
 * anywhere in the app, not just from the conversation it happens to concern.
 *
 * A device cannot add itself: only a member who already holds the group can Commit. So a new
 * phone announces itself and waits for one of them to notice. The question is who is
 * listening, and the answer used to be "only somebody with that exact chat open on screen".
 * That is a deadlock dressed up as a race: two people rarely have the same conversation open
 * at the same moment, and the device that announced simply sat there — unable to read
 * anything, unable to send, and telling its owner that encryption was still being set up.
 *
 * Mounted once, above the routes. Anyone with the app open at all will now admit an announced
 * device, whichever conversation it is for.
 */
export function useDeviceAdmission(userId: string | null): void {
  // Announcements already handled, so a repeated event does not start a second reconcile.
  const handled = useRef<Set<string>>(new Set())

  useEffect(() => {
    handled.current = new Set()
  }, [userId])

  useEventStream((e) => {
    if (!userId || !e.conversationId || !e.chatMessage) return
    if (e.chatMessage.contentType !== MLS_DEVICE) return
    // No sender check — deliberately. The announcement may carry OUR user id, and it must
    // still be acted on: it is our own OTHER device asking to be let in, and the device most
    // likely to be around to admit somebody's new phone is that same person's old one. The
    // announcing device itself also receives its own announce, and that is harmless —
    // admitAnnouncedDevice no-ops on a device that does not hold the group. Skipping "our
    // own" events here meant a second device could only ever be admitted by the OTHER
    // participant, and a person alone in the app could never bring a new device in at all.

    const key = `${e.conversationId}:${e.chatMessage.id}`
    if (handled.current.has(key)) return
    handled.current.add(key)

    // Everyone holding the group will try, and they will race. That is fine and intended: the
    // server's compare-and-set lets exactly one Commit through, and the rest find the device
    // already added and stop.
    void admitAnnouncedDevice(e.conversationId, userId).catch(() => {})
  })
}
