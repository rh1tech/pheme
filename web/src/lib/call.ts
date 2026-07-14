// A 1-to-1 voice call.
//
// The media is peer to peer and never touches the server. What the server does is pass a
// handful of sealed envelopes between the two browsers so they can find each other, and then
// get out of the way. Once the connection is up, our involvement ends — unless the two ends
// cannot reach each other directly at all, in which case coturn relays the audio and that is
// the only time call media transits our hardware.
//
// The signals are sealed under a key derived from the conversation's MLS group (see
// callEnvelope.ts). The server relays them and cannot read them, which matters because they
// carry the SDP, and the SDP carries the DTLS fingerprint that WebRTC's own media encryption
// is authenticated against. A server able to rewrite that fingerprint could put itself in the
// middle of the call. It cannot rewrite what it cannot read.

import { ApiError, api } from './api'
import {
  callKeyFor,
  catchUpToEpoch,
  freezeGroupForCall,
  myIdentity,
} from './mls'
import {
  openHeader,
  openSignal,
  sealControl,
  sealSignal,
  type CallBody,
  type CallHeader,
} from './callEnvelope'

export type CallStatus =
  /** Placing it: the other end has not picked up. */
  | 'calling'
  /** Somebody is calling us. */
  | 'ringing'
  /** Answered; the peers are finding each other. */
  | 'connecting'
  /** Audio is flowing. */
  | 'connected'
  | 'ended'

/** Why a call ended. The UI says different things for these, and so should it. */
export type CallEndReason =
  | 'hung-up'
  | 'declined'
  | 'busy'
  | 'unanswered'
  | 'answered-elsewhere'
  | 'failed'
  | 'out-of-sync'

export interface CallSnapshot {
  callId: string
  conversationId: string
  status: CallStatus
  /** True when we placed it. */
  outgoing: boolean
  reason?: CallEndReason
}

/** How long a call rings before it gives up. */
const RING_TIMEOUT_MS = 35_000

/**
 * How long to wait for ICE gathering before sending the offer anyway.
 *
 * We send one complete SDP rather than trickling candidates as they arrive. Trickling is
 * faster, but each candidate is independently load-bearing and none of them has a "next
 * stage" that would tell us it went missing — so a lost candidate does not fail the call, it
 * silently downgrades it to a TURN relay and nobody notices except the bandwidth bill. One
 * complete offer costs half a second of setup and cannot half-arrive.
 */
const ICE_GATHER_TIMEOUT_MS = 2_000

/** While a call is being set up, re-read the mailbox this often in case a nudge was dropped. */
const POLL_MS = 400

/** A fresh, unguessable call id. Random, so an old call's signals cannot be replayed into a new one. */
function newCallId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

/**
 * One call, from placing or answering it to hanging up.
 *
 * Deliberately not a React hook: a call outlives any one component (you can navigate away from
 * the conversation and still be talking), and the microphone must be released exactly once no
 * matter how the UI unmounts.
 */
export class Call {
  readonly callId: string
  readonly conversationId: string
  readonly outgoing: boolean

  private readonly userId: string
  private readonly onChange: (s: CallSnapshot) => void

  private status: CallStatus
  private reason?: CallEndReason

  private pc: RTCPeerConnection | null = null
  private localStream: MediaStream | null = null
  private remoteAudio: HTMLAudioElement | null = null

  /** Our own identity and the key we seal with. Derived once — see `deriveKeys`. */
  private identity = ''
  private secret: Uint8Array | null = null
  private epoch = 0
  /** The other devices' keys, by identity. Cached so each signal does not re-derive. */
  private peerSecrets = new Map<string, Uint8Array>()

  private seq = 0
  /** The highest sequence seen from each sender, so a replayed signal is refused. */
  private seen = new Map<string, number>()
  /** How far through the mailbox we have read. */
  private cursor = 0

  private releaseGroup: (() => void) | null = null
  private pollTimer: ReturnType<typeof setInterval> | null = null
  private ringTimer: ReturnType<typeof setTimeout> | null = null
  private draining = false

  private constructor(
    conversationId: string,
    userId: string,
    callId: string,
    outgoing: boolean,
    onChange: (s: CallSnapshot) => void,
  ) {
    this.conversationId = conversationId
    this.userId = userId
    this.callId = callId
    this.outgoing = outgoing
    this.status = outgoing ? 'calling' : 'ringing'
    this.onChange = onChange
  }

  /** Places a call. */
  static async place(
    conversationId: string,
    userId: string,
    onChange: (s: CallSnapshot) => void,
  ): Promise<Call> {
    const call = new Call(conversationId, userId, newCallId(), true, onChange)
    await call.start()
    return call
  }

  /**
   * Picks up an incoming call, once its invite has already been seen. The invite is passed in
   * rather than re-fetched: it is what told us to ring in the first place.
   */
  static async incoming(
    conversationId: string,
    userId: string,
    callId: string,
    onChange: (s: CallSnapshot) => void,
  ): Promise<Call> {
    const call = new Call(conversationId, userId, callId, false, onChange)
    await call.start()
    return call
  }

  snapshot(): CallSnapshot {
    return {
      callId: this.callId,
      conversationId: this.conversationId,
      status: this.status,
      outgoing: this.outgoing,
      reason: this.reason,
    }
  }

  private emit(): void {
    this.onChange(this.snapshot())
  }

  private setStatus(status: CallStatus, reason?: CallEndReason): void {
    if (this.status === 'ended') return // an ended call does not change its mind
    this.status = status
    this.reason = reason
    this.emit()
  }

  /** Common setup: pin the key, hold the group still, and start reading the mailbox. */
  private async start(): Promise<void> {
    // Hold the group's membership still for the duration. Admitting somebody's newly signed-in
    // device is a Commit, a Commit moves the MLS epoch, and the epoch is what our key is
    // derived from — so reconciling mid-call would pull the key out from under a conversation
    // two people are having. It can wait thirty seconds.
    this.releaseGroup = freezeGroupForCall()

    await this.deriveKeys()
    this.startPolling()

    this.ringTimer = setTimeout(() => {
      if (this.status === 'calling' || this.status === 'ringing') {
        void this.end(this.outgoing ? 'unanswered' : 'unanswered', true)
      }
    }, RING_TIMEOUT_MS)
  }

  /**
   * Derives this device's signing key ONCE, and remembers the epoch it came from.
   *
   * Once, and cached in memory for the life of the call — never re-derived. The exporter is
   * bound to the current MLS epoch, so a membership change moves it; a call that re-derived
   * per message would silently start encrypting under a key its peer cannot open, mid
   * sentence. Pinning the bytes makes an epoch change during a call a non-event.
   */
  private async deriveKeys(): Promise<void> {
    this.identity = await myIdentity(this.userId)
    const key = await callKeyFor(this.conversationId, this.userId, this.callId, this.identity)
    if (!key) throw new Error('this device is not in the conversation\'s encrypted group')
    this.secret = key.secret
    this.epoch = key.epoch
  }

  /** The key a given device seals with. Any member can derive any member's — that is how we read them. */
  private async peerSecret(identity: string): Promise<Uint8Array | null> {
    const cached = this.peerSecrets.get(identity)
    if (cached) return cached
    const key = await callKeyFor(this.conversationId, this.userId, this.callId, identity)
    if (!key) return null
    this.peerSecrets.set(identity, key.secret)
    return key.secret
  }

  private header(seq: number, control?: 'epoch-mismatch'): CallHeader {
    return {
      v: 1,
      callId: this.callId,
      epoch: this.epoch,
      from: this.identity,
      seq,
      ...(control ? { control } : {}),
    }
  }

  private async send(body: CallBody, ring = false): Promise<void> {
    if (!this.secret) return
    const wire = await sealSignal(this.secret, this.header(++this.seq), body)
    await api.callSignal(this.conversationId, this.callId, wire, ring).catch(() => {
      // A signal that does not go out is a call that does not connect, but there is nothing to
      // retry against here: the caller's ring timeout is what gives up. Swallowing it keeps a
      // failed hangup from throwing out of a cleanup path.
    })
  }

  // --- the two halves of the exchange ---------------------------------------

  /** Places the call: microphone, one complete offer, ring the other end. */
  async invite(): Promise<void> {
    await this.openMicrophone()
    const pc = await this.peerConnection()

    const offer = await pc.createOffer()
    await pc.setLocalDescription(offer)
    await this.gatheringComplete(pc)

    await this.send({ kind: 'invite', sdp: pc.localDescription?.sdp ?? '' }, true)
  }

  /**
   * Answers. Claims the call for THIS device first — every device the user is signed in on is
   * ringing, and exactly one may pick up.
   *
   * The claim is a server-side lock and not a race, because by the time a device loses it, it
   * has already opened the microphone. "Somebody else answered" cannot be delivered over a bus
   * that is allowed to drop messages: a loser who never hears it keeps ringing with a live mic.
   */
  async answer(offerSdp: string, deviceId: string): Promise<boolean> {
    const won = await api.callAccept(this.conversationId, this.callId, deviceId)
    if (!won) {
      await this.end('answered-elsewhere', false)
      return false
    }

    this.setStatus('connecting')
    await this.openMicrophone()
    const pc = await this.peerConnection()

    await pc.setRemoteDescription({ type: 'offer', sdp: offerSdp })
    const answer = await pc.createAnswer()
    await pc.setLocalDescription(answer)
    await this.gatheringComplete(pc)

    await this.send({ kind: 'answer', sdp: pc.localDescription?.sdp ?? '' })
    return true
  }

  /** Refuses an incoming call. */
  async decline(): Promise<void> {
    await this.send({ kind: 'decline' })
    await this.end('declined', false)
  }

  /** Hangs up, from either end and at any point. */
  async hangUp(): Promise<void> {
    await this.send({ kind: 'hangup' })
    await this.end('hung-up', false)
  }

  // --- reading the mailbox --------------------------------------------------

  /**
   * The live stream only nudges; the signals are read from here.
   *
   * Polling as well as listening is not belt and braces — the bus is explicitly allowed to
   * drop an event, and a dropped SDP answer is a call that silently never connects. Reading
   * from a cursor makes a lost nudge cost a few hundred milliseconds instead of the call, and
   * covers the browser reconnecting its EventSource mid-call, which the stream cannot.
   *
   * It stops the moment the audio is up: there is nothing left to say.
   */
  private startPolling(): void {
    void this.drain()
    this.pollTimer = setInterval(() => {
      if (this.status === 'connected' || this.status === 'ended') {
        this.stopPolling()
        return
      }
      void this.drain()
    }, POLL_MS)
  }

  private stopPolling(): void {
    if (this.pollTimer) clearInterval(this.pollTimer)
    this.pollTimer = null
  }

  /** Called when the live stream says this call has something new. */
  nudge(): void {
    void this.drain()
  }

  private async drain(): Promise<void> {
    if (this.draining || this.status === 'ended') return
    this.draining = true
    try {
      const signals = await api.callSignals(this.conversationId, this.callId, this.cursor)
      for (const s of signals) {
        this.cursor = Math.max(this.cursor, s.seq)
        await this.handle(s.ciphertext)
      }
    } catch {
      // Transient. The next poll picks it up; the ring timeout is what eventually gives up.
    } finally {
      this.draining = false
    }
  }

  private async handle(wire: string): Promise<void> {
    let header: CallHeader
    try {
      header = openHeader(wire)
    } catch {
      return // not a call signal; the server relays whatever it is given
    }
    if (header.callId !== this.callId) return
    if (header.from === this.identity) return // our own signal, echoed back to us

    // Replay and reordering. The server could resend an old signal; a monotonic sequence per
    // sender means it gets us nowhere.
    const last = this.seen.get(header.from) ?? 0
    if (header.seq <= last) return
    this.seen.set(header.from, header.seq)

    if (header.control === 'epoch-mismatch') {
      await this.onEpochMismatch(header)
      return
    }

    // The sender derived their key at THEIR epoch. If we are not at the same one, we cannot
    // derive it — the exporter only exports from the current epoch — so the two of us have to
    // agree on an epoch before we can say a word to each other.
    if (header.epoch !== this.epoch) {
      await this.reconcileEpoch(header.epoch)
      if (header.epoch !== this.epoch) return // handled: we told them, or we gave up
    }

    const secret = await this.peerSecret(header.from)
    if (!secret) return

    let body: CallBody
    try {
      body = await openSignal(secret, wire)
    } catch {
      // It did not open. Either it was tampered with (the header is bound in as AAD), or it was
      // sealed under a key we cannot derive. Neither is something to guess about.
      return
    }
    await this.onBody(body, header)
  }

  private async onBody(body: CallBody, header: CallHeader): Promise<void> {
    switch (body.kind) {
      case 'invite':
        // Handled by the layer that decides whether to ring — a Call only exists once someone
        // has decided to take it.
        break
      case 'answer': {
        // The FIRST answer wins and every later one is ignored. Two of the callee's devices can
        // pick up in the same instant; the server's lock decides which, but a loser's answer may
        // already be in flight, and applying it would tear down the connection we just made.
        if (!this.pc || this.pc.currentRemoteDescription) return
        if (!body.sdp) return
        this.setStatus('connecting')
        await this.pc.setRemoteDescription({ type: 'answer', sdp: body.sdp })
        break
      }
      case 'decline':
        await this.end('declined', false)
        break
      case 'busy':
        await this.end('busy', false)
        break
      case 'hangup':
        await this.end('hung-up', false)
        break
    }
    void header
  }

  // --- epochs ---------------------------------------------------------------

  /**
   * The other end derived its key at an epoch we are not at, so neither of us can read the
   * other.
   *
   * If we are BEHIND we can catch up — apply the Commits we missed and re-derive. If we are
   * AHEAD we cannot: MLS's exporter will not export a past epoch, and there is no way back. So
   * we say so, in the clear (there is nothing secret in "I am at epoch N"), and the caller
   * re-derives at the epoch we are actually at and tries again.
   */
  private async reconcileEpoch(theirs: number): Promise<void> {
    if (theirs > this.epoch) {
      await catchUpToEpoch(this.conversationId, this.userId, theirs)
      await this.deriveKeys()
      this.peerSecrets.clear() // every peer key was derived at the old epoch
      return
    }
    // We are ahead. Tell them, once — a second identical complaint helps nobody.
    const wire = sealControl(this.header(++this.seq, 'epoch-mismatch'))
    await api.callSignal(this.conversationId, this.callId, wire).catch(() => {})
  }

  private async onEpochMismatch(header: CallHeader): Promise<void> {
    if (header.epoch <= this.epoch) return // already there
    await catchUpToEpoch(this.conversationId, this.userId, header.epoch)
    const before = this.epoch
    await this.deriveKeys()
    this.peerSecrets.clear()
    if (this.epoch === before) {
      // We could not get to where they are. Rather than ring forever under a key nobody can
      // open, say plainly that this device is out of step.
      await this.end('out-of-sync', true)
      return
    }
    // Re-offer under the new key, if we are the one placing the call.
    if (this.outgoing && this.pc?.localDescription) {
      await this.send({ kind: 'invite', sdp: this.pc.localDescription.sdp }, false)
    }
  }

  // --- WebRTC ---------------------------------------------------------------

  private async openMicrophone(): Promise<void> {
    if (this.localStream) return
    this.localStream = await navigator.mediaDevices.getUserMedia({ audio: true, video: false })
  }

  private async peerConnection(): Promise<RTCPeerConnection> {
    if (this.pc) return this.pc

    const { iceServers } = await api.iceServers()
    const pc = new RTCPeerConnection({ iceServers })
    this.pc = pc

    // The only way to prove a call is really carrying audio is to ask the browser's own WebRTC
    // stack — `getStats()` on the peer connection. A UI that says "connected" proves nothing:
    // the SDP could have been exchanged and no media path built, and the two people would sit
    // in silence looking at a green bar. So the end-to-end test reads packetsReceived from
    // here.
    //
    // Development builds only; Vite strips this from production, where nothing should be
    // reaching into a live call from the console.
    if (import.meta.env.DEV) {
      ;(window as unknown as { __phemeCallPeer?: RTCPeerConnection }).__phemeCallPeer = pc
    }

    for (const track of this.localStream?.getTracks() ?? []) {
      pc.addTrack(track, this.localStream as MediaStream)
    }

    // The other person's voice. An <audio> element, not a component: the sound must not stop
    // because the user navigated to another conversation.
    pc.ontrack = (e) => {
      if (!this.remoteAudio) {
        this.remoteAudio = new Audio()
        this.remoteAudio.autoplay = true
      }
      this.remoteAudio.srcObject = e.streams[0]
      void this.remoteAudio.play().catch(() => {
        // Autoplay can be refused before a user gesture. Answering IS a gesture, so this is
        // rare; if it happens the user hears nothing and hangs up, which is honest.
      })
    }

    pc.onconnectionstatechange = () => {
      switch (pc.connectionState) {
        case 'connected':
          this.setStatus('connected')
          this.stopPolling()
          if (this.ringTimer) clearTimeout(this.ringTimer)
          break
        case 'failed':
          void this.end('failed', true)
          break
        case 'disconnected':
        case 'closed':
          // `disconnected` can recover on its own; only `failed` and an explicit hangup end a
          // call. Ending here would drop a call that was about to come back.
          break
      }
    }
    return pc
  }

  /**
   * Waits for ICE gathering, but not forever.
   *
   * We send one complete SDP rather than trickling. A TURN allocation can be slow, and a
   * candidate that has not arrived by now is one we can live without — the ones that matter
   * (host, and the reflexive address from STUN) are there in well under a second.
   */
  private gatheringComplete(pc: RTCPeerConnection): Promise<void> {
    if (pc.iceGatheringState === 'complete') return Promise.resolve()
    return new Promise((resolve) => {
      const done = () => {
        clearTimeout(timer)
        pc.removeEventListener('icegatheringstatechange', check)
        resolve()
      }
      const check = () => {
        if (pc.iceGatheringState === 'complete') done()
      }
      const timer = setTimeout(done, ICE_GATHER_TIMEOUT_MS)
      pc.addEventListener('icegatheringstatechange', check)
    })
  }

  // --- teardown -------------------------------------------------------------

  /**
   * Ends the call and releases everything, exactly once.
   *
   * The microphone is the part that must not be got wrong: a call that ends without stopping
   * its tracks leaves the browser's recording indicator on and the mic live. Every path out of
   * a call goes through here.
   */
  async end(reason: CallEndReason, notifyPeer: boolean): Promise<void> {
    if (this.status === 'ended') return
    this.setStatus('ended', reason)

    if (notifyPeer) await this.send({ kind: 'hangup' })

    this.stopPolling()
    if (this.ringTimer) clearTimeout(this.ringTimer)
    this.ringTimer = null

    for (const track of this.localStream?.getTracks() ?? []) track.stop()
    this.localStream = null

    if (this.remoteAudio) {
      this.remoteAudio.srcObject = null
      this.remoteAudio = null
    }
    this.pc?.close()
    this.pc = null

    this.releaseGroup?.()
    this.releaseGroup = null
  }
}

/**
 * Reads an incoming invite off the wire, if that is what it is.
 *
 * Called for a call this device knows nothing about yet — so it derives the sender's key from
 * scratch. Returns null for anything that is not an invite we can open, which includes signals
 * for calls we have already declined and signals from a device whose key we cannot derive.
 */
export async function readInvite(
  conversationId: string,
  userId: string,
  callId: string,
  wire: string,
): Promise<{ from: string; sdp: string; epoch: number } | null> {
  let header: CallHeader
  try {
    header = openHeader(wire)
  } catch {
    return null
  }
  if (header.callId !== callId || header.control) return null

  const me = await myIdentity(userId)
  if (header.from === me) return null // our own invite, echoed back

  // The invite was sealed at the caller's epoch. If we are behind — they added a device and we
  // have not caught up — we can get there.
  await catchUpToEpoch(conversationId, userId, header.epoch)

  const key = await callKeyFor(conversationId, userId, callId, header.from)
  if (!key || key.epoch !== header.epoch) return null

  try {
    const body = await openSignal(key.secret, wire)
    if (body.kind !== 'invite' || !body.sdp) return null
    return { from: header.from, sdp: body.sdp, epoch: header.epoch }
  } catch {
    return null
  }
}

/** True when the error means the server has calling switched off. */
export function isCallingDisabled(e: unknown): boolean {
  return e instanceof ApiError && e.status === 503
}
