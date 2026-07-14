// The record a call leaves behind in the conversation.
//
// A call that nobody answered has to leave a trace, or it did not happen: the phone buzzed
// once while you were away and there is nothing afterwards to say who wanted you. So the caller
// posts an ordinary chat message when a call ends without being answered.
//
// It is an ordinary message in every sense that matters — encrypted to the same MLS group, sent
// down the same path, stored the same way — and the server can no more read "Alice called Bob
// and he did not pick up" than it can read anything else they say. It is only marked out by its
// content type, which is the field the message model already uses to tell one kind of payload
// from another, and which the server treats as opaque.

/** The content type that marks a message as a call's record rather than something a human typed. */
export const CALL_EVENT = 'application/pheme-call-event'

export type CallEventOutcome =
  /** Rang out. Nobody picked up. */
  | 'missed'
  /** The other end refused it. */
  | 'declined'
  /** It never got a media path up — a network fault, not a decision. */
  | 'failed'

export interface CallEvent {
  outcome: CallEventOutcome
}

/** Encodes a call event as the body of a chat message. */
export function writeCallEvent(event: CallEvent): string {
  return JSON.stringify(event)
}

/**
 * Decodes a call event from a message body, or null if it is not one.
 *
 * Returns null rather than throwing on anything unexpected: a body that does not parse is a
 * message from a future version of the app, or a corrupt one, and neither is worth breaking the
 * whole transcript over. The bubble falls back to being an unremarkable message.
 */
export function readCallEvent(body: string): CallEvent | null {
  let parsed: unknown
  try {
    parsed = JSON.parse(body)
  } catch {
    return null
  }
  if (typeof parsed !== 'object' || parsed === null) return null
  const outcome = (parsed as { outcome?: unknown }).outcome
  if (outcome !== 'missed' && outcome !== 'declined' && outcome !== 'failed') return null
  return { outcome }
}
