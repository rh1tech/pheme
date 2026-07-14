// The cross-client contract for message content and photo encryption — the web half.
//
// The same vectors are asserted in the Flutter client, in mobile/test/unit/chat_content_test.dart.
// Both formats fail SILENTLY when the two clients disagree: a message arrives blank, or a photo will
// not decode, and nothing anywhere says why — both ends are doing exactly what they were told. So both
// are pinned on both sides. Change the format here and the mobile test fails; change it there and this
// one does.

import { describe, expect, it } from 'vitest'
import { deserializeContent, serializeContent, type ChatContent } from './chatContent'
import { openPhoto, sealPhotoBytes } from './photo'

const hex = (bytes: Uint8Array): string =>
  Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')

const unhex = (s: string): Uint8Array =>
  new Uint8Array((s.match(/../g) ?? []).map((b) => parseInt(b, 16)))

const decode = (bytes: Uint8Array): string => new TextDecoder().decode(bytes)
const encode = (s: string): Uint8Array => new TextEncoder().encode(s)

describe('content codec', () => {
  it('serialises a plain message exactly as the mobile client does', () => {
    expect(decode(serializeContent({ body: 'hello' }))).toBe('{"body":"hello"}')
  })

  it('omits absent fields rather than writing them as null', () => {
    const json = decode(serializeContent({ body: 'hi' }))
    expect(json).not.toContain('null')
    expect(json).not.toContain('replyTo')
    expect(json).not.toContain('photos')
  })

  it('carries only the id a reply replies to', () => {
    // Never the quoted TEXT. A client renders the quote from the message it already holds; copying the
    // text in would let a sender quote you as saying anything at all, with no way for the recipient
    // to check.
    expect(decode(serializeContent({ body: 'agreed', replyTo: 'msg-1' }))).toBe(
      '{"body":"agreed","replyTo":"msg-1"}',
    )
  })

  it('carries a photo key inside the message and nowhere else', () => {
    const content: ChatContent = {
      body: 'look',
      photos: [{ id: 'blob-1', key: 'a2V5', w: 1200, h: 800, mime: 'image/jpeg', size: 4096 }],
    }
    expect(decode(serializeContent(content))).toBe(
      '{"body":"look","photos":[{"id":"blob-1","key":"a2V5","w":1200,' +
        '"h":800,"mime":"image/jpeg","size":4096}]}',
    )
  })

  // An older client must still read a message from a newer one. Showing what we understand and
  // ignoring the rest is the difference between "a photo I cannot see yet" and "a blank bubble".
  it('still shows what it understands of a message from a newer client', () => {
    const future = encode('{"body":"hi","replyTo":"m1","reactions":["🎉"],"video":{"id":"v1"}}')
    const read = deserializeContent(future)
    expect(read?.body).toBe('hi')
    expect(read?.replyTo).toBe('m1')
  })

  it('drops a malformed photo rather than losing the whole message', () => {
    const mixed = encode(
      '{"body":"two of three","photos":[' +
        '{"id":"a","key":"k","w":1,"h":1,"mime":"image/jpeg","size":1},' +
        '{"id":"","key":"k","mime":"image/jpeg"},' +
        '{"key":"k","mime":"image/jpeg"},' +
        '{"id":"c","key":"k","w":1,"h":1,"mime":"image/jpeg","size":1}]}',
    )
    const read = deserializeContent(mixed)
    expect(read?.body).toBe('two of three')
    expect(read?.photos?.map((p) => p.id)).toEqual(['a', 'c'])
  })
})

describe('photo encryption golden vector', () => {
  /** 40 41 42 … 5f */
  const KEY = new Uint8Array(32).map((_, i) => 0x40 + i)

  /** c0 c1 c2 … cb. Fixed ONLY so the vector reproduces; a real photo gets 12 random bytes. */
  const NONCE = new Uint8Array(12).map((_, i) => 0xc0 + i)

  const PLAINTEXT = 'a tiny pretend jpeg'
  const KEY_B64 = 'QEFCQ0RFRkdISUpLTE1OT1BRUlNUVVZXWFlaW1xdXl8='

  /** nonce ‖ ciphertext ‖ tag. */
  const SEALED_HEX =
    'c0c1c2c3c4c5c6c7c8c9cacb3e6444a714e31d9b06dc5053c5c782a7ef903998' +
    'cba5a38a447d297e638a7f9282561b'

  it('seals to the bytes the mobile client expects', async () => {
    const sealed = await sealPhotoBytes(KEY, NONCE, encode(PLAINTEXT))
    expect(hex(sealed)).toBe(SEALED_HEX)
  })

  it('opens a photo sealed by the mobile client', async () => {
    const opened = await openPhoto(KEY_B64, unhex(SEALED_HEX))
    expect(decode(opened)).toBe(PLAINTEXT)
  })

  it('prepends the nonce, so a blob is one opaque thing', async () => {
    const sealed = await sealPhotoBytes(KEY, NONCE, encode(PLAINTEXT))
    expect(hex(sealed.slice(0, 12))).toBe(hex(NONCE))
  })

  it('refuses a photo under the wrong key', async () => {
    const wrong = btoa(String.fromCharCode(...new Uint8Array(32)))
    await expect(openPhoto(wrong, unhex(SEALED_HEX))).rejects.toThrow()
  })

  it('refuses a tampered photo', async () => {
    // The AAD binds the blob's purpose and the tag covers every byte. Flip one and it is gone.
    const tampered = unhex(SEALED_HEX)
    tampered[20] ^= 0x01
    await expect(openPhoto(KEY_B64, tampered)).rejects.toThrow()
  })
})
