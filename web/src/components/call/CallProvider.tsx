import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { api } from '../../lib/api'
import { Call, readInvite, type CallSnapshot } from '../../lib/call'
import { loadMlsDeviceId } from '../../lib/device'
import { useAuth } from '../../auth/context'
import { useEventStream } from '../../hooks/useEventStream'
import { notifyError } from '../../lib/notify'
import { PeerKeysMissingError, postCallEvent } from '../../lib/mls'
import type { CallEventOutcome } from '../../lib/callEvent'
import { useTranslation } from 'react-i18next'

import { CallContext, type CallContextValue } from './context'

/**
 * Writes a missed call into the conversation, so it leaves a trace.
 *
 * A call that rang out while you were away buzzed the phone once and then left nothing behind —
 * no way to see that anyone had wanted you, or who. That is what this fixes, and it is a real
 * encrypted message, not a UI flourish: it is in the history on every device, after a reload,
 * for both people.
 *
 * ONLY THE CALLER WRITES IT, and only for a call nobody picked up. The callee writing one too
 * would put the same missed call in the chat twice; and a call that was answered is not a missed
 * call, it is a conversation, and the two people who had it do not need to be told it happened.
 */
async function recordCall(s: CallSnapshot, userId: string): Promise<void> {
  if (!s.outgoing || s.seconds > 0) return

  const outcome: CallEventOutcome | null =
    s.reason === 'unanswered'
      ? 'missed'
      : s.reason === 'declined'
        ? 'declined'
        : s.reason === 'failed'
          ? 'failed'
          : null
  // Everything else — we hung up before it rang out, they were busy, it was answered on another
  // of their devices — is not a missed call and gets no entry.
  if (!outcome) return

  await postCallEvent(s.conversationId, userId, { outcome }).catch(() => {
    // The record is worth having and not worth failing a call over. If it does not land, the
    // call simply leaves no trace, which is where we were before.
  })
}

/**
 * Owns the one call this browser can be on.
 *
 * It lives above the routes, because a call outlives the conversation view: you can navigate
 * to another chat, or to the channel list, and still be talking. It also has to be listening
 * when nobody has the conversation open at all — that is what an incoming call IS.
 */
export function CallProvider({ children }: { children: ReactNode }) {
  const { userId } = useAuth()
  const { t } = useTranslation()
  const [snapshot, setSnapshot] = useState<CallSnapshot | null>(null)
  const [incoming, setIncoming] = useState<CallContextValue['incoming']>(null)

  // The live Call object. A ref, not state: it is not rendered, it must not be recreated by a
  // re-render, and the microphone it holds must be released exactly once.
  const callRef = useRef<Call | null>(null)
  // Calls we have already dealt with — answered, declined, or missed. Without this, every
  // re-read of the mailbox would ring again for a call the user has already dismissed.
  const handledRef = useRef<Set<string>>(new Set())

  const clear = useCallback(() => {
    callRef.current = null
    setSnapshot(null)
  }, [])

  const onChange = useCallback(
    (s: CallSnapshot) => {
      setSnapshot(s)
      if (s.status !== 'ended') return

      handledRef.current.add(s.callId)
      if (userId) void recordCall(s, userId)
      // Leave the ended state up for a moment so the user can see WHY it ended — declined,
      // unanswered, answered on another device — rather than having the window vanish.
      setTimeout(() => {
        if (callRef.current?.callId === s.callId) clear()
      }, 2500)
    },
    [clear, userId],
  )

  const place = useCallback(
    async (conversationId: string) => {
      if (!userId || callRef.current) return
      try {
        const call = await Call.place(conversationId, userId, onChange)
        callRef.current = call
        setSnapshot(call.snapshot())
        await call.invite()
      } catch (e) {
        callRef.current = null
        setSnapshot(null)
        if (e instanceof PeerKeysMissingError) {
          notifyError(t('chat.peerNotReady'), null)
          return
        }
        // The two that actually happen: the microphone was refused, and the server has calling
        // switched off. Both are worth saying out loud rather than a dead button.
        notifyError(t('call.failed'), e)
      }
    },
    [userId, onChange, t],
  )

  const answer = useCallback(async () => {
    if (!userId || !incoming || callRef.current) return
    const invite = incoming
    setIncoming(null)
    handledRef.current.add(invite.callId)

    const deviceId = loadMlsDeviceId()
    if (!deviceId) return

    try {
      const call = await Call.incoming(invite.conversationId, userId, invite.callId, onChange)
      callRef.current = call
      setSnapshot(call.snapshot())
      // Returns false when another of this user's devices picked up first. That is not an
      // error — it is the answer — and the Call has already put the microphone away.
      await call.answer(invite.sdp, deviceId)
    } catch (e) {
      callRef.current = null
      setSnapshot(null)
      notifyError(t('call.failed'), e)
    }
  }, [userId, incoming, onChange, t])

  const decline = useCallback(async () => {
    if (!userId || !incoming) return
    const invite = incoming
    setIncoming(null)
    handledRef.current.add(invite.callId)
    // A short-lived Call, purely to seal and send the refusal under the right key.
    const call = await Call.incoming(invite.conversationId, userId, invite.callId, () => {})
    await call.decline()
  }, [userId, incoming])

  const hangUp = useCallback(async () => {
    await callRef.current?.hangUp()
  }, [])

  const dismiss = useCallback(() => clear(), [clear])

  const setMuted = useCallback((muted: boolean) => {
    callRef.current?.setMuted(muted)
  }, [])

  const setInputDevice = useCallback(async (deviceId: string) => {
    await callRef.current?.setInputDevice(deviceId)
  }, [])

  const setOutputDevice = useCallback(async (deviceId: string) => {
    await callRef.current?.setOutputDevice(deviceId)
  }, [])

  // Incoming calls, and every signal for a call we are already on.
  useEventStream((e) => {
    const signal = e.callSignal
    if (!signal || !userId || !e.conversationId) return

    // A call we are on: let it read its own mailbox. This is only a nudge — the signal itself
    // is fetched, because the stream is allowed to drop events and a dropped SDP answer is a
    // call that silently never connects.
    if (callRef.current?.callId === signal.callId) {
      callRef.current.nudge()
      return
    }

    // A call WE placed, ringing on another of our own devices. Do not ring here.
    if (signal.fromUserId === userId) return
    if (handledRef.current.has(signal.callId)) return
    if (callRef.current || incoming) return // already busy; TODO: send `busy`

    // Read out of the event before the closure: inside it, TypeScript can no longer prove the
    // field is still there, and neither can we — the event object is not ours.
    const conversationId = e.conversationId
    const callId = signal.callId

    void (async () => {
      const signals = await api.callSignals(conversationId, callId, 0).catch(() => [])
      for (const s of signals) {
        const invite = await readInvite(conversationId, userId, callId, s.ciphertext)
        if (!invite) continue
        if (handledRef.current.has(callId)) return
        setIncoming({ conversationId, callId, sdp: invite.sdp, from: invite.from })
        return
      }
      // Somebody is calling — the nudge said so — and this device found nothing it could open.
      // readInvite has already said why. This says that it happened at all, which is the thing
      // you cannot infer from a phone that simply sits there not ringing.
      console.warn(
        `[call] nudged about call ${callId} but none of its ${signals.length} signal(s) ` +
          `could be read as an invite — this device will not ring`,
      )
    })().catch((e: unknown) => {
      console.warn(`[call] failed to handle the nudge for call ${callId}:`, e)
    })
  })

  // A call tapped from a push notification.
  //
  // The app was closed when it rang, so it never saw the invite go by on the live stream —
  // there was no live stream. The notification carries the call id instead, and the call is
  // read out of the mailbox, where it is still sitting.
  //
  // Runs once per call id: if the call has already gone (the caller gave up while the phone
  // was being unlocked) there is simply nothing in the mailbox, and nothing rings.
  useEffect(() => {
    if (!userId) return
    const params = new URLSearchParams(window.location.search)
    const callId = params.get('call')
    const conversationId = window.location.pathname.match(/^\/chats\/([^/]+)/)?.[1]
    if (!callId || !conversationId) return
    if (handledRef.current.has(callId) || callRef.current) return

    let live = true
    void (async () => {
      const signals = await api.callSignals(conversationId, callId, 0).catch(() => [])
      for (const s of signals) {
        const invite = await readInvite(conversationId, userId, callId, s.ciphertext)
        if (!invite || !live || handledRef.current.has(callId)) continue
        setIncoming({ conversationId, callId, sdp: invite.sdp, from: invite.from })
        return
      }
    })()
    return () => {
      live = false
    }
  }, [userId])

  // The microphone must not survive the tab. A refresh mid-call would otherwise leave the
  // recording indicator on until the browser noticed.
  useEffect(() => {
    const call = callRef
    return () => {
      void call.current?.end('hung-up', true)
    }
  }, [])

  return (
    <CallContext.Provider
      value={{
        call: snapshot,
        incoming,
        place,
        answer,
        decline,
        hangUp,
        dismiss,
        setMuted,
        setInputDevice,
        setOutputDevice,
      }}
    >
      {children}
    </CallContext.Provider>
  )
}
