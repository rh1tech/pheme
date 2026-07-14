// The inner content codec for conversation messages: the shape of a message once decrypted,
// serialised to the bytes that MLS then encrypts.
//
// Encryption itself lives in lib/mls.ts — this only turns a message into bytes and back. Keeping the
// two apart means the wire format of a message body is independent of the crypto that wraps it.
//
// ------------------------------------------------------------------------------------------------
// THIS SHAPE IS A CROSS-CLIENT CONTRACT. The Flutter app has the same codec in
// mobile/lib/src/crypto/chat_content.dart, and the two have to agree — a field one writes and the
// other cannot read is a message that arrives blank on somebody's phone.
//
// It is also why every field but `body` is OPTIONAL, and why parsing is lenient about extras: a
// client that has not been updated yet must still be able to read a message from one that has,
// showing what it understands and quietly ignoring the rest. A photo sent from a phone renders as
// its caption on an old web tab. It does not render as nothing.
// ------------------------------------------------------------------------------------------------

/**
 * One photo, as it appears inside an encrypted message.
 *
 * THE KEY IS HERE, AND THAT IS THE WHOLE DESIGN. The photo is AES-GCM ciphertext sitting in the
 * server's blob store; the key that opens it exists only inside this message, which is itself
 * end-to-end encrypted. So the server holds a blob it cannot open and never receives the key, and the
 * two never meet anywhere it can reach.
 *
 * The dimensions and the mime type are here for the same reason: they are properties of the
 * PLAINTEXT, and the server has no business knowing them. It stores the ciphertext as
 * application/octet-stream and learns nothing but a size.
 */
export interface ChatPhoto {
  /** The blob id, from POST /v1/conversations/{id}/attachments. */
  id: string
  /** base64 AES-256-GCM key, 32 bytes. Fresh for every photo — never reused. */
  key: string
  /** Pixel dimensions, so a bubble can reserve the right space before the bytes arrive. */
  w: number
  h: number
  mime: string
  /** Size of the plaintext. */
  size: number
}

/** The decoded shape of a conversation message. */
export interface ChatContent {
  body: string
  /**
   * The message this one replies to.
   *
   * Just an id. The quoted text is NOT copied in, and that is deliberate: a client renders the quote
   * from the message it already holds, and if it does not hold that message — a device that joined
   * after it was sent, and so can never decrypt it — it says so, rather than showing a quote it
   * cannot verify. Copying the text in would be a lie waiting to happen, because the sender could
   * quote you as having said anything at all and the recipient would have no way to check.
   */
  replyTo?: string
  photos?: ChatPhoto[]
}

/** Serialises message content to the bytes MLS will encrypt. */
export function serializeContent(content: ChatContent): Uint8Array {
  const out: ChatContent = { body: content.body }
  if (content.replyTo) out.replyTo = content.replyTo
  if (content.photos?.length) out.photos = content.photos
  return new TextEncoder().encode(JSON.stringify(out))
}

/** Parses decrypted bytes back into content. Returns null on garbage. */
export function deserializeContent(bytes: Uint8Array): ChatContent | null {
  try {
    const parsed: unknown = JSON.parse(new TextDecoder().decode(bytes))
    if (typeof parsed !== 'object' || parsed === null) return null

    const raw = parsed as Record<string, unknown>
    if (typeof raw.body !== 'string') return null

    const content: ChatContent = { body: raw.body }
    if (typeof raw.replyTo === 'string' && raw.replyTo) content.replyTo = raw.replyTo

    const photos = parsePhotos(raw.photos)
    if (photos.length > 0) content.photos = photos

    return content
  } catch {
    return null
  }
}

/**
 * Reads the photo list, dropping anything malformed rather than failing the whole message.
 *
 * A message with one broken photo, three good ones and a line of text should show the text and the
 * three. Rejecting the lot — because one entry is malformed, or because a future client added a field
 * this one does not understand — throws away more than it protects.
 */
function parsePhotos(value: unknown): ChatPhoto[] {
  if (!Array.isArray(value)) return []

  const out: ChatPhoto[] = []
  for (const entry of value) {
    if (typeof entry !== 'object' || entry === null) continue
    const p = entry as Record<string, unknown>

    if (typeof p.id !== 'string' || !p.id) continue
    if (typeof p.key !== 'string' || !p.key) continue
    if (typeof p.mime !== 'string' || !p.mime) continue

    out.push({
      id: p.id,
      key: p.key,
      w: typeof p.w === 'number' ? p.w : 0,
      h: typeof p.h === 'number' ? p.h : 0,
      mime: p.mime,
      size: typeof p.size === 'number' ? p.size : 0,
    })
  }
  return out
}
