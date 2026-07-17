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
import { cacheContent, clearPreviews, exportAllContents, importContents } from './chatCache'
import { clearSafetyPins } from './safety'
import { loadMlsDeviceId, saveMlsDeviceId } from './device'
import { serializeContent } from './chatContent'
import { CALL_EVENT, writeCallEvent } from './callEvent'
import type { CallEvent } from './callEvent'
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

/**
 * How long this device has been announcing itself and waiting to be let in, by conversation.
 *
 * In memory, so it resets when the tab does. That is deliberate: a reset is a last resort, and
 * it should take a sustained period of a live device asking and getting nowhere — not a stale
 * timestamp from a week ago.
 */
const waitingSince = new Map<string, number>()

/**
 * How long a device waits to be admitted before concluding that nobody is coming.
 *
 * Long enough that a member who was going to admit it would have: the app admits announced
 * devices from anywhere, so it needs somebody to have Pheme open at all, not to be looking at
 * the right conversation. Short enough that a person staring at "setting up encryption" is not
 * left there.
 */
const STUCK_MS = 90_000

function stuckFor(conversationId: string): number {
  const since = waitingSince.get(conversationId)
  if (since === undefined) {
    waitingSince.set(conversationId, Date.now())
    return 0
  }
  return Date.now() - since
}

function clearStuck(conversationId: string): void {
  waitingSince.delete(conversationId)
}

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
        saveMlsDeviceId(deviceId)
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
        retiredDeviceId = loadMlsDeviceId() ?? ''
        deviceId = crypto.randomUUID()
        saveMlsDeviceId(deviceId)
        client = new MlsClient(userId, deviceId)
      }

      const s = new Session(client, userId, deviceId, await storedVersion(), await storedEpoch())
      if (!keep) await s.persist()
      await idbSet(OWNER_KEY, new TextEncoder().encode(userId))
      return { s, fresh: !keep, retiredDeviceId }
    })

    // Everything below is HOUSEKEEPING, and none of it blocks the session.
    //
    // Purging a dead identity's KeyPackages and topping up the stock of live ones are both about being
    // REACHABLE — about somebody else being able to start a chat with this device. Neither has any
    // bearing on whether this device can read the conversations it is already in.
    //
    // They used to be awaited, so the first conversation opened after a reload waited on two network
    // calls before it could show a message — and while it waited, the composer said encryption was
    // still being set up. It was not. It was publishing KeyPackages.
    //
    // Outside the lock (these take it themselves; taking it twice would deadlock).
    void (async () => {
      if (session.retiredDeviceId) {
        await api.deleteKeyPackages(session.retiredDeviceId).catch(() => {})
      }
      await session.s.replenishKeyPackages().catch(() => {
        // A peer starting a chat finds no package and has to retry. Nothing we are already in breaks.
      })
    })()

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
      // The users this Commit removes leaves of. A legacy leaf has no ':' and IS the user
      // id whole — slicing to indexOf(-1) used to chop its last character instead, and the
      // server was told a user that does not exist was being removed.
      [...new Set(identities.map((i) => {
        const sep = i.indexOf(':')
        return sep === -1 ? i : i.slice(0, sep)
      }))],
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
   * Decrypts against whichever of the conversation's groups the message actually belongs to.
   *
   * A conversation can have had more than one group: when every device that held one lost its
   * key material, the only way to talk again was to start a fresh one. The old group is not
   * deleted, though, and neither is anything said to it — so a message from before the reset is
   * still perfectly readable by a device that still holds the old group, and this is where that
   * happens.
   *
   * Tries the current group first, since that is where all but the oldest messages live.
   */
  async decryptAny(groupIds: string[], ciphertextBase64: string): Promise<Uint8Array | null> {
    for (const groupId of groupIds) {
      try {
        const out = await this.decrypt(groupId, ciphertextBase64)
        if (out) return out
      } catch {
        // Not this group's message — or not one we can read. Try the next.
      }
    }
    return null
  }

  /**
   * Derives a secret from the group for something outside MLS's own messaging, together
   * with the epoch it came from.
   *
   * The epoch comes back with it because the exporter is per-epoch and the caller MUST pin
   * it. Both are read under the same lock, so the pair is always consistent — read
   * separately, a Commit landing in between would hand back a key from one epoch labelled
   * with another, and the two ends of a call would talk past each other.
   *
   * Returns null when this device does not hold the group.
   */
  async exportSecret(
    groupId: string,
    label: string,
    context: Uint8Array,
    length: number,
  ): Promise<{ secret: Uint8Array; epoch: number } | null> {
    return this.exclusive(async () => {
      const bytes = groupBytes(groupId)
      if (!this.client.hasGroup(bytes)) return null
      // A pure read: no ratchet mutation, so nothing to persist. That is the whole reason
      // signalling uses this rather than MLS application messages.
      const secret = this.client.exportSecret(bytes, label, context, length)
      return { secret, epoch: this.epochUnlocked(groupId) }
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

  const run = settleGroup(conversation, myUserId)
    .then((groupId) => {
      // The group we ended up in goes at the FRONT of what we can read.
      //
      // settleGroup records what the server told it, but the server may have told it there was
      // no group at all — and then this device went and established one. Without this, the
      // device that created the group could not decrypt a single message in it, including its
      // own conversation's entire history.
      if (groupId) rememberReadableGroup(conversation.id, groupId)
      return groupId
    })
    .finally(() => settling.delete(conversation.id))
  settling.set(conversation.id, run)
  return run
}

async function settleGroup(
  conversation: Conversation,
  myUserId: string,
): Promise<string | null> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversation.id)

  // Everything this device could read a message from. A message from before a reset was
  // encrypted to a group that is no longer current, and it is still perfectly readable by a
  // device that still holds that group.
  const allGroups = [state.groupId, ...(state.priorGroupIds ?? [])].filter(Boolean)
  readableGroups.set(conversation.id, allGroups)
  // Write it down, so the NEXT open of this conversation needs none of this.
  if (state.groupId) void rememberGroupIds(conversation.id, allGroups)

  // Nobody has established a group yet, so somebody must.
  //
  // ANY member does it, not just the conversation's creator. Reserving it for the creator
  // looked tidy — it avoids a loser burning the KeyPackages it claimed — but it is a
  // deadlock: if the creator never opens the chat, every other member sits at "setting up
  // encryption" forever, with no way to make progress and nothing to tell them why. The
  // server's compare-and-set already makes a race safe; one of them simply loses. A wasted
  // KeyPackage is a rounding error next to a conversation that never works.
  if (!state.groupId) {
    return establishGroup(session, conversation, myUserId)
  }

  // A group exists. Catch up on anything we missed — that may be the very Welcome that
  // lets this device in.
  //
  // Best effort, like everything else below: a device that already holds the group must not
  // be broken by a failure to fetch. See the guard on reconcileDevices.
  await catchUp(session, conversation.id, state.groupId).catch(() => {})

  if (!(await session.hasGroup(state.groupId))) {
    // The group exists and this device is not in it: a new phone, a browser whose storage
    // was evicted, a member added while we were offline. A device cannot add itself —
    // only a member of the group can Commit — so say so, and a member will admit us.
    //
    // This is where the old code destroyed the group and rebuilt it. That is why a single
    // second device could make a whole conversation unreadable for everyone in it.
    await announceDevice(conversation.id)

    // …unless nobody is coming.
    //
    // Every device that held this group can have lost its key material — a browser cleared, an
    // iOS PWA whose storage was evicted on the seven-day rule — and nothing says that cannot
    // happen to both people in the same week. Then there is no member left who can admit
    // anybody, every device sits here announcing itself, and the conversation is dead forever.
    //
    // So after waiting long enough that a member who WAS coming would have come, retire the
    // group and start a new one. This is safe to do without being certain, because it destroys
    // nothing: the old group is remembered, not deleted, and anyone who still holds it can
    // still read every message ever sent to it. The worst a premature reset can do is make
    // everyone rejoin a fresh group. Being permanently unable to talk is worse than that.
    if (stuckFor(conversation.id) > STUCK_MS) {
      clearStuck(conversation.id)
      await api.mlsResetGroup(conversation.id).catch(() => {})
      return settleGroup(conversation, myUserId)
    }
    return null
  }
  clearStuck(conversation.id)

  // Holding the group is not the same as being able to FOLLOW it. If the catch-up above
  // left this device behind the server's epoch, its ratchet may have forked — see the
  // fork self-healing block for what happens next.
  if (await stillBehind(session, conversation.id, state)) {
    observeWedge(conversation.id, myUserId)
  }

  // WE HOLD THE GROUP. Nothing below this line may take that away.
  //
  // Reconciliation is hygiene — admitting somebody's new device, pruning a ghost. It talks to
  // the network and it builds Commits, so it can fail: a stale KeyPackage that will not
  // validate, a request that times out, a Commit the server refuses for a reason we did not
  // anticipate. None of that says anything about whether THIS device can read THIS
  // conversation, and it must not be allowed to imply otherwise.
  //
  // It was allowed to. An exception here propagated out of ensureGroup, the chat route caught
  // it and left the group id empty, and because decryption is gated on that id the device
  // rendered every message as "Not available on this device", refused to send, and reported
  // that encryption was still being set up — while holding perfectly good keys the whole time.
  // A failure to tidy up bricked a working conversation.
  await reconcileDevices(session, conversation.id, state.groupId).catch(() => {})
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

  const claimed = await claimFor(conversation.id, targets)
  const groupId = crypto.randomUUID()

  await session.createGroup(groupId)
  const result = await session.commitAdd(
    conversation.id,
    groupId,
    claimed.map((c) => c.keyPackage),
  )
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
  // Not while a call is up. Every add and every prune here is a Commit, a Commit moves the
  // epoch, and the epoch is what the call's encryption key is derived from — so reconciling
  // mid-call would pull the key out from under a conversation two people are having right
  // now, to admit a device that can perfectly well wait thirty seconds.
  //
  // Nothing is lost by deferring: the device that wants in has announced itself, and the
  // next time anyone opens the chat — including the moment this call ends — it is admitted.
  if (callsInProgress > 0) return

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

    const claimed = await claimFor(conversationId, missing)
    if (claimed.length === 0) return // they published nothing after all
    const result = await session.commitAdd(
      conversationId,
      groupId,
      claimed.map((c) => c.keyPackage),
    )
    if (result === 'accepted') {
      // Verify each add actually MATERIALISED: the leaf a claimed package produces answers
      // to whatever credential is inside the bytes, and a stale directory entry can carry
      // somebody's long-dead legacy identity. A device that is still not a leaf after its
      // own accepted Add can never be added by this route — remember that, or the next
      // reconcile claims the same package and commits the same no-op forever.
      const now = new Set(await session.memberIdentities(groupId))
      const duds = claimed.filter((c) => !now.has(deviceIdentity(c.userId, c.deviceId)))
      if (duds.length === 0) return
      for (const dud of duds) {
        zombieDevices.add(deviceIdentity(dud.userId, dud.deviceId))
        // Our own dead device's packages we can actually purge — only the owner may — which
        // stops every OTHER member's reconcile from walking into the same trap.
        if (dud.userId === session.userId) {
          void api.deleteKeyPackages(dud.deviceId).catch(() => {})
        }
      }
      continue // the prune round removes whatever leaf the dud package created
    }
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
    const sep = leaf.indexOf(':')
    if (sep === -1) {
      // A LEGACY leaf — an identity from before leaves carried a device id, so it names a
      // person and no device. No current client can hold its keys (legacy state is
      // discarded on load), so it can never read anything, and it never leaves on its own.
      // Prune it deliberately. It used to be pruned by accident — slice(0, -1) mangled the
      // user id, which read as a departed member — which happened to do the right thing
      // here while doing the wrong thing everywhere else.
      out.push(leaf)
      continue
    }
    const userId = leaf.slice(0, sep)
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

/**
 * Devices whose published KeyPackage turned out to be a TRAP: it was claimed and committed, and the
 * leaf it produced does not answer to `userId:deviceId` at all. That is what a zombie from the
 * legacy directory looks like — a package published under a device id whose credential inside is
 * the old bare-user identity. Adding it creates a leaf nobody can ever hold, the device stays
 * "missing", and every reconcile does it again: half of an add/prune war that once burned five
 * hundred epochs in a single conversation.
 *
 * Remembered here so this session never claims for them again; a zombie of OUR OWN user
 * additionally gets its published packages purged (only the owner may), which ends it for everyone.
 */
const zombieDevices = new Set<string>()

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
      if (zombieDevices.has(deviceIdentity(userId, deviceId))) continue
      out.push({ userId, deviceId })
    }
  }
  return out
}

/**
 * Claims one KeyPackage per device, keeping WHICH device each one was claimed for. A device that
 * has published none is skipped.
 */
async function claimFor(
  conversationId: string,
  devices: { userId: string; deviceId: string }[],
): Promise<{ userId: string; deviceId: string; keyPackage: Uint8Array }[]> {
  try {
    const claimed = await api.claimKeyPackages(conversationId, devices)
    return claimed.map((c) => ({
      userId: c.userId,
      deviceId: c.deviceId,
      keyPackage: base64ToBytes(c.keyPackage),
    }))
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

// --- fork self-healing ------------------------------------------------------
//
// A device can hold a group and still be unable to FOLLOW it. If its ratchet ever forked —
// the July 2026 commit storm managed it by interleaving adds of the same KeyPackage — every
// Commit the others make is rejected by OpenMLS from then on, and rejected silently: the
// device keeps its group id, so nothing announces, nothing resets, and nothing ever
// recovers. It just stops being able to read anything new, forever, while looking joined.
//
// The fingerprint is unmistakable, though: the server's epoch is ahead, every Commit that
// would close the gap is fetchable, and applying them changes nothing. A device that is
// merely behind closes the gap the moment catchUp runs; a forked one cannot, ever.
//
// So a settle or live catch-up that leaves the device still behind puts the conversation
// under observation, and a fresh check after a grace period decides. Still behind then →
// the group is beyond following, and it is retired (mlsResetGroup) — which destroys
// nothing: the old group is remembered and stays readable, and a fresh group comes up that
// everyone can actually be in. Every wedged member does the same and the server's
// compare-and-set lets exactly one reset through.

/** Long enough to outlast a flurry of in-flight Commits; short enough that a person watching a broken chat sees it come back. */
const WEDGE_GRACE_MS = 20_000

/** Conversations under observation for a forked ratchet, by id → the pending recheck. */
const wedgeTimers = new Map<string, ReturnType<typeof setTimeout>>()

/**
 * True when this device holds the group, the server's epoch is ahead, and catching up does
 * not close the gap — the one state an intact ratchet can never be in after a catch-up.
 */
async function stillBehind(
  session: Session,
  conversationId: string,
  state: { groupId: string; epoch: number },
): Promise<boolean> {
  if (!state.groupId || !(await session.hasGroup(state.groupId))) return false
  if ((await session.epoch(state.groupId)) >= state.epoch) return false
  await catchUp(session, conversationId, state.groupId).catch(() => {})
  return (await session.epoch(state.groupId)) < state.epoch
}

/** Puts a conversation under observation; the recheck heals it if the wedge is real. */
function observeWedge(conversationId: string, myUserId: string): void {
  if (wedgeTimers.has(conversationId)) return
  const timer = setTimeout(() => {
    void (async () => {
      try {
        const session = await mlsSession(myUserId)
        const state = await api.mlsGroupState(conversationId)
        if (!(await stillBehind(session, conversationId, state))) return
        // Confirmed: this ratchet can never follow the group again. Retire it and settle
        // into whatever comes next.
        await api.mlsResetGroup(conversationId)
        const conversation = await api.getConversation(conversationId)
        await ensureGroup(conversation, myUserId)
      } catch {
        // Nothing lost — the next settle of this conversation observes again.
      } finally {
        wedgeTimers.delete(conversationId)
      }
    })()
  }, WEDGE_GRACE_MS)
  wedgeTimers.set(conversationId, timer)
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
  conversationId: string,
  myUserId: string,
): Promise<void> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId || !(await session.hasGroup(state.groupId))) return
  await reconcileDevices(session, conversationId, state.groupId)
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

// --- call keys ------------------------------------------------------------
//
// A voice call's media is encrypted by WebRTC itself (DTLS-SRTP), but the key exchange for
// that is authenticated by a fingerprint carried in the SDP — and the SDP goes through our
// server. A server that could rewrite the fingerprint could put itself in the middle of the
// call and listen to all of it.
//
// So the SDP is encrypted under a key derived from the conversation's MLS group, which the
// server does not have. That makes a call exactly as private as the chat it is placed from,
// and the safety number two people already compare covers both.

/** The exporter label. Changing it changes every derived key, so it is versioned. */
const CALL_LABEL = 'pheme-call-v1'

/**
 * The key a given DEVICE encrypts its call signalling with, plus the epoch it was derived
 * at. Returns null when this device does not hold the conversation's group.
 *
 * Keyed per sending device, not per call: all of a person's devices would otherwise encrypt
 * under one key with independently chosen nonces, and an AES-GCM nonce collision between
 * two of them leaks the authentication key. This removes the possibility rather than
 * trusting 96 random bits not to repeat.
 *
 * Every member device can derive any sender's key — that is how they decrypt it — so this
 * gives GROUP authenticity, not SENDER authenticity. Between two people that is meaningless
 * (a forger could only be you or the person you are talking to). It would not be sound for
 * group calls without also signing the payload.
 */
export async function callKeyFor(
  conversationId: string,
  myUserId: string,
  callId: string,
  senderIdentity: string,
): Promise<{ secret: Uint8Array; epoch: number } | null> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId) return null
  const context = new TextEncoder().encode(`${callId}|${senderIdentity}`)
  return session.exportSecret(state.groupId, CALL_LABEL, context, 32)
}

/**
 * Every group of a conversation this device could read a message from: the current one first,
 * then any that have been retired.
 *
 * Recorded by settleGroup, so it is whatever the server last told us. A message from before a
 * reset was encrypted to a group that is no longer current, and it is still readable — that is
 * the whole point of remembering the old ones rather than deleting them.
 */
const readableGroups = new Map<string, string[]>()

// ---------------------------------------------------------------------------------------------
// WHICH GROUP EACH CONVERSATION IS, remembered across reloads.
//
// It is the one thing about a conversation's encrypted group that this device cannot work out for
// itself: the group's ID. Everything else it already knows — it is holding the ratchet.
//
// And the id never moves. The server sets it once, through a compare-and-set, and only a reset can
// change it — which is rare, deliberate, and remembers the old id anyway.
//
// So asking the server for it on every open is asking a question we already know the answer to, and
// paying several round trips to hear it — during which nothing can be decrypted, nothing renders, and
// the only thing the UI can honestly say is that encryption is still being set up. It is not. It is
// waiting for the post.
// ---------------------------------------------------------------------------------------------

const GROUPS_KEY = 'group-ids'

type GroupIdMap = Record<string, string[]>

async function storedGroupIds(): Promise<GroupIdMap> {
  const bytes = await idbGet(GROUPS_KEY)
  if (!bytes) return {}
  try {
    const parsed: unknown = JSON.parse(new TextDecoder().decode(bytes))
    return typeof parsed === 'object' && parsed !== null ? (parsed as GroupIdMap) : {}
  } catch {
    return {}
  }
}

/** Writes down what a conversation's groups are, so the next open needs no network at all. */
async function rememberGroupIds(conversationId: string, groupIds: string[]): Promise<void> {
  if (groupIds.length === 0) return

  const all = await storedGroupIds()
  const existing = all[conversationId]
  if (existing && existing.length === groupIds.length && existing.every((g, i) => g === groupIds[i])) {
    return
  }

  all[conversationId] = groupIds
  await idbSet(GROUPS_KEY, new TextEncoder().encode(JSON.stringify(all)))
}

/**
 * Makes a conversation READABLE from what this device already knows, without asking the server.
 *
 * Returns true when there was something to prime — enough to start decrypting immediately, with no
 * network at all.
 *
 * ------------------------------------------------------------------------------------------------
 * WHAT THIS DOES NOT DO, AND MUST NOT: hand back a group id to ENCRYPT to, or claim membership.
 *
 * A remembered id is enough to read. It is not proof of membership. If another device reset the
 * conversation, the current group is one this tab has never heard of — and a client that trusted this
 * cache would cheerfully seal its next message to the RETIRED group. Everyone else is on the new one.
 * Nobody could read it. Nothing would report an error, because nothing went wrong: the message was
 * encrypted perfectly, to a group nobody is in.
 *
 * Reading cannot lie in that direction. A message from the old group still opens with the old group,
 * and one from the new group simply does not open — a miss, not a forgery, and confirmGroup repairs it
 * a moment later.
 *
 * Only the server can say which group is current. See confirmGroup.
 * ------------------------------------------------------------------------------------------------
 */
export async function primeGroup(conversationId: string): Promise<boolean> {
  const known = (await storedGroupIds())[conversationId]
  if (!known || known.length === 0) return false

  // Every group the conversation has ever had. A message from before a reset was encrypted to one that
  // is no longer current, and it is still perfectly readable by a device that still holds it.
  readableGroups.set(conversationId, known)
  return true
}

/**
 * Asks the server which group is current, and whether this device is in it. ONE round trip.
 *
 * The authoritative answer, and the only one that may enable sending or calling. Deliberately separate
 * from ensureGroup, which also catches up on Commits, admits new devices and prunes ghosts — all worth
 * doing, all worth doing in the background, none of it worth making the user wait for.
 *
 * It also REPAIRS THE CACHE. If another device reset the conversation, the id we had written down is
 * retired, and this is where we find out: the old group stays readable, the new one becomes the one we
 * must be admitted to, and ensureGroup does the admitting.
 */
export async function confirmGroup(
  conversationId: string,
  myUserId: string,
): Promise<string | null> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)

  const all = [state.groupId, ...(state.priorGroupIds ?? [])].filter(Boolean)
  if (all.length > 0) {
    readableGroups.set(conversationId, all)
    void rememberGroupIds(conversationId, all)
  }

  if (!state.groupId) return null
  return (await session.hasGroup(state.groupId)) ? state.groupId : null
}

/** Puts a group at the front of what a conversation can be decrypted against. */
function rememberReadableGroup(conversationId: string, groupId: string): void {
  const groups = readableGroups.get(conversationId) ?? []
  const all = [groupId, ...groups.filter((g) => g !== groupId)]
  readableGroups.set(conversationId, all)
  // On disk too. Best effort: a failure costs one round trip next time, not correctness.
  void rememberGroupIds(conversationId, all)
}

/**
 * Decrypts a chat message against whichever of the conversation's groups it belongs to.
 *
 * Returns null when this device cannot read it at all — which is a real answer, not a failure:
 * MLS gives a device no access to what was said before it joined.
 */
export async function decryptChatMessage(
  conversationId: string,
  myUserId: string,
  ciphertextBase64: string,
): Promise<Uint8Array | null> {
  const groups = readableGroups.get(conversationId)
  if (!groups || groups.length === 0) return null
  const session = await mlsSession(myUserId)
  return session.decryptAny(groups, ciphertextBase64)
}

/** This device's MLS identity (`userId:deviceId`) — who a call signal is from. */
export async function myIdentity(myUserId: string): Promise<string> {
  return (await mlsSession(myUserId)).identity
}

/**
 * Brings this device up to `epoch` so it can derive a call key that was minted there.
 *
 * The exporter only ever exports from the CURRENT epoch, so two devices at different epochs
 * derive different keys and cannot talk. A device that is behind can catch up — that is what
 * this does. A device that is AHEAD cannot go back, and must tell the caller so instead.
 *
 * Returns the epoch this device ended up at.
 */
export async function catchUpToEpoch(
  conversationId: string,
  myUserId: string,
  epoch: number,
): Promise<number> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId) return 0
  const current = await session.epoch(state.groupId)
  if (current >= epoch) return current
  await catchUp(session, conversationId, state.groupId)
  return session.epoch(state.groupId)
}

/**
 * Posts the record of a call into the conversation, encrypted to the group like anything else.
 *
 * Only the caller does this, and only for a call that was never answered — so exactly one
 * message is written, by the one device that knows the call rang out. It is a real message: the
 * other end reads it from its own history, on every device, after a reload, forever. Nothing
 * about it is a local UI flourish.
 *
 * Silently does nothing if this device does not hold the group. A device that cannot encrypt is
 * a device whose call could not have happened either, and there is nobody to tell.
 */
export async function postCallEvent(
  conversationId: string,
  myUserId: string,
  event: CallEvent,
): Promise<void> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId || !(await session.hasGroup(state.groupId))) return

  const body = writeCallEvent(event)
  const ciphertext = await session.encrypt(state.groupId, serializeContent({ body }))
  const msg = await api.sendChatMessage(conversationId, ciphertext, CALL_EVENT)

  // Write it into the local cache, because we will never be able to decrypt it: MLS destroys the
  // message key on encrypt, so a sender cannot read what it sent. Without this the caller — the
  // one person who knows the call went unanswered — would see its own record of it sealed.
  await cacheContent(conversationId, msg.id, { body })
}

/**
 * Applies every Commit the server has, so this device is at the group's current epoch.
 *
 * A caller MUST do this before it derives a call key. The exporter only exports from the
 * CURRENT epoch, and a device that is behind — someone else's phone was admitted to the group
 * an hour ago and nothing has made this tab notice — would seal its invite under an epoch its
 * peer has already left behind. The peer cannot go back to it (MLS has no way to export a past
 * epoch), so it silently cannot read the invite, and the call rings out with no way to say why.
 *
 * The recipient can survive being behind — it catches up to the epoch named in the header. It
 * cannot survive being ahead. So the sender is the one that has to be current, and this is
 * where it becomes current.
 *
 * Returns the epoch this device ended up at.
 */
export async function catchUpToLatest(conversationId: string, myUserId: string): Promise<number> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId) return 0
  await catchUp(session, conversationId, state.groupId)
  const epoch = await session.epoch(state.groupId)
  // Caught up and still behind: the fingerprint of a forked ratchet. Put the conversation
  // under observation — the recheck decides, and heals if it is real.
  if (epoch < state.epoch && (await session.hasGroup(state.groupId))) {
    observeWedge(conversationId, myUserId)
  }
  return epoch
}

/**
 * Holds the group's membership still for the duration of a call.
 *
 * Reconciliation adds a member's newly signed-in device to the group, which is a Commit, and
 * a Commit moves the epoch — which moves the call key underneath a call that is already
 * ringing. The change is not urgent: the new device waits a few seconds and is admitted the
 * moment the call ends.
 *
 * A counter and not a boolean, because a second call can start before the first has finished
 * tearing down, and the first one's cleanup must not unfreeze the second.
 */
let callsInProgress = 0

export function freezeGroupForCall(): () => void {
  callsInProgress++
  let released = false
  return () => {
    if (released) return // a release must be idempotent; callers run it in finally blocks
    released = true
    callsInProgress = Math.max(0, callsInProgress - 1)
  }
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
  // A heal that fires after the keys it was healing are gone would reset a group the next
  // account has no stake in.
  for (const timer of wedgeTimers.values()) clearTimeout(timer)
  wedgeTimers.clear()
  // The recovery passphrase must not survive a logout, nor an auto-backup fire under the
  // next account on a shared device.
  sessionPassphrase = null
  if (autoBackupTimer) {
    clearTimeout(autoBackupTimer)
    autoBackupTimer = null
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
  return (await api.getKeyBackup(true)) != null
}

/**
 * A sanity ceiling on the sealed transcript, matched to the server's per-upload bound — not a
 * limit on history. Past it, the KEYS still back up (recoverable conversation, missing
 * scrollback) rather than failing the whole backup. Real text histories sit far below this.
 */
const MAX_TRANSCRIPT_BACKUP_BYTES = 256 * 1024 * 1024

/**
 * The recovery passphrase, held IN MEMORY for the life of the tab once the user has proven it
 * this session — by setting up a backup or by restoring one. It enables auto-backup (below)
 * to keep the server copy current without prompting on every change.
 *
 * In memory only, never persisted: the device already holds all the plaintext this would
 * protect, so keeping the passphrase in a variable adds no exposure the server could reach —
 * but writing it to disk WOULD let a stolen device decrypt the server's copy, so we do not.
 * Cleared on logout with everything else. A reload therefore stops auto-backup until the next
 * manual backup or restore, which is the safe direction to fail.
 */
let sessionPassphrase: string | null = null

/**
 * How long to coalesce a burst of changes before re-sealing. A backup re-encrypts the whole
 * state and transcript, so it must not run per message; a minute of quiet is plenty fresh
 * while keeping the cost off the hot path.
 */
const AUTO_BACKUP_DEBOUNCE_MS = 60_000
let autoBackupTimer: ReturnType<typeof setTimeout> | null = null
let autoBackupUser = ''

/**
 * Schedules a background backup, if one is unlocked this session. A no-op when it is not — the
 * user has not set up or restored a backup here, so there is no passphrase to seal with, and
 * nothing to keep current. Coalesced: many calls in a burst result in one upload.
 */
export function autoBackupSoon(userId: string): void {
  if (!sessionPassphrase || !userId) return
  autoBackupUser = userId
  if (autoBackupTimer) return
  // The E2E suite shortens this so it does not have to idle a real minute; the app never
  // sets it, so production always uses the full debounce.
  const override = (globalThis as { __phemeAutoBackupMs?: number }).__phemeAutoBackupMs
  const delay = typeof override === 'number' ? override : AUTO_BACKUP_DEBOUNCE_MS
  autoBackupTimer = setTimeout(() => {
    autoBackupTimer = null
    const pass = sessionPassphrase
    if (!pass) return
    // Fire and forget: a failed auto-backup is not worth surfacing — the next change
    // schedules another, and the manual backup button is always there. The keys and
    // transcripts are safe locally regardless.
    void backupKeys(autoBackupUser, pass).catch(() => {})
  }, delay)
}

/**
 * Seals the current device state under `passphrase` and uploads it — together with the
 * decrypted transcript cache, sealed separately under the same passphrase.
 *
 * The transcripts are not an optimisation. Decryption is one-shot, so everything this
 * device has already read exists NOWHERE but its local cache: keys alone restore the
 * ability to talk, and only this restores what was said.
 */
export async function backupKeys(userId: string, passphrase: string): Promise<void> {
  const session = await mlsSession(userId)
  const state = await idbGet(STATE_KEY)
  if (!state) throw new Error('no local key state to back up')
  const pass = new TextEncoder().encode(passphrase)
  const blob = encryptBackup(pass, state)

  let transcript: { salt: string; nonce: string; ciphertext: string } | undefined
  try {
    const bodies = await exportAllContents()
    const sealed = encryptBackup(
      pass,
      new TextEncoder().encode(JSON.stringify({ v: 1, bodies })),
    )
    if (sealed.ciphertext.byteLength <= MAX_TRANSCRIPT_BACKUP_BYTES) {
      transcript = {
        salt: bytesToBase64(sealed.salt),
        nonce: bytesToBase64(sealed.nonce),
        ciphertext: bytesToBase64(sealed.ciphertext),
      }
    }
  } catch {
    // A transcript that cannot be gathered must not stop the keys being backed up.
  }

  await api.putKeyBackup(
    session.deviceId,
    bytesToBase64(blob.salt),
    bytesToBase64(blob.nonce),
    bytesToBase64(blob.ciphertext),
    transcript,
  )
  // The passphrase is proven and the backup is live: remember it for this session so
  // auto-backup can keep the server copy current as the conversation grows.
  sessionPassphrase = passphrase
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
    if (restoredDevice) saveMlsDeviceId(restoredDevice)

    // The transcripts, if the backup carried them: everything the old device had read,
    // straight into this one's cache. Best effort AND non-fatal — a backup from before
    // transcripts existed has none, and a blob that will not open must not undo the key
    // restore that already succeeded.
    if (backup.transcriptCiphertext && backup.transcriptSalt && backup.transcriptNonce) {
      try {
        const opened = decryptBackup(
          new TextEncoder().encode(passphrase),
          base64ToBytes(backup.transcriptSalt),
          base64ToBytes(backup.transcriptNonce),
          base64ToBytes(backup.transcriptCiphertext),
        )
        const parsed: unknown = JSON.parse(new TextDecoder().decode(opened))
        if (typeof parsed === 'object' && parsed !== null && 'bodies' in parsed) {
          await importContents((parsed as { bodies: Record<string, Record<string, string>> }).bodies)
        }
      } catch {
        // The keys are restored and the conversations work; only the old scrollback is
        // missing. Saying nothing here beats failing a restore that succeeded.
      }
    }

    restoreNeeded = false
    ready = null
    readyUserId = ''
    settling.clear()
    // Proven this session: keep it so auto-backup carries forward anything read from here
    // on, and the restored device's backup stays as current as the old one's was.
    sessionPassphrase = passphrase
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
