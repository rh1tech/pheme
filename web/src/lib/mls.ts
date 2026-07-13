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
import { ApiError, api } from './api'
import { idbClearExcept, idbDelete, idbGet, idbSet, idbSetMany } from './idb'
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
// The key-material epoch, bumped on every wipe and restore. It lives in IndexedDB and
// not in a variable because the tab whose keys were destroyed is usually NOT the tab
// that destroyed them — a per-tab counter cannot see another tab's logout.
const EPOCH_KEY = 'client-epoch'

/** Thrown when this session's keys were destroyed or replaced (logout, restore). */
export class SessionInvalidatedError extends Error {
  constructor() {
    super('this device\'s encryption keys were replaced or destroyed')
    this.name = 'SessionInvalidatedError'
  }
}

/**
 * Thrown when the person we are trying to reach has published no KeyPackages — they
 * have not opened Pheme on a device that does encrypted chats, so there is nothing to
 * build a group with. They become reachable the moment they do.
 */
export class PeerKeysMissingError extends Error {
  constructor() {
    super('that person has not set up encrypted chats yet')
    this.name = 'PeerKeysMissingError'
  }
}

/** Thrown when another session already set up an identity on this device. */
export class IdentityAlreadySetUpError extends Error {
  constructor() {
    super('this device already has encryption set up')
    this.name = 'IdentityAlreadySetUpError'
  }
}

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
/**
 * "I am a member of this conversation and I cannot join it."
 *
 * Sent by someone who has a Welcome they could not use — the KeyPackage it names is not
 * one they hold, so they are locked out of their own conversation with no way back in:
 * the creator sees a group and never sends another Welcome. This asks them to build the
 * group again. It is not a Welcome and carries no key material, so it cannot be used to
 * destroy anyone's KeyPackages the way a forged Welcome can.
 */
export const MLS_REJOIN = 'application/mls-rejoin'

let ready: Promise<Session> | null = null
let readyUserId = ''
let wasmReady: Promise<void> | null = null
// Memoized answer to "must this device restore before it can have an identity?" —
// null until asked. Reset whenever the answer could change.
let restoreNeeded: boolean | null = null
// Provisioning runs in flight, by conversation. Two callers ask for it — opening the
// chat, and sending into one that has no group yet — and they must not both do it.
const provisioning = new Map<string, Promise<void>>()

/** A Welcome that was created but not yet accepted by the server. */
const pendingWelcomeKey = (conversationId: string) => `welcome:${conversationId}`

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
  /** The key-material epoch this session belongs to. See `assertLive`. */
  private readonly epoch: number
  readonly deviceId: string

  private constructor(client: MlsClient, deviceId: string, version: number, epoch: number) {
    this.client = client
    this.deviceId = deviceId
    this.version = version
    this.epoch = epoch
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

    // Would we have to mint a brand-new identity? Answered BEFORE taking the lock,
    // because answering it may need the network (does a backup exist?), and a network
    // call made while holding the lock would freeze every other tab for as long as it
    // hangs. The result is re-checked under the lock, where it is cheap.
    const mustRestore = await needsRestore(userId)

    const session = await withMlsLock(async () => {
      // Under the lock: two tabs opening at once would otherwise each read no device id,
      // each mint one, and each keep its own — publishing KeyPackages under a device id
      // the other tab will never look up again.
      const deviceId = ensureDeviceId()

      // State left by a different account (a shared device where the previous user
      // did not log out cleanly) must never be adopted: encrypting under someone
      // else's MLS identity would send their key material out under our name.
      if ((await storedOwner()) !== userId) await wipeUnlocked()

      const saved = await idbGet(STATE_KEY)

      // No local keys, but a backup is waiting: do NOT mint an identity. Doing so
      // would publish KeyPackages for a client the restore is about to throw away —
      // and publishing is irrevocable. A peer claiming one would send a Welcome the
      // restored client has no key for: a message stuck forever. Refuse, and let the
      // restore prompt resolve it.
      if (!saved && mustRestore) throw new NeedsRestoreError()

      const client = saved
        ? MlsClient.fromState(saved)
        : new MlsClient(new TextEncoder().encode(userId))
      const s = new Session(client, deviceId, await storedVersion(), await storedEpoch())
      if (!saved) await s.persist()
      await idbSet(OWNER_KEY, new TextEncoder().encode(userId))
      return { s, fresh: !saved }
    })

    // Outside the lock (these take it themselves; taking it twice would deadlock).
    // A fresh identity's device may still have stale public key packages on the server
    // from a previous identity on this same device — a wipe, a cleared browser. Their
    // private halves are gone, so anyone claiming one would build a group this device
    // could never join. Purge them before publishing new ones.
    if (session.fresh) {
      await api.deleteKeyPackages(session.s.deviceId).catch(() => {})
    }
    await session.s.replenishKeyPackages()
    return session.s
  }

  /**
   * Refuses to go on if this session's key material has been destroyed or replaced —
   * by a logout or a restore, in THIS tab or any other. Callers hold the lock.
   *
   * The epoch lives in IndexedDB rather than in a variable, because a variable is
   * per-tab and the thing we are defending against is another tab. A logout in one tab
   * leaves a second tab holding a live in-memory client whose private keys are still
   * intact; without a shared epoch, that tab happily encrypts with them and writes
   * them back to the disk the user just asked us to clear.
   */
  private async assertLive(): Promise<void> {
    if ((await storedEpoch()) !== this.epoch) {
      throw new SessionInvalidatedError()
    }
  }

  private async persist(): Promise<void> {
    await this.assertLive()
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
    // State gone while the version moved: someone wiped it. assertLive has already
    // rejected this session, so there is nothing to adopt — do not quietly carry on
    // with the in-memory keys, which is precisely how a wiped identity stays alive.
    if (!state) throw new SessionInvalidatedError()
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
      // Checked before the operation, not just before the write: an encrypt on a
      // destroyed identity still consumes ratchet state, and there is no reason to run
      // it at all once the session is dead.
      await this.assertLive()
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
  async startGroup(
    conversationId: string,
    memberUserIds: string[],
    force = false,
  ): Promise<string[]> {
    // Claim the KeyPackages before taking the lock — this is network I/O, and the
    // lock blocks every other tab for as long as it is held.
    const keyPackages: Uint8Array[] = []
    for (const userId of memberUserIds) {
      try {
        keyPackages.push(base64ToBytes(await api.claimKeyPackage(userId)))
      } catch (e) {
        // They have published no KeyPackages, so there is no way to add them to an
        // encrypted group — nobody can reach them until they open Pheme once. That is
        // a fact about the other person, not a failure of ours, and the UI has to say
        // so rather than reporting a generic error the user cannot act on.
        if (e instanceof ApiError && e.status === 404) throw new PeerKeysMissingError()
        throw e
      }
    }

    return this.exclusive(async () => {
      // Re-checked with the lock held. The in-flight map above stops this tab from
      // provisioning twice; this stops a SECOND TAB from doing it, which would replace
      // the group another tab just made and leave the other person joined to a group
      // nobody encrypts to. The KeyPackages claimed above are wasted in that case —
      // cheap, and far better than a conversation that can never be read.
      //
      // `force` is the deliberate exception: rebuilding a group that the other member
      // could never join. Replacing it is the point.
      if (!force && this.holdsGroup(conversationId)) return []

      const groupId = new TextEncoder().encode(conversationId)
      // A group id can only be created once — creating over the top is refused — so a
      // rebuild has to discard the old group first. Everything encrypted to it stays
      // unreadable, which for the person who could never join it always was.
      if (force) this.client.deleteGroup(groupId)
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
  restoreNeeded = false
}

/**
 * Whether this device must restore from a backup before it can have an identity:
 * it holds no usable keys, the user has not chosen to start over, and the server has
 * a backup waiting.
 *
 * Memoized. Without it, every retry of a refused session would repeat the network
 * call, and the chat route retries whenever its messages change. Cleared whenever the
 * answer could have changed — a wipe, a restore, or the user choosing to start fresh.
 */
async function needsRestore(userId: string): Promise<boolean> {
  if (restoreNeeded !== null) return restoreNeeded

  // State belonging to someone else is about to be wiped, so it does not count.
  const owned = (await storedOwner()) === userId && (await idbGet(STATE_KEY)) != null
  if (owned || (await idbGet(FRESH_KEY))) {
    restoreNeeded = false
    return false
  }
  // A check that FAILS is not a check that says "no backup". Treating a flaky network
  // as "safe to mint an identity" is how a real backup gets orphaned by a throwaway one
  // — and publishing KeyPackages cannot be undone. Fail closed: refuse to decide.
  restoreNeeded = await backupExists()
  return restoreNeeded
}

/** The account the locally stored MLS state belongs to, if any. */
async function storedOwner(): Promise<string> {
  const bytes = await idbGet(OWNER_KEY)
  return bytes ? new TextDecoder().decode(bytes) : ''
}

/** The current key-material epoch. Zero when nothing has ever been wiped. */
async function storedEpoch(): Promise<number> {
  const bytes = await idbGet(EPOCH_KEY)
  if (!bytes || bytes.byteLength < 4) return 0
  return new DataView(bytes.buffer, bytes.byteOffset, 4).getUint32(0)
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
  return withMlsLock(async () => {
    await wipeUnlocked()
    ready = null
    readyUserId = ''
  })
}

/**
 * The wipe itself. Callers must already hold the lock.
 *
 * Bumping the persisted epoch is what makes the wipe stick: any session still holding
 * the old epoch — in this tab or another — is refused the right to encrypt or write,
 * instead of quietly putting the keys back on the disk we just cleared.
 *
 * It deliberately does not touch the cached session promise. Session.load calls this
 * while building that very promise; clearing it there would send the next caller off
 * to start a second, redundant load.
 */
async function wipeUnlocked(): Promise<void> {
  const next = (await storedEpoch()) + 1
  restoreNeeded = null
  // Clearing the store and advancing the epoch are one transaction, not two. The clear
  // removes the epoch along with everything else; if the write back were separate and
  // anything interrupted between them, the epoch would read as zero — which is what a
  // stale session in another tab is still carrying, so it would pass the liveness check
  // and put the destroyed keys straight back.
  await idbClearExcept([[EPOCH_KEY, encodeVersion(next)]])
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
export async function provisionGroup(
  conversation: Conversation,
  myUserId: string,
  force = false,
): Promise<void> {
  if (conversation.createdBy !== myUserId) return

  // Provisioning a conversation happens exactly once, however many callers ask for it.
  //
  // Two do: the route asks when the chat is opened, and sending asks if no group exists
  // yet. Left to race, both see "no group", both claim a KeyPackage from the other
  // person, both create a group — and the second createGroup REPLACES the first, since
  // MLS stores a group by its id. Two Welcomes then go out for two different groups.
  // The recipient joins from the first and can never decrypt a word, because everything
  // is encrypted to the second. That is not a rare interleaving; it is what happens when
  // someone opens a chat and immediately types, which is what people do.
  const inFlight = provisioning.get(conversation.id)
  if (inFlight) return inFlight

  const run = (async () => {
    const session = await mlsSession(myUserId)

    // A Welcome we created but never managed to post: finish that rather than build a
    // second group. Without this, a failed post leaves the group on this device and the
    // other person with no way in, forever — every later attempt sees the group and
    // returns early.
    const pending = force ? undefined : await idbGet(pendingWelcomeKey(conversation.id))
    if (pending) {
      await api.sendChatMessage(conversation.id, bytesToBase64(pending), MLS_WELCOME)
      await idbDelete(pendingWelcomeKey(conversation.id))
      return
    }

    if (!force && (await session.hasGroup(conversation.id))) return

    const others = conversation.members.map((m) => m.userId).filter((uid) => uid !== myUserId)
    const welcomes = await session.startGroup(conversation.id, others, force)
    for (const welcome of welcomes) {
      // Recorded before it is sent, so a failure here is recoverable on the next attempt
      // instead of stranding the other member.
      await idbSet(pendingWelcomeKey(conversation.id), base64ToBytes(welcome))
      await api.sendChatMessage(conversation.id, welcome, MLS_WELCOME)
      await idbDelete(pendingWelcomeKey(conversation.id))
    }
  })().finally(() => provisioning.delete(conversation.id))

  provisioning.set(conversation.id, run)
  return run
}

/**
 * Asks the conversation's creator to build the group again, because we hold a Welcome
 * we cannot use and are therefore locked out of our own conversation.
 */
export async function requestRejoin(conversationId: string): Promise<void> {
  // One byte: the server rejects an empty body, and there is nothing to say beyond the
  // fact that this was sent.
  await api.sendChatMessage(conversationId, bytesToBase64(new Uint8Array([1])), MLS_REJOIN)
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
  return (await api.getKeyBackup(true)) != null
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

  return withMlsLock(async () => {
    // Another tab may have set up an identity while this prompt sat open (the user chose
    // "start fresh" there, or simply signed in elsewhere). Restoring would replace it
    // with an older snapshot and strand whatever was said in between. Refuse — and say
    // so, rather than closing the prompt as though the restore had happened.
    if ((await idbGet(STATE_KEY)) && (await storedOwner()) === userId) {
      throw new IdentityAlreadySetUpError()
    }

    // The state and its owner must land together, in one transaction. Written
    // separately, a Session.load racing in between would see state whose owner is not
    // yet claimed, take it for another account's leftovers, and wipe the backup we
    // just recovered — while restore went on to report success.
    const nextVersion = (await storedVersion()) + 1
    const nextEpoch = (await storedEpoch()) + 1
    await idbSetMany([
      [STATE_KEY, state],
      [OWNER_KEY, new TextEncoder().encode(userId)],
      [VERSION_KEY, encodeVersion(nextVersion)],
      // A new epoch: any session built on the identity this replaces — in this tab or
      // another — must not write over what we just recovered.
      [EPOCH_KEY, encodeVersion(nextEpoch)],
    ])
    restoreNeeded = false
    ready = null
    readyUserId = ''
    return true
  })
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
