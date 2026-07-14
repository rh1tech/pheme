import { createContext, useContext } from 'react'
import type { CallSnapshot } from '../../lib/call'

/**
 * The one call this browser can be on.
 *
 * The context lives here rather than beside the provider so the provider file exports only
 * components — React's fast refresh cannot handle a file that mixes the two, and a broken
 * refresh in the middle of a call is a dropped call.
 */
export interface CallContextValue {
  /** The call in progress, or null. */
  call: CallSnapshot | null
  /** An incoming call that has not been answered or declined yet. */
  incoming: {
    conversationId: string
    callId: string
    sdp: string
    from: string
  } | null
  place: (conversationId: string) => Promise<void>
  answer: () => Promise<void>
  decline: () => Promise<void>
  hangUp: () => Promise<void>
  dismiss: () => void
  /** Stops sending the microphone. The track stays in the connection and transmits silence. */
  setMuted: (muted: boolean) => void
  /** Switches the microphone mid-call, without renegotiating. */
  setInputDevice: (deviceId: string) => Promise<void>
  /** Sends the call's audio to a chosen speaker. Unsupported in Safari — see canChooseOutput. */
  setOutputDevice: (deviceId: string) => Promise<void>
}

export const CallContext = createContext<CallContextValue | null>(null)

export function useCalls(): CallContextValue {
  const ctx = useContext(CallContext)
  if (!ctx) throw new Error('useCalls must be used inside a CallProvider')
  return ctx
}
