// The wire format of a call signal, and the only place call signalling is encrypted.
//
// A signal is an envelope: a small cleartext header, and a sealed body.
//
//   { h: {...}, n: "<nonce>", c: "<ciphertext>" }
//
// The HEADER is readable by the server, and has to be. It carries which call this is, which
// epoch the key was derived at, which device sent it, and a sequence number — all things a
// receiver needs before it can decrypt anything, and none of which tell the server what is
// being said. It is also bound into the ciphertext as AES-GCM additional data, so a server
// that rewrote any of it would make the body fail to open rather than quietly redirect it.
//
// The BODY is what matters: the SDP. It carries the DTLS fingerprint that WebRTC's own media
// encryption is authenticated against, so a server able to rewrite it could substitute its
// own fingerprint and sit in the middle of the call, listening. Sealing it under a key
// derived from the conversation's MLS group — which the server does not have — is what makes
// that impossible, and it is the whole reason this file exists.

import { base64ToBytes, bytesToBase64 } from './mls'

/** What a signal is. Only `epoch-mismatch` is ever sent in the clear (see below). */
export type CallKind =
  | 'invite'
  | 'answer'
  | 'decline'
  | 'busy'
  | 'hangup'
  | 'epoch-mismatch'

/** The sealed part: what the two people are actually saying to each other. */
export interface CallBody {
  kind: Exclude<CallKind, 'epoch-mismatch'>
  /** The SDP offer or answer. Absent on hangup/decline/busy. */
  sdp?: string
}

/** The cleartext part. The server sees exactly this and no more. */
export interface CallHeader {
  v: 1
  callId: string
  /** The MLS epoch the sender derived its key at. See `control` for why this is in the clear. */
  epoch: number
  /** The sending DEVICE, as `userId:deviceId`. The receiver derives its key from this. */
  from: string
  /** Monotonic per (callId, from). A receiver rejects anything it has already seen. */
  seq: number
  /**
   * An unencrypted control, or absent for a normal sealed signal.
   *
   * There is exactly one, and it exists because of a bootstrapping problem: a device that is
   * at a LATER epoch than the sender cannot derive the sender's key at all — MLS's exporter
   * only exports from the current epoch — so it cannot reply in a sealed envelope to say so.
   * `epoch-mismatch` is that reply. It carries no secret and asserts nothing: it says "I am
   * at epoch N", and the caller re-derives and tries again.
   */
  control?: 'epoch-mismatch'
}

export interface CallEnvelope {
  h: CallHeader
  /** base64 nonce. Absent on a cleartext control. */
  n?: string
  /** base64 AES-GCM ciphertext. Absent on a cleartext control. */
  c?: string
}

/**
 * The bytes bound into the ciphertext as additional authenticated data.
 *
 * Serialised with the keys in a fixed order, because AES-GCM compares these byte for byte:
 * two JSON encoders that order keys differently would produce two different AADs and every
 * signal would fail to open. Never use JSON.stringify on the header object directly.
 */
function headerAAD(h: CallHeader): Uint8Array {
  const canonical = JSON.stringify([h.v, h.callId, h.epoch, h.from, h.seq, h.control ?? ''])
  return new TextEncoder().encode(canonical)
}

async function aesKey(secret: Uint8Array): Promise<CryptoKey> {
  return crypto.subtle.importKey('raw', secret as BufferSource, 'AES-GCM', false, [
    'encrypt',
    'decrypt',
  ])
}

/**
 * Seals a signal under this device's call key.
 *
 * The nonce is 12 RANDOM bytes, every time. It must never be a counter: every device in the
 * conversation can derive every other device's key, so two devices running a counter from
 * zero would eventually encrypt two different messages under the same key and nonce — and an
 * AES-GCM nonce collision does not merely leak those two plaintexts, it leaks the
 * authentication key and lets an attacker forge signals at will.
 */
export async function sealSignal(
  secret: Uint8Array,
  header: CallHeader,
  body: CallBody,
): Promise<string> {
  const nonce = crypto.getRandomValues(new Uint8Array(12))
  const key = await aesKey(secret)
  const sealed = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv: nonce as BufferSource, additionalData: headerAAD(header) as BufferSource },
    key,
    new TextEncoder().encode(JSON.stringify(body)) as BufferSource,
  )
  const envelope: CallEnvelope = {
    h: header,
    n: bytesToBase64(nonce),
    c: bytesToBase64(new Uint8Array(sealed)),
  }
  return bytesToBase64(new TextEncoder().encode(JSON.stringify(envelope)))
}

/** A cleartext control. The only one is `epoch-mismatch`; it carries nothing secret. */
export function sealControl(header: CallHeader): string {
  const envelope: CallEnvelope = { h: header }
  return bytesToBase64(new TextEncoder().encode(JSON.stringify(envelope)))
}

/** Reads the cleartext header. Throws on anything that is not a v1 envelope. */
export function openHeader(wire: string): CallHeader {
  const envelope = JSON.parse(new TextDecoder().decode(base64ToBytes(wire))) as CallEnvelope
  const h = envelope.h
  if (!h || h.v !== 1 || !h.callId || !h.from || typeof h.seq !== 'number') {
    throw new Error('not a call signal')
  }
  return h
}

/**
 * Opens a sealed signal with the SENDER's key.
 *
 * Throws if the header was tampered with (it is bound in as AAD), if the key is wrong — which
 * is what a server substituting its own signal would look like — or if the body is not a
 * signal. There is no lenient path: a signal that does not open is not a signal.
 */
export async function openSignal(secret: Uint8Array, wire: string): Promise<CallBody> {
  const envelope = JSON.parse(new TextDecoder().decode(base64ToBytes(wire))) as CallEnvelope
  if (!envelope.n || !envelope.c) throw new Error('signal is not sealed')

  const key = await aesKey(secret)
  const opened = await crypto.subtle.decrypt(
    {
      name: 'AES-GCM',
      iv: base64ToBytes(envelope.n) as BufferSource,
      additionalData: headerAAD(envelope.h) as BufferSource,
    },
    key,
    base64ToBytes(envelope.c) as BufferSource,
  )
  const body = JSON.parse(new TextDecoder().decode(new Uint8Array(opened))) as CallBody
  if (!body?.kind) throw new Error('signal has no kind')
  return body
}
