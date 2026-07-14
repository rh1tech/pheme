// Encrypting a photo, and getting it back.
//
// The construction, and the reason for each part:
//
//   * a FRESH 32-byte key per photo, never reused. Two photos under one key is one nonce collision
//     away from leaking both, and there is no reason to economise on 32 bytes.
//   * a FRESH 12-byte random nonce, PREPENDED to the ciphertext. Prepending means the caller stores
//     one opaque blob and can never get the two out of step — the nonce is not a secret, it just must
//     not repeat.
//   * AES-256-GCM, with the blob's purpose bound in as additional data, so a blob cannot be passed
//     off as something else sealed under the same key.
//
// The sealed bytes go to the server, which stores them as application/octet-stream and cannot open
// them. The KEY goes inside the MLS-encrypted message that references the photo — so the server holds
// the lock and never sees the key, and the two never meet anywhere it can reach.
//
// This format is a cross-client contract: mobile/lib/src/crypto/photo_crypto.dart must produce
// exactly the same bytes, and a golden vector on both sides pins it.

/** Bound in as AES-GCM additional data. A photo blob cannot be passed off as anything else. */
const PHOTO_AAD = new TextEncoder().encode('pheme.photo.v1')

const NONCE_BYTES = 12
const KEY_BYTES = 32

/** The largest edge a photo is scaled to before it is sealed. */
const MAX_EDGE = 1600

/** JPEG quality for the re-encode. */
const QUALITY = 0.82

export interface SealedPhoto {
  /** nonce ‖ ciphertext ‖ tag. What the server stores. */
  bytes: Uint8Array
  /** base64 of the raw key. Goes inside the encrypted message, never to the server. */
  key: string
  width: number
  height: number
  mime: string
  /** Size of the PLAINTEXT, for the UI. */
  size: number
}

/** A fresh key. Never derived, never reused — a photo has nothing to do with any other photo. */
function newKey(): Uint8Array {
  return crypto.getRandomValues(new Uint8Array(KEY_BYTES))
}

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

/** Seals already-encoded image bytes. Split out so a golden vector can pin it with a fixed key. */
export async function sealPhotoBytes(
  key: Uint8Array,
  nonce: Uint8Array,
  plaintext: Uint8Array,
): Promise<Uint8Array> {
  const k = await crypto.subtle.importKey('raw', key as BufferSource, 'AES-GCM', false, ['encrypt'])
  const sealed = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv: nonce as BufferSource, additionalData: PHOTO_AAD as BufferSource },
    k,
    plaintext as BufferSource,
  )

  const out = new Uint8Array(nonce.length + sealed.byteLength)
  out.set(nonce, 0)
  out.set(new Uint8Array(sealed), nonce.length)
  return out
}

/**
 * Opens a sealed photo.
 *
 * Throws when the key is wrong or the bytes were tampered with. There is no lenient path: a photo
 * that does not open is not the photo that was sent, and showing something else would be worse than
 * showing nothing.
 */
export async function openPhoto(keyBase64: string, sealed: Uint8Array): Promise<Uint8Array> {
  if (sealed.length <= NONCE_BYTES) throw new Error('photo is truncated')

  const nonce = sealed.slice(0, NONCE_BYTES)
  const ciphertext = sealed.slice(NONCE_BYTES)

  const k = await crypto.subtle.importKey(
    'raw',
    fromBase64(keyBase64) as BufferSource,
    'AES-GCM',
    false,
    ['decrypt'],
  )
  const opened = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: nonce as BufferSource, additionalData: PHOTO_AAD as BufferSource },
    k,
    ciphertext as BufferSource,
  )
  return new Uint8Array(opened)
}

/**
 * Downscales, re-encodes and seals a photo the user picked.
 *
 * The re-encode is not only about size. Drawing to a canvas and reading it back STRIPS THE METADATA —
 * the EXIF block, which on a phone photo routinely carries the GPS coordinates of where it was taken,
 * the device, and the timestamp. Sending an end-to-end encrypted photo with the sender's home address
 * inside it would be a fine joke at our expense, and the only reliable way to prevent it is to never
 * ship the original bytes at all.
 */
export async function preparePhoto(file: File): Promise<SealedPhoto> {
  const bitmap = await createImageBitmap(file)

  const scale = Math.min(1, MAX_EDGE / Math.max(bitmap.width, bitmap.height))
  const width = Math.max(1, Math.round(bitmap.width * scale))
  const height = Math.max(1, Math.round(bitmap.height * scale))

  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height

  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('could not prepare that photo')
  ctx.drawImage(bitmap, 0, 0, width, height)
  bitmap.close()

  const blob = await new Promise<Blob | null>((resolve) =>
    canvas.toBlob(resolve, 'image/jpeg', QUALITY),
  )
  if (!blob) throw new Error('could not prepare that photo')

  const plaintext = new Uint8Array(await blob.arrayBuffer())
  const key = newKey()
  const nonce = crypto.getRandomValues(new Uint8Array(NONCE_BYTES))

  return {
    bytes: await sealPhotoBytes(key, nonce, plaintext),
    key: toBase64(key),
    width,
    height,
    mime: 'image/jpeg',
    size: plaintext.length,
  }
}
