// The inner content codec for conversation messages: the shape of a message
// once decrypted, serialised to the bytes that MLS then encrypts.
//
// Encryption itself lives in lib/mls.ts — this only turns a message into bytes
// and back. Keeping the two apart means the wire format of a message body is
// independent of the crypto that wraps it.

/** The decoded shape of a conversation message. Images arrive in a later phase. */
export interface ChatContent {
  body: string
}

/** Serialises message content to the bytes MLS will encrypt. */
export function serializeContent(content: ChatContent): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(content))
}

/** Parses decrypted bytes back into content. Returns null on garbage. */
export function deserializeContent(bytes: Uint8Array): ChatContent | null {
  try {
    const parsed: unknown = JSON.parse(new TextDecoder().decode(bytes))
    if (typeof parsed !== 'object' || parsed === null) return null
    const body = (parsed as { body?: unknown }).body
    return typeof body === 'string' ? { body } : null
  } catch {
    return null
  }
}
