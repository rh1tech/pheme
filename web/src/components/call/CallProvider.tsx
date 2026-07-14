import { useCallback, useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { api } from '../../lib/api'
import { Call, readInvite, type CallSnapshot } from '../../lib/call'
import { loadMlsDeviceId } from '../../lib/device'
import { useAuth } from '../../auth/context'
import { useEventStream } from '../../hooks/useEventStream'
import { notifyError } from '../../lib/notify'
import { PeerKeysMissingError } from '../../lib/mls'
import { useTranslation } from 'react-i18next'

import { CallContext, type CallContextValue } from './context'

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
      if (s.status === 'ended') {
        handledRef.current.add(s.callId)
        // Leave the ended state up for a moment so the user can see WHY it ended — declined,
        // unanswered, answered on another device — rather than having the window vanish.
        setTimeout(() => {
          if (callRef.current?.callId === s.callId) clear()
        }, 2500)
      }
    },
    [clear],
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
    })()
  })

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
      }}
    >
      {children}
    </CallContext.Provider>
  )
}
