// The web MLS session: end-to-end encryption for conversations.
//
// Wraps the pheme-mls WASM client (crates/pheme-mls) with the app-side
// orchestration: load the module once, restore or create this device's identity,
// keep the server's KeyPackage directory topped up, and persist all client state
// (identity + every group's ratchet state) to IndexedDB after each change.
//
// The server is the untrusted Delivery Service throughout: it only ever sees the
// opaque bytes these functions hand it.

import init, { MlsClient, encryptBackup, decryptBackup } from '../crypto/pkg/pheme_mls.js'
import wasmUrl from '../crypto/pkg/pheme_mls_bg.wasm?url'
import { api } from './api'
import { idbClear, idbGet, idbSet, idbSetMany } from './idb'
import { clearPreviews } from './chatCache'
import { clearSafetyPins } from './safety'
import { loadWebDeviceId, saveWebDeviceId } from './device'
import type { Conversation } from './types'

const STATE_KEY = 'client-state'
// Which user the stored state belongs to. Without this, state left behind by a
// previous account on a shared device would be silently adopted by the next one —
// who would then encrypt under the wrong identity and publish the wrong KeyPackages.
const OWNER_KEY = 'client-owner'
// Bumped on every persist, so a tab can tell in one cheap read whether another tab
// has advanced the shared state since it last looked.
const VERSION_KEY = 'client-version'
// The cross-tab lock guarding all MLS state mutation for this origin.
const MLS_LOCK = 'pheme-mls-state'
// Set when the user has explicitly chosen to start fresh on this device rather than
// restore from their backup. Without it we would refuse to create an identity forever.
const FRESH_KEY = 'fresh-accepted'

/** Thrown when this device has no keys but a backup exists and must be restored first. */
export class NeedsRestoreError extends Error {
  constructor() {
    super('encrypted chats need to be restored on this device')
    this.name = 'NeedsRestoreError'
  }
}
// Keep at least this many unclaimed KeyPackages published, so a peer can always
// start a chat; replenish up to the target when it runs low.
const MIN_KEY_PACKAGES = 5
const TARGET_KEY_PACKAGES = 20

/** Content types on the wire that this layer produces and consumes. */
export const MLS_APPLICATION = 'application/mls'
export const MLS_WELCOME = 'application/mls-welcome'

let ready: Promise<Session> | null = null
let readyUserId = ''
let wasmReady: Promise<void> | null = null
// Bumped whenever the local keys are destroyed. A Session created before the wipe
// belongs to an older generation, and is refused the right to write — otherwise an
// operation already in flight when the user logged out would persist the keys again
// after the wipe had "finished".
let generation = 0

/** Loads the WASM module once. Backup/restore need it before any Session exists. */
function ensureWasm(): Promise<void> {
  if (!wasmReady) wasmReady = init(wasmUrl).then(() => undefined)
  return wasmReady
}

/**
 * Runs `fn` with exclusive access to this origin's MLS state.
 *
 * Everything that reads-decides-writes the stored state goes through here — not just
 * steady-state encrypt/decrypt, but session bootstrap, key restore and the logout
 * wipe. Those three are the operations most likely to race in practice (a login, a
 * restore and a logout each kick off several async flows at once), and leaving them
 * outside the lock is what makes a lock a decoration.
 */
function withMlsLock<T>(fn: () => Promise<T>): Promise<T> {
  // Web Locks is unavailable in a few older engines; there we fall back to running
  // unserialized, which is the pre-existing behaviour rather than a new hazard.
  if (!navigator.locks) return fn()
  return navigator.locks.request(MLS_LOCK, fn)
}

/**
 * One device's MLS session.
 *
 * The MLS state is per-device, but a browser gives each TAB its own copy of this
 * object. Two tabs of the same account are ordinary, and left alone they would each
 * hold an independent in-memory client loaded from the same stored state: both could
 * decrypt the same message (each still holding its own unconsumed copy of the
 * message key), breaking the decrypt-once invariant everything here rests on, and
 * whichever persisted last would silently overwrite the other's advanced ratchet —
 * leaving messages permanently unreadable.
 *
 * So every state-mutating operation runs under a cross-tab lock, and re-reads the
 * stored state first if another tab has moved it on. The version counter is what
 * makes "has it moved on?" cheap: without it every operation would have to
 * deserialize the whole key store just to find out that nothing changed.
 */
class Session {
  private client: MlsClient
  private version: number
  /** The wipe generation this session belongs to. See `persist`. */
  private readonly generation: number
  readonly deviceId: string

  private constructor(client: MlsClient, deviceId: string, version: number, generation: number) {
    this.client = client
    this.deviceId = deviceId
    this.version = version
    this.generation = generation
  }

  /**
   * Loads (or creates) this device's session.
   *
   * The whole bootstrap runs under the same lock as every other mutation. It reads
   * the owner, may wipe, reads the state, may create an identity, and writes — a
   * read-decide-write over exactly the keys the lock exists to protect. Unlocked, two
   * tabs opening at once would each see "no state", each mint a DIFFERENT identity,
   * and each persist it; the loser would then be encrypting under an identity nobody
   * else knows about, with the version counter none the wiser.
   */
  static async load(userId: string): Promise<Session> {
    await ensureWasm()
    const deviceId = ensureDeviceId()

    const session = await withMlsLock(async () => {
      // State left by a different account (a shared device where the previous user
      // did not log out cleanly) must never be adopted: encrypting under someone
      // else's MLS identity would send their key material out under our name.
      if ((await storedOwner()) !== userId) await wipeUnlocked()

      const saved = await idbGet(STATE_KEY)

      // No local keys, but a backup exists and the user has not chosen to start over:
      // do NOT mint an identity. Doing so would publish KeyPackages for a client that
      // is about to be thrown away by the restore — and those are irrevocable. Peers
      // claiming one would send a Welcome the restored client has no key for, a
      // message stuck forever. Refuse, and let the restore prompt resolve it.
      if (!saved && !(await idbGet(FRESH_KEY)) && (await backupExists())) {
        throw new NeedsRestoreError()
      }

      const client = saved
        ? MlsClient.fromState(saved)
        : new MlsClient(new TextEncoder().encode(userId))
      const s = new Session(client, deviceId, await storedVersion(), generation)
      if (!saved) await s.persist()
      await idbSet(OWNER_KEY, new TextEncoder().encode(userId))
      return s
    })

    // Outside the lock: replenishKeyPackages takes it itself, and taking it twice
    // would deadlock.
    await session.replenishKeyPackages()
    return session
  }

  private async persist(): Promise<void> {
    // The keys were wiped (a logout) while this operation was in flight. Writing now
    // would put the very material the user asked us to destroy back onto the disk.
    if (this.generation !== generation) {
      throw new Error('mls session invalidated')
    }
    this.version++
    // One transaction: if the state advanced but the version did not, another tab
    // would conclude nothing had changed and mutate on top of consumed key material.
    await idbSetMany([
      [STATE_KEY, this.client.exportState()],
      [VERSION_KEY, encodeVersion(this.version)],
    ])
  }

  /** Adopts another tab's newer state before we mutate on top of a stale copy. */
  private async refreshIfStale(): Promise<void> {
    const stored = await storedVersion()
    if (stored === this.version) return
    const state = await idbGet(STATE_KEY)
    if (!state) return
    this.client = MlsClient.fromState(state)
    this.version = stored
  }

  /**
   * Runs an operation with exclusive access to the MLS state across every tab.
   *
   * Must not be called from inside another `exclusive` — the same lock name would
   * deadlock. Public methods take the lock; the private ones they call do not.
   */
  private exclusive<T>(op: () => Promise<T>): Promise<T> {
    return withMlsLock(async () => {
      await this.refreshIfStale()
      return op()
    })
  }

  /**
   * Publishes fresh KeyPackages when the server's stock runs low, and makes sure the
   * device has published its one reusable last-resort package.
   *
   * Single-use packages can be claimed by anyone, so a stranger can drain them. The
   * last-resort package is what guarantees the user stays reachable: it carries an
   * RFC 9420 extension that makes this client KEEP the private key instead of
   * deleting it after first use, so it can be handed out again and again. The
   * extension has to be set here, when the package is built — a flag on the server
   * would change nothing.
   */
  async replenishKeyPackages(): Promise<void> {
    let status: { count: number; hasLastResort: boolean }
    try {
      status = await api.keyPackageCount(this.deviceId)
    } catch {
      return // best effort; a peer starting a chat will just have to retry
    }
    const needStock = status.count < MIN_KEY_PACKAGES
    const needLastResort = !status.hasLastResort
    if (!needStock && !needLastResort) return

    const { packages, lastResort } = await this.exclusive(async () => {
      const fresh: string[] = []
      if (needStock) {
        for (let i = status.count; i < TARGET_KEY_PACKAGES; i++) {
          fresh.push(bytesToBase64(this.client.keyPackage()))
        }
      }
      const reusable = needLastResort
        ? bytesToBase64(this.client.lastResortKeyPackage())
        : undefined
      // Building a KeyPackage stores its private key; that state must be kept, or the
      // published package would be one we can no longer be added with.
      await this.persist()
      return { packages: fresh, lastResort: reusable }
    })
    await api.publishKeyPackages(this.deviceId, packages, lastResort)
  }

  /**
   * Creates the MLS group for a conversation and returns the Welcome to relay to
   * the other members (as an MLS_WELCOME control message). The caller creates the
   * conversation server-side first; groupId is the conversation id.
   */
  async startGroup(conversationId: string, memberUserIds: string[]): Promise<string[]> {
    // Claim the KeyPackages before taking the lock — this is network I/O, and the
    // lock blocks every other tab for as long as it is held.
    const keyPackages: Uint8Array[] = []
    for (const userId of memberUserIds) {
      keyPackages.push(base64ToBytes(await api.claimKeyPackage(userId)))
    }

    return this.exclusive(async () => {
      const groupId = new TextEncoder().encode(conversationId)
      this.client.createGroup(groupId)
      // All initial members must be added in a single Commit, or earlier joiners
      // end up an epoch behind and cannot decrypt. The one Welcome covers them all.
      const welcomes: string[] = []
      if (keyPackages.length > 0) {
        const added = this.client.addMembers(groupId, keyPackages)
        welcomes.push(bytesToBase64(added.welcome))
      }
      await this.persist()
      return welcomes
    })
  }

  /** Joins a group from a relayed Welcome. Ignores a Welcome not meant for us. */
  async tryJoin(conversationId: string, welcomeBase64: string): Promise<boolean> {
    return this.exclusive(async () => {
      if (this.holdsGroup(conversationId)) return true
      try {
        this.client.joinFromWelcome(base64ToBytes(welcomeBase64))
        await this.persist()
        return true
      } catch {
        // Not addressed to this device (e.g. we are the group's creator, who sent
        // it), or already joined. Either way, nothing to do.
        return false
      }
    })
  }

  /** True when this client already holds the group — encrypt/decrypt will work. */
  async hasGroup(conversationId: string): Promise<boolean> {
    return this.exclusive(async () => this.holdsGroup(conversationId))
  }

  /** The unlocked check, for use by methods that already hold the lock. */
  private holdsGroup(conversationId: string): boolean {
    return this.client.hasGroup(new TextEncoder().encode(conversationId))
  }

  /**
   * The safety number for a conversation: the digits two people compare, out of
   * band, to prove no one is in the middle. Computed from the group's own ratchet
   * tree, so a KeyPackage the server swapped in shows up as a different number.
   */
  async safetyNumber(conversationId: string): Promise<string> {
    return this.exclusive(async () =>
      this.client.safetyNumber(new TextEncoder().encode(conversationId)),
    )
  }

  async encrypt(conversationId: string, plaintext: Uint8Array): Promise<string> {
    return this.exclusive(async () => {
      const groupId = new TextEncoder().encode(conversationId)
      const ciphertext = this.client.encrypt(groupId, plaintext)
      await this.persist()
      return bytesToBase64(ciphertext)
    })
  }

  /** Decrypts an application message, or returns null for a control message. */
  async decrypt(conversationId: string, ciphertextBase64: string): Promise<Uint8Array | null> {
    return this.exclusive(async () => {
      const groupId = new TextEncoder().encode(conversationId)
      const out = this.client.decrypt(groupId, base64ToBytes(ciphertextBase64))
      await this.persist()
      return out ?? null
    })
  }
}

/**
 * Resolves the session for `userId`, loading the WASM module on first use.
 *
 * The session is cached per user, not globally: if a different account signs in
 * without a reload, the cached one is discarded rather than reused. Reusing it
 * would encrypt the new user's messages under the previous user's MLS identity.
 */
export function mlsSession(userId: string): Promise<Session> {
  if (!ready || readyUserId !== userId) {
    readyUserId = userId
    // A failed load must not be cached: it would leave the app permanently unable to
    // build a session (e.g. after the user restores from their backup and retries).
    ready = Session.load(userId).catch((e: unknown) => {
      ready = null
      readyUserId = ''
      throw e
    })
  }
  return ready
}

/**
 * Records that the user chose to start over on this device rather than restore their
 * backup. Their existing encrypted history stays unreadable here until they restore;
 * new conversations work from a fresh identity.
 */
export async function acceptFreshIdentity(): Promise<void> {
  await idbSet(FRESH_KEY, new Uint8Array([1]))
}

/** The account the locally stored MLS state belongs to, if any. */
async function storedOwner(): Promise<string> {
  const bytes = await idbGet(OWNER_KEY)
  return bytes ? new TextDecoder().decode(bytes) : ''
}

/** The version of the stored state — how a tab notices another tab moved it on. */
async function storedVersion(): Promise<number> {
  const bytes = await idbGet(VERSION_KEY)
  if (!bytes || bytes.byteLength < 4) return 0
  return new DataView(bytes.buffer, bytes.byteOffset, 4).getUint32(0)
}

function encodeVersion(version: number): Uint8Array {
  const bytes = new Uint8Array(4)
  new DataView(bytes.buffer).setUint32(0, version >>> 0)
  return bytes
}

/**
 * Erases this device's MLS keys and every decrypted message cached from them.
 *
 * Called on logout: the key state and the plaintext cache are exactly what the
 * encryption exists to protect, and leaving them readable on a shared device after
 * signing out would defeat it. There is no way to re-derive them afterwards except
 * from the passphrase-protected backup — which is the point of that backup.
 */
export async function wipeLocalKeys(): Promise<void> {
  await withMlsLock(async () => {
    await wipeUnlocked()
    ready = null
    readyUserId = ''
  })
}

/**
 * The wipe itself. Callers must already hold the lock.
 *
 * Bumping the generation is what makes the wipe stick: an encrypt or decrypt that was
 * already in flight will finish, try to persist, find itself a generation behind, and
 * refuse — instead of writing the keys straight back to the disk we just cleared.
 *
 * It deliberately does not touch the cached session promise. Session.load calls this
 * while building that very promise; clearing it there would send the next caller off
 * to start a second, redundant load.
 */
async function wipeUnlocked(): Promise<void> {
  generation++
  await idbClear()
  clearPreviews()
  clearSafetyPins()
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
  if (await session.hasGroup(conversation.id)) return
  const others = conversation.members.map((m) => m.userId).filter((uid) => uid !== myUserId)
  const welcomes = await session.startGroup(conversation.id, others)
  for (const welcome of welcomes) {
    await api.sendChatMessage(conversation.id, welcome, MLS_WELCOME)
  }
}

// --- encrypted key backup -------------------------------------------------
//
// The device's whole MLS state is sealed under a recovery passphrase and stored
// server-side, so IndexedDB eviction (iOS Safari's ~7-day rule) or a new device is
// recoverable. A backup is a point-in-time snapshot: re-run it after active use to
// keep recovery current.

/** Whether this device already holds MLS keys locally (IndexedDB). */
export async function hasLocalKeys(): Promise<boolean> {
  return (await idbGet(STATE_KEY)) != null
}

/** Whether the server holds a key backup for the signed-in user. */
export async function backupExists(): Promise<boolean> {
  try {
    return (await api.getKeyBackup(true)) != null
  } catch {
    return false
  }
}

/** Seals the current device state under `passphrase` and uploads it. */
export async function backupKeys(userId: string, passphrase: string): Promise<void> {
  const session = await mlsSession(userId)
  const state = await idbGet(STATE_KEY)
  if (!state) throw new Error('no local key state to back up')
  const blob = encryptBackup(new TextEncoder().encode(passphrase), state)
  await api.putKeyBackup(
    session.deviceId,
    bytesToBase64(blob.salt),
    bytesToBase64(blob.nonce),
    bytesToBase64(blob.ciphertext),
  )
}

/**
 * Recovers device state from the server backup using `passphrase`, writing it to
 * local storage. Returns false when there is no backup; throws on a wrong
 * passphrase (the GCM tag fails). Must run before the first mlsSession() call, so a
 * fresh identity is not created in place of the recovered one; it resets the
 * session singleton to be safe.
 */
export async function restoreKeys(userId: string, passphrase: string): Promise<boolean> {
  await ensureWasm()
  const backup = await api.getKeyBackup(true)
  if (!backup) return false
  const state = decryptBackup(
    new TextEncoder().encode(passphrase),
    base64ToBytes(backup.salt),
    base64ToBytes(backup.nonce),
    base64ToBytes(backup.ciphertext),
  )
  // Validate the recovered blob really is a client state before committing it.
  MlsClient.fromState(state)

  await withMlsLock(async () => {
    // The state and its owner must land together, in one transaction. Written
    // separately, a Session.load racing in between would see state whose owner is not
    // yet claimed, take it for another account's leftovers, and wipe the backup we
    // just recovered — while restore went on to report success.
    const nextVersion = (await storedVersion()) + 1
    await idbSetMany([
      [STATE_KEY, state],
      [OWNER_KEY, new TextEncoder().encode(userId)],
      [VERSION_KEY, encodeVersion(nextVersion)],
    ])
    // Any session built on the pre-restore identity must not write over this.
    generation++
    ready = null
    readyUserId = ''
  })
  return true
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
