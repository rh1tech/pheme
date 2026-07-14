// The web MLS session: end-to-end encryption for conversations.
//
// Wraps the pheme-mls WASM client (crates/pheme-mls) with the app-side orchestration:
// load the module once, restore or create this DEVICE's identity, keep the server's
// KeyPackage directory topped up, and persist all client state (identity + every group's
// ratchet state) to IndexedDB after each change.
//
// The server is the untrusted Delivery Service throughout: it only ever sees the opaque
// bytes these functions hand it. But it is the one party every member agrees on, and two
// questions can only be settled there — which group a conversation IS, and whose Commit
// came first. Both are settled by one compare-and-set (api.mlsCommit).
//
// ---------------------------------------------------------------------------
// The rule everything here follows: AN MLS LEAF IS A DEVICE, NOT A PERSON.
//
// Two devices of the same user are two independent clients with different private keys.
// Each must be its own leaf in the group, or it cannot decrypt a single message — which
// is what a chat full of "…" actually was. So:
//
//   * a group is built from one KeyPackage per DEVICE of each member, never one per user;
//   * a device that is missing from the group gets ADDED to it (reconcileDevices), and the
//     group is never torn down and rebuilt around it — rebuilding destroys the key
//     material for every message anyone has ever sent;
//   * removing someone removes EVERY leaf they have, or they keep reading on their phone.
// ---------------------------------------------------------------------------

import init, { MlsClient, encryptBackup, decryptBackup } from '../crypto/pkg/pheme_mls.js'
import wasmUrl from '../crypto/pkg/pheme_mls_bg.wasm?url'
import { ApiError, api } from './api'
import { idbClearExcept, idbGet, idbSet, idbSetMany } from './idb'
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
 * Thrown when the person we are trying to reach has published no KeyPackages on any
 * device — they have not opened Pheme somewhere that does encrypted chats, so there is
 * nothing to build a group with. They become reachable the moment they do.
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

// How many times a Commit is re-proposed after the server refuses it. Each round trip
// means another member committed first; a handful of concurrent membership changes is
// realistic, an unbounded fight is not.
const COMMIT_ATTEMPTS = 4

/** Content types on the wire that this layer produces and consumes. */
export const MLS_APPLICATION = 'application/mls'
export const MLS_WELCOME = 'application/mls-welcome'
/**
 * A membership Commit — every current member applies it to reach the new epoch. Posted
 * only through api.mlsCommit, which weighs it against the conversation's epoch; the
 * ordinary message route refuses it.
 */
export const MLS_COMMIT = 'application/mls-commit'
/**
 * "I am a member of this conversation and my device is not in its group."
 *
 * Posted by a device that holds no group — a new phone, a browser whose storage was
 * evicted — so that a member who DOES hold the group adds it. It carries no key material,
 * so it cannot be used to burn anyone's KeyPackages the way a forged Welcome can.
 *
 * It replaces the old "rejoin" message, which asked the conversation's creator to DESTROY
 * the group and build a new one. That is what turned one locked-out device into a
 * conversation nobody could read: every rebuild threw away the key material for every
 * message ever sent to the old group, for everybody.
 */
export const MLS_DEVICE = 'application/mls-device'

/** The control types, which carry no user-visible text. */
export const MLS_CONTROL_TYPES: ReadonlySet<string> = new Set([MLS_WELCOME, MLS_COMMIT, MLS_DEVICE])

let ready: Promise<Session> | null = null
let readyUserId = ''
let wasmReady: Promise<void> | null = null
// Memoized answer to "must this device restore before it can have an identity?" —
// null until asked. Reset whenever the answer could change.
let restoreNeeded: boolean | null = null
// ensureGroup runs in flight, by conversation. Several callers ask for it — opening the
// chat, sending into it, a device announcing itself — and they must not all do it at once.
const settling = new Map<string, Promise<string | null>>()

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

/** The MLS group id as bytes. The server hands it out as an opaque string. */
function groupBytes(groupId: string): Uint8Array {
  return new TextEncoder().encode(groupId)
}

/** One device's MLS identity, as it appears in a group's leaf: `userId:deviceId`. */
function deviceIdentity(userId: string, deviceId: string): string {
  return `${userId}:${deviceId}`
}

/** The device half of a credential identity. */
function deviceOf(identity: string): string {
  const sep = identity.indexOf(':')
  return sep === -1 ? '' : identity.slice(sep + 1)
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
  private readonly epochTag: number
  readonly userId: string
  readonly deviceId: string

  private constructor(
    client: MlsClient,
    userId: string,
    deviceId: string,
    version: number,
    epochTag: number,
  ) {
    this.client = client
    this.userId = userId
    this.deviceId = deviceId
    this.version = version
    this.epochTag = epochTag
  }

  /** This device's leaf identity, as it appears in a group's member list. */
  get identity(): string {
    return deviceIdentity(this.userId, this.deviceId)
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

      // The identity carries the DEVICE id, not just the user id. That is the whole fix:
      // it is what gives this device its own leaf in every group, distinct from the
      // user's other devices, so all of them can read the conversation.
      //
      // For existing state the device id comes from the CLIENT, not from local storage.
      // The credential is what the groups actually hold a leaf under, and a restored
      // backup carries the identity of the device it was taken from — so local storage
      // can be wrong, and the credential cannot.
      const restored = saved ? MlsClient.fromState(saved) : null

      // State from BEFORE identities carried a device id — its credential is the bare user
      // id, so it names a person and not a device. It cannot be kept: every group it holds
      // is one where this user occupies a single leaf shared with all their other devices,
      // which is the bug itself, and `deviceOf` on it returns nothing, so the client would
      // publish its KeyPackages under an empty device id and quietly stop being reachable
      // at all.
      //
      // Discard it and mint a proper device identity. Nothing of value is lost that was not
      // already lost: those old groups are unreadable from the moment the conversation's
      // group is established afresh, and the plaintext this device has already decrypted
      // lives in the message cache, which is left alone.
      const isLegacy = restored !== null && deviceOf(restored.identity) === ''
      const keep = restored !== null && !isLegacy

      let client: MlsClient
      let deviceId: string
      let retiredDeviceId = ''
      if (keep && restored) {
        client = restored
        deviceId = deviceOf(client.identity)
        saveWebDeviceId(deviceId)
      } else {
        // A FRESH identity gets a FRESH device id, even if this browser already had one.
        //
        // A new identity means the old private keys are gone — the storage was evicted,
        // the user logged out, the browser was cleared. But the groups this device used to
        // be in still hold a leaf under the old name, and that leaf's keys no longer exist
        // anywhere. Reusing the name would make the new client indistinguishable from the
        // dead one: every member reconciling the group would see `user:device` already
        // present, conclude there was nothing to add, and this device would never be let
        // back in. Locked out permanently, by its own name.
        //
        // Under the lock, because two tabs opening at once would otherwise each mint one
        // and each keep its own.
        retiredDeviceId = loadWebDeviceId() ?? ''
        deviceId = crypto.randomUUID()
        saveWebDeviceId(deviceId)
        client = new MlsClient(userId, deviceId)
      }

      const s = new Session(client, userId, deviceId, await storedVersion(), await storedEpoch())
      if (!keep) await s.persist()
      await idbSet(OWNER_KEY, new TextEncoder().encode(userId))
      return { s, fresh: !keep, retiredDeviceId }
    })

    // Outside the lock (these take it themselves; taking it twice would deadlock).
    // The identity this one replaces may still have public KeyPackages on the server. Their
    // private halves are gone, so anyone claiming one would build a group this device could
    // never join. Purge them before publishing new ones — and purging them is also what
    // tells everyone else that leaf is a ghost, so the group can prune it.
    if (session.retiredDeviceId) {
      await api.deleteKeyPackages(session.retiredDeviceId).catch(() => {})
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
    if ((await storedEpoch()) !== this.epochTag) {
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

  /** True when this client holds the group — encrypt/decrypt will work. */
  async hasGroup(groupId: string): Promise<boolean> {
    return this.exclusive(async () => this.client.hasGroup(groupBytes(groupId)))
  }

  /** The group's current epoch, or 0 when we do not hold it. */
  async epoch(groupId: string): Promise<number> {
    return this.exclusive(async () => this.epochUnlocked(groupId))
  }

  private epochUnlocked(groupId: string): number {
    const bytes = groupBytes(groupId)
    if (!this.client.hasGroup(bytes)) return 0
    return Number(this.client.epoch(bytes))
  }

  /** The `userId:deviceId` of every leaf in the group. Empty when we do not hold it. */
  async memberIdentities(groupId: string): Promise<string[]> {
    return this.exclusive(async () => {
      const bytes = groupBytes(groupId)
      if (!this.client.hasGroup(bytes)) return []
      return this.client.memberIdentities(bytes) as string[]
    })
  }

  /** Creates the group locally. It is not real until the server accepts our first Commit. */
  async createGroup(groupId: string): Promise<void> {
    return this.exclusive(async () => {
      this.client.createGroup(groupBytes(groupId))
      await this.persist()
    })
  }

  /**
   * Discards a group we created but the server never accepted — we lost the race to
   * establish it, and what we built is an orphan nobody else will ever join.
   *
   * Never call this on a group other members are in. Discarding a live group throws away
   * the key material for every message ever sent to it, for everyone.
   */
  async discardGroup(groupId: string): Promise<void> {
    return this.exclusive(async () => {
      this.client.deleteGroup(groupBytes(groupId))
      await this.persist()
    })
  }

  /**
   * Proposes a Commit, asks the server to accept it, and applies it ONLY if the server
   * does. Returns whether it landed.
   *
   * The whole round trip is under the lock, network call and all. That is deliberate,
   * and it is the one place this file holds the lock across I/O: a staged Commit lives in
   * the persisted client state, so a second tab that staged its own on top of ours would
   * corrupt both. A membership change is rare — one blocked round trip is a fair price
   * for a group that cannot fork.
   *
   * On refusal the staged Commit is thrown away, never applied. Applying a Commit the
   * group refused is exactly what forks a device off the conversation for good: it
   * advances its own ratchet to an epoch nobody else is in, and silently stops being able
   * to read anything ever again.
   */
  private async commit(
    conversationId: string,
    groupId: string,
    stage: (bytes: Uint8Array) => { welcome?: Uint8Array; commit: Uint8Array },
    removes: string[] = [],
  ): Promise<'accepted' | 'conflict'> {
    return this.exclusive(async () => {
      const bytes = groupBytes(groupId)
      const baseEpoch = this.epochUnlocked(groupId)
      const staged = stage(bytes)
      // Staging writes a pending commit into the client state; it must be persisted, or a
      // reload would leave the group with a pending commit the server has accepted and
      // this device has forgotten.
      await this.persist()

      try {
        await api.mlsCommit(conversationId, {
          groupId,
          baseEpoch,
          welcome: staged.welcome ? bytesToBase64(staged.welcome) : undefined,
          commit: bytesToBase64(staged.commit),
          // Declared so the server can hold a Commit to the same rule as the roster: in a
          // group, only an admin removes anybody else. It cannot read the Commit to check
          // (see the server's mayRemove), so this is the honest path declaring itself.
          removes: removes.length > 0 ? removes : undefined,
        })
      } catch (e) {
        // Refused, or never got there. Either way this Commit is not the group's history,
        // so it must not become ours.
        this.client.commitRejected(bytes)
        await this.persist()
        if (e instanceof ApiError && e.status === 409) return 'conflict'
        throw e
      }

      this.client.commitAccepted(bytes)
      await this.persist()
      return 'accepted'
    })
  }

  /** Adds devices to the group, as their own leaves, in a single Commit. */
  async commitAdd(
    conversationId: string,
    groupId: string,
    keyPackages: Uint8Array[],
  ): Promise<'accepted' | 'conflict'> {
    return this.commit(conversationId, groupId, (bytes) => {
      const added = this.client.stageAdd(bytes, keyPackages)
      return { welcome: added.welcome, commit: added.commit }
    })
  }

  /**
   * Removes every leaf belonging to each user, in a single Commit — for throwing someone
   * out of the group. Every device they have, not whichever one the tree found first.
   */
  async commitRemoveUsers(
    conversationId: string,
    groupId: string,
    userIds: string[],
  ): Promise<'accepted' | 'conflict'> {
    return this.commit(
      conversationId,
      groupId,
      (bytes) => ({ commit: this.client.stageRemoveUsers(bytes, userIds) }),
      userIds,
    )
  }

  /**
   * Removes the exact leaves named, in a single Commit — for pruning a ghost device while
   * leaving that same person's live devices alone.
   */
  async commitRemoveDevices(
    conversationId: string,
    groupId: string,
    identities: string[],
  ): Promise<'accepted' | 'conflict'> {
    return this.commit(
      conversationId,
      groupId,
      (bytes) => ({ commit: this.client.stageRemoveDevices(bytes, identities) }),
      [...new Set(identities.map((i) => i.slice(0, i.indexOf(':'))))],
    )
  }

  /**
   * Joins from a relayed Welcome. Returns false for a Welcome that is not ours — one
   * addressed to a different device, or to a group we already hold.
   */
  async tryJoin(groupId: string, welcomeBase64: string): Promise<boolean> {
    return this.exclusive(async () => {
      const bytes = groupBytes(groupId)
      if (this.client.hasGroup(bytes)) return true
      try {
        this.client.joinFromWelcome(base64ToBytes(welcomeBase64))
        await this.persist()
        return this.client.hasGroup(bytes)
      } catch {
        // Addressed to another device (every device gets its own Welcome), or already
        // used. Neither is an error worth surfacing.
        return false
      }
    })
  }

  /**
   * Applies a Commit another member produced, if we are actually behind it.
   *
   * `atEpoch` is the epoch the Commit produced. Applying one we are already past is not
   * just wasted work — OpenMLS rejects it, and treating that as a failure would make a
   * routine replay (the SSE echo of a Commit we already have) look like a broken group.
   */
  async applyCommit(groupId: string, commitBase64: string, atEpoch: number): Promise<void> {
    return this.exclusive(async () => {
      const bytes = groupBytes(groupId)
      if (!this.client.hasGroup(bytes)) return
      if (atEpoch > 0 && atEpoch <= this.epochUnlocked(groupId)) return // already applied
      this.client.applyCommit(bytes, base64ToBytes(commitBase64))
      await this.persist()
    })
  }

  async encrypt(groupId: string, plaintext: Uint8Array): Promise<string> {
    return this.exclusive(async () => {
      const ciphertext = this.client.encrypt(groupBytes(groupId), plaintext)
      await this.persist()
      return bytesToBase64(ciphertext)
    })
  }

  /** Decrypts an application message, or returns null for a control message. */
  async decrypt(groupId: string, ciphertextBase64: string): Promise<Uint8Array | null> {
    return this.exclusive(async () => {
      const out = this.client.decrypt(groupBytes(groupId), base64ToBytes(ciphertextBase64))
      await this.persist()
      return out ?? null
    })
  }

  /**
   * The safety number for a conversation: the digits two people compare, out of band, to
   * prove no one is in the middle. Computed from the group's own ratchet tree, so a
   * KeyPackage the server swapped in shows up as a different number.
   *
   * It changes when a member adds or removes a device, because the set of keys in the
   * group really has changed. That is not noise — a device nobody recognises appearing in
   * the group is exactly what this is for.
   */
  async safetyNumber(groupId: string): Promise<string> {
    return this.exclusive(async () => this.client.safetyNumber(groupBytes(groupId)))
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

// --- group lifecycle ------------------------------------------------------
//
// Everything below answers one question: "is this device a leaf of this conversation's
// MLS group, and is every other member's device a leaf too?"

/**
 * Makes sure this device is in the conversation's group, and that every device of every
 * member is too. Returns the group id once this device can encrypt to it, or null while
 * it cannot yet.
 *
 * Null is a normal state, not a failure: a device that has just been added to a group has
 * to wait for a member to notice and admit it. It is not stuck — it has said so
 * (announceDevice), and it will be let in.
 *
 * Deduplicated per conversation. Two callers ask — opening the chat, and sending into one
 * — and if both ran, both would claim KeyPackages and both would try to establish the
 * group.
 */
export function ensureGroup(
  conversation: Conversation,
  myUserId: string,
): Promise<string | null> {
  const inFlight = settling.get(conversation.id)
  if (inFlight) return inFlight

  const run = settleGroup(conversation, myUserId).finally(() => settling.delete(conversation.id))
  settling.set(conversation.id, run)
  return run
}

async function settleGroup(
  conversation: Conversation,
  myUserId: string,
): Promise<string | null> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversation.id)

  // Nobody has established a group yet.
  if (!state.groupId) {
    // Only the creator builds it. The compare-and-set would make a race safe anyway —
    // one of them would simply lose — but a loser has burned the KeyPackages it claimed,
    // and there is no reason to spend them.
    if (conversation.createdBy !== myUserId) return null
    return establishGroup(session, conversation, myUserId)
  }

  // A group exists. Catch up on anything we missed — that may be the very Welcome that
  // lets this device in.
  await catchUp(session, conversation.id, state.groupId)

  if (!(await session.hasGroup(state.groupId))) {
    // The group exists and this device is not in it: a new phone, a browser whose storage
    // was evicted, a member added while we were offline. A device cannot add itself —
    // only a member of the group can Commit — so say so, and a member will admit us.
    //
    // This is where the old code destroyed the group and rebuilt it. That is why a single
    // second device could make a whole conversation unreadable for everyone in it.
    await announceDevice(conversation.id)
    return null
  }

  await reconcileDevices(session, conversation.id, state.groupId)
  return state.groupId
}

/**
 * Builds the conversation's group: one leaf per DEVICE of every member, all in a single
 * Commit so nobody lands an epoch behind.
 *
 * The group id is minted here and claimed through the server's compare-and-set, so it can
 * only ever be set once. If we lose that race — the user's other device got there first —
 * the group we just built is an orphan: we throw it away and join the real one.
 */
async function establishGroup(
  session: Session,
  conversation: Conversation,
  myUserId: string,
): Promise<string | null> {
  const published = await api.mlsDevices(conversation.id)
  const targets = missingDevices(session, published, [])

  // Nobody else is reachable. For a direct chat that is the whole conversation, and the
  // user needs to be told plainly rather than watching a message fail to send. (In a
  // group, the members who ARE reachable still get a working group; the stragglers are
  // added by reconciliation the moment they publish keys.)
  const reachableOthers = targets.some((d) => d.userId !== myUserId)
  if (conversation.kind === 'direct' && !reachableOthers) throw new PeerKeysMissingError()
  if (targets.length === 0) {
    // Not even another device of our own to add. There is no Commit to make, so there is
    // nothing to establish yet — a group of one has no one to talk to.
    throw new PeerKeysMissingError()
  }

  const keyPackages = await claimFor(conversation.id, targets)
  const groupId = crypto.randomUUID()

  await session.createGroup(groupId)
  const result = await session.commitAdd(conversation.id, groupId, keyPackages)
  if (result === 'conflict') {
    // Another device of ours established the group first. What we built is an orphan that
    // nobody will ever join — drop it and settle again, this time joining theirs.
    await session.discardGroup(groupId)
    return settleGroup(conversation, myUserId)
  }
  return groupId
}

/**
 * Brings the group's leaves into line with who is actually in the conversation and what
 * devices they actually have: adds the devices that are missing, prunes the ones that
 * should not be there.
 *
 * This is the heart of it. A group's leaves and the conversation's membership drift apart
 * constantly — someone signs in on a laptop, someone is added, someone's browser storage
 * is evicted — and reconciling them is what keeps every device able to read. It is safe to
 * run from any member and as often as we like: the server's compare-and-set picks one
 * winner, and a loser simply finds nothing left to do.
 *
 * The membership is re-read from the SERVER, never taken from a Conversation the caller
 * happens to be holding. That is not defensiveness, it is the fix for a real bug: when a
 * member was added, every other member's live-event handler ran this with the conversation
 * it had fetched BEFORE the add — so the newcomer looked like a stranger, and the first
 * member to react promptly removed them again.
 */
async function reconcileDevices(
  session: Session,
  conversationId: string,
  groupId: string,
): Promise<void> {
  // Leaves we would prune but are not allowed to (we are not an admin). Remembered so the
  // loop does not keep retrying the same refusal instead of getting on with the adds.
  const pruned: string[] = []

  for (let attempt = 0; attempt < COMMIT_ATTEMPTS; attempt++) {
    const members = await api.listConversationMembers(conversationId)
    const memberIds = members.map((m) => m.userId)
    const published = await api.mlsDevices(conversationId)
    const leaves = await session.memberIdentities(groupId)

    // Prune first — a leaf that should not be there must not still be in the group when we
    // go and encrypt to it.
    //
    // Unless we are not allowed to: in a group, only an admin removes anybody. A non-admin
    // is refused, and that is fine — pruning a departed member or a ghost device is
    // hygiene, and it waits for an admin. What must NOT happen is the refusal stopping us
    // from ADDING the devices that are missing, which is the part somebody is waiting on.
    const stale = pruned.length > 0 ? [] : staleLeaves(session, leaves, memberIds, published)
    if (stale.length > 0) {
      let result: 'accepted' | 'conflict'
      try {
        result = await session.commitRemoveDevices(conversationId, groupId, stale)
      } catch (e) {
        if (!(e instanceof ApiError && e.status === 403)) throw e
        pruned.push(...stale) // not ours to remove; get on with the adds
        continue
      }
      if (result === 'conflict') await catchUp(session, conversationId, groupId)
      continue // look again either way: the group has changed shape
    }

    const missing = missingDevices(session, published, leaves)
    if (missing.length === 0) return

    const keyPackages = await claimFor(conversationId, missing)
    if (keyPackages.length === 0) return // they published nothing after all
    const result = await session.commitAdd(conversationId, groupId, keyPackages)
    if (result === 'accepted') return
    // Refused: another member committed first. Apply their Commit and look again — the
    // device we were adding may already be in, in which case there is nothing left to do.
    await catchUp(session, conversationId, groupId)
  }
}

/**
 * Leaves that have no business being in the group: `{identities, users}`.
 *
 * Two kinds, and they must be pruned differently.
 *
 *   * A DEPARTED MEMBER — every leaf they hold goes, so removing them removes their phone
 *     and their laptop, not whichever one the group found first.
 *   * A GHOST DEVICE — a member who is staying, but one of whose devices no longer exists:
 *     its KeyPackages are gone from the directory because that browser was cleared and came
 *     back with a new identity. Only that leaf goes. Pruning it by user would take the
 *     person's live phone out with it.
 *
 * A user with no published devices at all is left alone: that is what a member who has
 * never opened Pheme looks like, and also what one looks like for the instant between
 * purging a dead identity's KeyPackages and publishing the new one's.
 */
function staleLeaves(
  session: Session,
  leaves: string[],
  memberIds: string[],
  published: Record<string, string[]>,
): string[] {
  const members = new Set(memberIds)
  const out: string[] = []
  for (const leaf of leaves) {
    if (leaf === session.identity) continue // never prune ourselves
    const userId = leaf.slice(0, leaf.indexOf(':'))
    if (!userId) continue
    if (!members.has(userId)) {
      out.push(leaf) // departed member
      continue
    }
    const devices = published[userId] ?? []
    if (devices.length === 0) continue // cannot tell; leave them be
    if (!devices.includes(deviceOf(leaf))) out.push(leaf) // ghost device
  }
  return out
}

/** The published devices that are not already leaves of the group, excluding our own. */
function missingDevices(
  session: Session,
  published: Record<string, string[]>,
  leaves: string[],
): { userId: string; deviceId: string }[] {
  const have = new Set(leaves)
  const out: { userId: string; deviceId: string }[] = []
  for (const [userId, deviceIds] of Object.entries(published)) {
    for (const deviceId of deviceIds) {
      // Our own device is never claimed for: it holds the group (or is creating it), and
      // claiming our own KeyPackage would burn one for nothing.
      if (deviceIdentity(userId, deviceId) === session.identity) continue
      if (have.has(deviceIdentity(userId, deviceId))) continue
      out.push({ userId, deviceId })
    }
  }
  return out
}

/** Claims one KeyPackage per device. A device that has published none is skipped. */
async function claimFor(
  conversationId: string,
  devices: { userId: string; deviceId: string }[],
): Promise<Uint8Array[]> {
  try {
    const claimed = await api.claimKeyPackages(conversationId, devices)
    return claimed.map((c) => base64ToBytes(c.keyPackage))
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return []
    throw e
  }
}

/**
 * Applies every Commit the group has made since this device last looked, in order.
 *
 * Without this a device that was closed while the group changed can never decrypt again:
 * MLS will not let it read the new epoch until it has applied the Commit that created it,
 * and that Commit may be far outside the page of history the chat view loads. Asking the
 * server for the Commits past our epoch makes catching up exact and bounded.
 */
export async function catchUp(
  session: Session,
  conversationId: string,
  groupId: string,
): Promise<void> {
  const from = await session.epoch(groupId)
  const messages = await api.mlsCommitsSince(conversationId, from)

  for (const msg of messages) {
    if (msg.contentType === MLS_WELCOME) {
      // Only one of these is addressed to this device; the rest fail harmlessly, without
      // touching our KeyPackages (a Welcome names the exact package it is for).
      if (!(await session.hasGroup(groupId))) {
        await session.tryJoin(groupId, msg.ciphertext)
      }
      continue
    }
    if (msg.contentType === MLS_COMMIT) {
      await session.applyCommit(groupId, msg.ciphertext, msg.mlsEpoch ?? 0).catch(() => {
        // A Commit we cannot apply is one from a branch we are not on, or one we already
        // have. Neither is recoverable by retrying, and neither should stop us applying
        // the rest.
      })
    }
  }
}

/**
 * Tells the conversation that this device holds no group and needs to be let in.
 *
 * Carries no key material — it is a request to be added, not a Welcome — so, unlike the
 * message it replaces, it cannot be used to destroy anybody's KeyPackages.
 */
async function announceDevice(conversationId: string): Promise<void> {
  await api
    .sendChatMessage(conversationId, bytesToBase64(new Uint8Array([1])), MLS_DEVICE)
    .catch(() => {
      // Best effort. The next time this chat is opened it announces again, and any member
      // who opens it will reconcile and find this device missing regardless.
    })
}

/**
 * Responds to another device announcing itself: add it, if we hold the group.
 *
 * Every member who has the conversation open does this. They will race, and that is fine
 * — the server's compare-and-set lets exactly one Commit through, and the others find the
 * device already added and stop.
 */
export async function admitAnnouncedDevice(
  conversation: Conversation,
  myUserId: string,
): Promise<void> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversation.id)
  if (!state.groupId || !(await session.hasGroup(state.groupId))) return
  await reconcileDevices(session, conversation.id, state.groupId)
}

/**
 * Adds a user to a group conversation: server-side membership first (so they are
 * authorised to read the Welcome relayed to them), then every one of their devices is
 * added to the MLS group as its own leaf.
 */
export async function addGroupMember(
  conversationId: string,
  myUserId: string,
  newUserId: string,
): Promise<void> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId || !(await session.hasGroup(state.groupId))) {
    throw new Error('this device is not in the conversation\'s encrypted group yet')
  }

  // Membership first, then the group. It has to be that way round now that the key
  // directory is scoped to a conversation: we cannot ask which devices someone has until
  // they are in it. If it turns out they have none — they have never opened Pheme — take
  // them back off the roster, so an admin is told they could not be reached rather than
  // being left with a member who can never read anything.
  await api.addConversationMember(conversationId, newUserId)
  const devices = await api.mlsDevices(conversationId)
  if ((devices[newUserId] ?? []).length === 0) {
    await api.removeConversationMember(conversationId, newUserId).catch(() => {})
    throw new PeerKeysMissingError()
  }

  await reconcileDevices(session, conversationId, state.groupId)
}

/**
 * Removes a user from a group conversation. The MLS Commit goes first — that is what
 * actually cuts them off — and it removes EVERY device they have, not just the one the
 * group happened to find. Then their server-side membership is dropped.
 */
export async function removeGroupMember(
  conversationId: string,
  myUserId: string,
  memberUserId: string,
): Promise<void> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)

  if (memberUserId === myUserId) {
    // Leaving. MLS forbids committing your own removal (CannotRemoveSelf), so this is not
    // a Commit at all: drop the membership, and destroy the group state on this device so
    // nothing here can read the conversation any more. The members who remain prune the
    // leaves we leave behind the next time they reconcile.
    await api.removeConversationMember(conversationId, memberUserId)
    if (state.groupId) await session.discardGroup(state.groupId)
    return
  }

  if (!state.groupId || !(await session.hasGroup(state.groupId))) {
    throw new Error('this device is not in the conversation\'s encrypted group yet')
  }

  for (let attempt = 0; attempt < COMMIT_ATTEMPTS; attempt++) {
    const result = await session.commitRemoveUsers(conversationId, state.groupId, [memberUserId])
    if (result === 'accepted') break
    // Somebody else moved the group. Catch up and remove them from the epoch that
    // actually happened — they must not be left in it.
    await catchUp(session, conversationId, state.groupId)
    if (attempt === COMMIT_ATTEMPTS - 1) {
      throw new Error('could not remove that member — the group changed underneath us')
    }
  }
  await api.removeConversationMember(conversationId, memberUserId)
}

/** The safety number for a conversation, or '' before its group exists. */
export async function conversationSafetyNumber(
  conversationId: string,
  myUserId: string,
): Promise<string> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId || !(await session.hasGroup(state.groupId))) return ''
  return session.safetyNumber(state.groupId)
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
  settling.clear()
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
  // Validate the recovered blob really is a client state before committing it — and read
  // back WHICH DEVICE it belongs to.
  const restored = MlsClient.fromState(state)
  const restoredDevice = deviceOf(restored.identity)

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
    // Adopt the identity we just restored. The groups inside that state hold leaves under
    // the ORIGINAL device's name, and its published KeyPackages are filed under it — so
    // this browser has to answer to that device id, not to one of its own. Keeping a
    // local id here would leave the restored client unable to be added to anything: it
    // would publish keys as one device and hold leaves as another.
    if (restoredDevice) saveWebDeviceId(restoredDevice)

    restoreNeeded = false
    ready = null
    readyUserId = ''
    settling.clear()
    return true
  })
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
