// The web MLS session: end-to-end encryption for conversations.
//
// Wraps the pheme-mls WASM client (crates/pheme-mls) with the app-side
// orchestration: load the module once, restore or create this device's identity,
// keep the server's KeyPackage directory topped up, and persist all client state
// (identity + every group's ratchet state) to IndexedDB after each change.
//
// The server is the untrusted Delivery Service throughout: it only ever sees the
// opaque bytes these functions hand it.

import init, { MlsClient } from '../crypto/pkg/pheme_mls.js'
import wasmUrl from '../crypto/pkg/pheme_mls_bg.wasm?url'
import { api } from './api'
import { idbGet, idbSet } from './idb'
import { loadWebDeviceId, saveWebDeviceId } from './device'
import type { Conversation } from './types'

const STATE_KEY = 'client-state'
// Keep at least this many unclaimed KeyPackages published, so a peer can always
// start a chat; replenish up to the target when it runs low.
const MIN_KEY_PACKAGES = 5
const TARGET_KEY_PACKAGES = 20

/** Content types on the wire that this layer produces and consumes. */
export const MLS_APPLICATION = 'application/mls'
export const MLS_WELCOME = 'application/mls-welcome'

let ready: Promise<Session> | null = null

/** One device's MLS session. There is a single instance per tab. */
class Session {
  private readonly client: MlsClient
  readonly deviceId: string

  private constructor(client: MlsClient, deviceId: string) {
    this.client = client
    this.deviceId = deviceId
  }

  static async load(userId: string): Promise<Session> {
    await init(wasmUrl)
    const deviceId = ensureDeviceId()
    const identity = new TextEncoder().encode(userId)

    const saved = await idbGet(STATE_KEY)
    const client = saved ? MlsClient.fromState(saved) : new MlsClient(identity)
    const session = new Session(client, deviceId)
    if (!saved) await session.persist()
    await session.replenishKeyPackages()
    return session
  }

  private async persist(): Promise<void> {
    await idbSet(STATE_KEY, this.client.exportState())
  }

  /** Publishes fresh KeyPackages when the server's stock runs low. */
  async replenishKeyPackages(): Promise<void> {
    let count: number
    try {
      count = await api.keyPackageCount(this.deviceId)
    } catch {
      return // best effort; a peer starting a chat will just have to retry
    }
    if (count >= MIN_KEY_PACKAGES) return
    const fresh: string[] = []
    for (let i = count; i < TARGET_KEY_PACKAGES; i++) {
      fresh.push(bytesToBase64(this.client.keyPackage()))
    }
    await this.persist() // keyPackage() consumes randomness the state must keep
    await api.publishKeyPackages(this.deviceId, fresh)
  }

  /**
   * Creates the MLS group for a conversation and returns the Welcome to relay to
   * the other members (as an MLS_WELCOME control message). The caller creates the
   * conversation server-side first; groupId is the conversation id.
   */
  async startGroup(conversationId: string, memberUserIds: string[]): Promise<string[]> {
    const groupId = new TextEncoder().encode(conversationId)
    this.client.createGroup(groupId)
    // All initial members must be added in a single Commit, or earlier joiners end
    // up an epoch behind and cannot decrypt. Claim every KeyPackage, then add them
    // at once — the one Welcome is addressed to all of them.
    const keyPackages: Uint8Array[] = []
    for (const userId of memberUserIds) {
      keyPackages.push(base64ToBytes(await api.claimKeyPackage(userId)))
    }
    const welcomes: string[] = []
    if (keyPackages.length > 0) {
      const added = this.client.addMembers(groupId, keyPackages)
      welcomes.push(bytesToBase64(added.welcome))
    }
    await this.persist()
    return welcomes
  }

  /** Joins a group from a relayed Welcome. Ignores a Welcome not meant for us. */
  async tryJoin(conversationId: string, welcomeBase64: string): Promise<boolean> {
    if (this.hasGroup(conversationId)) return true
    try {
      this.client.joinFromWelcome(base64ToBytes(welcomeBase64))
      await this.persist()
      return true
    } catch {
      // Not addressed to this device (e.g. we are the group's creator, who sent
      // it), or already joined. Either way, nothing to do.
      return false
    }
  }

  /** True when this client already holds the group — encrypt/decrypt will work. */
  hasGroup(conversationId: string): boolean {
    return this.client.hasGroup(new TextEncoder().encode(conversationId))
  }

  async encrypt(conversationId: string, plaintext: Uint8Array): Promise<string> {
    const groupId = new TextEncoder().encode(conversationId)
    const ciphertext = this.client.encrypt(groupId, plaintext)
    await this.persist()
    return bytesToBase64(ciphertext)
  }

  /** Decrypts an application message, or returns null for a control message. */
  async decrypt(conversationId: string, ciphertextBase64: string): Promise<Uint8Array | null> {
    const groupId = new TextEncoder().encode(conversationId)
    const out = this.client.decrypt(groupId, base64ToBytes(ciphertextBase64))
    await this.persist()
    return out ?? null
  }
}

/** Resolves the singleton session, loading the WASM module on first use. */
export function mlsSession(userId: string): Promise<Session> {
  if (!ready) ready = Session.load(userId)
  return ready
}

/**
 * Ensures the conversation's MLS group exists and its members can join.
 *
 * Only the creator establishes the group — otherwise two peers racing to create
 * the same direct chat would build incompatible groups for the one conversation.
 * The creator claims each other member's KeyPackage, adds them, and relays the
 * resulting Welcome as an MLS_WELCOME control message; each member joins from the
 * Welcome addressed to them. Safe to call repeatedly: a no-op once the group is set
 * up, and for anyone who is not the creator.
 */
export async function provisionGroup(conversation: Conversation, myUserId: string): Promise<void> {
  if (conversation.createdBy !== myUserId) return
  const session = await mlsSession(myUserId)
  if (session.hasGroup(conversation.id)) return
  const others = conversation.members.map((m) => m.userId).filter((uid) => uid !== myUserId)
  const welcomes = await session.startGroup(conversation.id, others)
  for (const welcome of welcomes) {
    await api.sendChatMessage(conversation.id, welcome, MLS_WELCOME)
  }
}

function ensureDeviceId(): string {
  let id = loadWebDeviceId()
  if (!id) {
    id = crypto.randomUUID()
    saveWebDeviceId(id)
  }
  return id
}

// --- base64 helpers (the JSON transport carries bytes as base64) ---

export function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  for (const b of bytes) binary += String.fromCharCode(b)
  return btoa(binary)
}

export function base64ToBytes(base64: string): Uint8Array {
  const binary = atob(base64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes
}
