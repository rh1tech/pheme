// The content codec for conversation messages.
//
// This is the single seam between the app and message encryption. Today it is
// plaintext-JSON, base64-wrapped, so chats work end to end before the crypto
// lands. In Phase 3 the two functions below become encrypt/decrypt against the
// MLS core (crates/pheme-mls → WASM); NOTHING else in the app changes, because
// everywhere else already treats a message's content as an opaque `ciphertext`
// string produced and consumed only here.
//
// Not yet private: until this file encrypts, the server can read message content.
// Do not present Phase 2 chats as encrypted.

/** The decoded shape of a conversation message. Images arrive in Phase 3. */
export interface ChatContent {
  body: string
}

const CONTENT_TYPE = 'application/json'

function toBase64(bytes: Uint8Array): string {
  let binary = ''
  for (const b of bytes) binary += String.fromCharCode(b)
  return btoa(binary)
}

function fromBase64(base64: string): Uint8Array {
  const binary = atob(base64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes
}

/** Serialises content into the opaque `{ciphertext, contentType}` the API stores. */
export function encodeChatContent(content: ChatContent): {
  ciphertext: string
  contentType: string
} {
  const json = JSON.stringify(content)
  const bytes = new TextEncoder().encode(json)
  return { ciphertext: toBase64(bytes), contentType: CONTENT_TYPE }
}

/** Recovers content from a wire message. Returns null when it cannot be read. */
export function decodeChatContent(ciphertext: string, contentType: string): ChatContent | null {
  // Phase 3 will dispatch on contentType (application/mls vs the plaintext JSON
  // here). Today only the JSON codec exists; an unknown type is unreadable.
  if (contentType !== CONTENT_TYPE) return null
  try {
    const json = new TextDecoder().decode(fromBase64(ciphertext))
    const parsed: unknown = JSON.parse(json)
    if (typeof parsed !== 'object' || parsed === null) return null
    const body = (parsed as { body?: unknown }).body
    return typeof body === 'string' ? { body } : null
  } catch {
    return null
  }
}
