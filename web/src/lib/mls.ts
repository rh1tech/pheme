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

// How many times an external join is re-attempted against fresher GroupInfo after a conflict. A
// conflict means a Commit landed between fetching GroupInfo and offering our external commit; a few
// retries absorb ordinary concurrency, and beyond that we fall back to announce-and-wait.
const EXTERNAL_JOIN_ATTEMPTS = 4

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
/**
 * "I just joined and hold none of this conversation's past — can a device that has it send it?"
 *
 * Posted by a freshly-joined device with no local transcript. A co-member that holds the history
 * seals it and answers with MLS_HISTORY_OFFER. Carries the requester's identity and epoch, never
 * key material or content.
 */
export const MLS_HISTORY_REQUEST = 'application/mls-history-request'
/**
 * The answer to a history request: "your history is sealed and waiting at this id."
 *
 * The transcript never rides the message — it is sealed under a key DERIVED FROM THE GROUP (which
 * the server cannot derive) and stored as a blob; this points at it, with the salt and nonce and the
 * epoch the key was derived at, addressed to the one requester it is for.
 */
export const MLS_HISTORY_OFFER = 'application/mls-history-offer'

/** The control types, which carry no user-visible text. */
export const MLS_CONTROL_TYPES: ReadonlySet<string> = new Set([
  MLS_WELCOME,
  MLS_COMMIT,
  MLS_DEVICE,
  MLS_HISTORY_REQUEST,
  MLS_HISTORY_OFFER,
])

/** The exporter label for the history-sync key. Versioned, since changing it changes the key. */
const HISTORY_SYNC_LABEL = 'pheme/history-sync/v1'

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

  /**
   * The self-contained GroupInfo a non-member needs to join this group by external commit. A pure
   * read — no ratchet mutation, nothing to persist.
   */
  async exportGroupInfo(groupId: string): Promise<Uint8Array> {
    return this.exclusive(async () => this.client.exportGroupInfo(groupBytes(groupId)))
  }

  /**
   * Joins an existing group by EXTERNAL COMMIT — adds this device's own leaf from a member's
   * published GroupInfo, with no Welcome and nobody online to admit it. `baseEpoch` is the epoch the
   * GroupInfo was exported at; the external commit produces `baseEpoch + 1`, offered through the same
   * compare-and-set as any commit.
   *
   * The external commit is PENDING and, unlike a staged commit, CANNOT be cleared — so a refusal is
   * handled by deleting the whole group (not commitRejected) and letting the caller retry from fresh
   * GroupInfo.
   */
  async joinByExternalCommit(
    conversationId: string,
    groupId: string,
    groupInfo: Uint8Array,
    baseEpoch: number,
  ): Promise<'accepted' | 'conflict'> {
    return this.exclusive(async () => {
      const bytes = groupBytes(groupId)
      // A concurrent settle (or a Welcome that just landed) already put us in. Nothing to do.
      if (this.client.hasGroup(bytes)) return 'accepted'

      const commit = this.client.joinByExternalCommit(groupInfo)
      // The pending external commit lives in the client state; persist it, or a reload would leave
      // a group half-joined against a commit the server may have accepted.
      await this.persist()

      try {
        await api.mlsCommit(conversationId, { groupId, baseEpoch, commit: bytesToBase64(commit) })
      } catch (e) {
        // Refused or unreachable. An external commit cannot be rolled back like a staged one — the
        // only way out is to discard the group we just created and start over from fresh GroupInfo.
        this.client.deleteGroup(bytes)
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
    // was evicted, a member added while we were offline.
    //
    // FIRST, add our own leaf by EXTERNAL COMMIT against a member's published GroupInfo — no
    // Welcome, nobody online to admit us, one round trip. This is what turns "log in on a new
    // device / the web at any time" from a wait into an instant join.
    const joined = await tryExternalJoin(session, conversation.id, state.groupId)
    if (joined) {
      clearStuck(conversation.id)
      // We just joined and hold none of the past — ask a co-member for it. No-op if we have it.
      void requestHistory(conversation.id, myUserId)
      return joined
    }

    // No GroupInfo to join against (no member has published one, or it is for a group since
    // retired). Fall back to announcing: post a keyless request to be added, and a member who
    // holds the group admits us the next time one is online.
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

  // Keep GroupInfo fresh at whatever epoch we ended on (reconcile may have added a device), so the
  // NEXT new device can external-join immediately instead of waiting to be admitted. Fire-and-forget.
  void publishGroupInfo(session, conversation.id, state.groupId)
  // If we hold the group but none of its history (a device admitted by Welcome, say), ask for it.
  void requestHistory(conversation.id, myUserId)
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
  // Publish GroupInfo for the group we just established, so a member who was offline when we
  // built it can external-join the moment they open the chat, without us admitting them.
  void publishGroupInfo(session, conversation.id, groupId)
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
 * Adds this device's own leaf to an existing group by EXTERNAL COMMIT, from a member's published
 * GroupInfo — no Welcome, nobody online to admit it. Returns the group id on success, or null when
 * external join is not possible (no GroupInfo published, or it is for a group since retired), in
 * which case the caller falls back to announce-and-wait.
 *
 * This is what makes opening a conversation on a new device — or a web login at any time — instant
 * instead of a wait for a member to come online and admit it.
 */
async function tryExternalJoin(
  session: Session,
  conversationId: string,
  groupId: string,
): Promise<string | null> {
  for (let attempt = 0; attempt < EXTERNAL_JOIN_ATTEMPTS; attempt++) {
    const info = await api.mlsGroupInfo(conversationId).catch(() => null)
    // No GroupInfo, or GroupInfo for a group that has since been replaced — external join cannot
    // help; let the caller announce and wait.
    if (!info || info.groupId !== groupId) return null

    const result = await session.joinByExternalCommit(
      conversationId,
      groupId,
      base64ToBytes(info.groupInfo),
      info.epoch,
    )
    if (result === 'accepted') {
      rememberReadableGroup(conversationId, groupId)
      // Republish at the new epoch so the NEXT joiner can external-join without waiting either.
      void publishGroupInfo(session, conversationId, groupId)
      return groupId
    }
    // 'conflict': a Commit landed between the fetch and the offer. Loop to fetch fresher GroupInfo
    // and try again at the new epoch.
  }
  // Exhausted the retries against a moving group. Fall back rather than hammer the commit path.
  return null
}

/**
 * Publishes this device's current GroupInfo, so a NEW device can external-join at the current epoch
 * instead of waiting to be admitted. Fire-and-forget: a joiner that finds none simply falls back to
 * announce-and-wait, so a failure here costs a little latency for the next device, never correctness.
 */
async function publishGroupInfo(
  session: Session,
  conversationId: string,
  groupId: string,
): Promise<void> {
  try {
    const info = await session.exportGroupInfo(groupId)
    const epoch = await session.epoch(groupId)
    await api.publishGroupInfo(conversationId, {
      groupId,
      epoch,
      groupInfo: bytesToBase64(info),
    })
  } catch {
    // Best effort.
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
  conversationId: string,
  myUserId: string,
): Promise<void> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId || !(await session.hasGroup(state.groupId))) return
  await reconcileDevices(session, conversationId, state.groupId)
}

// --- device-to-device history sync ----------------------------------------
//
// A device that joins an existing conversation holds none of what was said before it arrived —
// MLS gives a new leaf no access to the past. Rather than leave that history to a backup alone, a
// co-member that DOES hold it can hand it over directly: sealed under a key both derive from the
// group (which the server cannot derive), stored as a one-shot blob, pointed at by a control
// message. The server only ever sees ciphertext.
//
// The key is the group's exporter secret at a specific epoch, bound to the requester's identity, so
// a blob offered to one device at one epoch cannot be replayed to another or at another epoch.

interface HistoryRequestBody {
  id: string // the requester's leaf identity
  epoch: number
}
interface HistoryOfferBody {
  to: string // the requester this offer is for
  epoch: number // the epoch the key was derived at
  historyId: string
  salt: string
  nonce: string
}

function encodeControl(obj: unknown): string {
  return bytesToBase64(new TextEncoder().encode(JSON.stringify(obj)))
}
function decodeControl<T>(ciphertextBase64: string): T | null {
  try {
    return JSON.parse(new TextDecoder().decode(base64ToBytes(ciphertextBase64))) as T
  } catch {
    return null
  }
}

/** Conversations this device has already asked history for this session — so it asks once, not on every settle. */
const historyRequested = new Set<string>()

/**
 * Asks co-members for this conversation's pre-join history — once per conversation per session.
 * A no-op unless this device holds the group but has no local transcript for it (i.e. it just
 * joined and has nothing to show). The request carries only this device's identity and epoch.
 */
export async function requestHistory(conversationId: string, myUserId: string): Promise<void> {
  if (historyRequested.has(conversationId)) return
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId || !(await session.hasGroup(state.groupId))) return
  // Nothing to fetch if we already hold this conversation's history.
  const existing = (await exportAllContents())[conversationId]
  if (existing && Object.keys(existing).length > 0) return
  historyRequested.add(conversationId)
  const epoch = await session.epoch(state.groupId)
  const body: HistoryRequestBody = { id: session.identity, epoch }
  await api
    .sendChatMessage(conversationId, encodeControl(body), MLS_HISTORY_REQUEST)
    .catch(() => historyRequested.delete(conversationId))
}

/**
 * Responds to a history request: seal this conversation's transcript under a group-derived key bound
 * to the requester, upload it, and point the requester at it. No-op if we hold nothing, or the
 * request is our own. Callers elect a single responder before calling this (see useHistorySync).
 */
export async function offerHistory(
  conversationId: string,
  myUserId: string,
  requesterIdentity: string,
): Promise<void> {
  const session = await mlsSession(myUserId)
  if (requesterIdentity === session.identity) return
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId || !(await session.hasGroup(state.groupId))) return
  const bodies = (await exportAllContents())[conversationId]
  if (!bodies || Object.keys(bodies).length === 0) return

  const derived = await session.exportSecret(
    state.groupId,
    HISTORY_SYNC_LABEL,
    new TextEncoder().encode(requesterIdentity),
    32,
  )
  if (!derived) return
  const sealed = encryptBackup(
    derived.secret,
    new TextEncoder().encode(JSON.stringify({ v: 1, bodies })),
  )
  const historyId = await api.uploadHistory(conversationId, sealed.ciphertext)
  const offer: HistoryOfferBody = {
    to: requesterIdentity,
    epoch: derived.epoch,
    historyId,
    salt: bytesToBase64(sealed.salt),
    nonce: bytesToBase64(sealed.nonce),
  }
  await api.sendChatMessage(conversationId, encodeControl(offer), MLS_HISTORY_OFFER).catch(() => {})
}

/**
 * Receives an offer addressed to this device: derive the same group key, fetch the blob, open it,
 * and import the history into the local cache. Returns whether it imported anything.
 *
 * The epoch must match: the exporter secret is per-epoch, so a drift between the offer and now means
 * the keys differ — we bail and let a re-request settle it.
 */
export async function receiveHistoryOffer(
  conversationId: string,
  myUserId: string,
  offerCiphertext: string,
): Promise<boolean> {
  const session = await mlsSession(myUserId)
  const offer = decodeControl<HistoryOfferBody>(offerCiphertext)
  if (!offer || offer.to !== session.identity) return false
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId || !(await session.hasGroup(state.groupId))) return false

  const derived = await session.exportSecret(
    state.groupId,
    HISTORY_SYNC_LABEL,
    new TextEncoder().encode(session.identity),
    32,
  )
  if (!derived || derived.epoch !== offer.epoch) return false

  let plaintext: Uint8Array
  try {
    const blob = await api.getHistory(conversationId, offer.historyId)
    plaintext = decryptBackup(derived.secret, base64ToBytes(offer.salt), base64ToBytes(offer.nonce), blob)
  } catch {
    return false
  }
  try {
    const parsed = JSON.parse(new TextDecoder().decode(plaintext)) as {
      bodies?: Record<string, string>
    }
    if (parsed.bodies && Object.keys(parsed.bodies).length > 0) {
      await importContents({ [conversationId]: parsed.bodies })
      return true
    }
  } catch {
    return false
  }
  return false
}

/** The leaf identities of a conversation's current group, for the responder election. Empty if we do not hold it. */
export async function groupMemberIdentities(conversationId: string, myUserId: string): Promise<string[]> {
  const session = await mlsSession(myUserId)
  const state = await api.mlsGroupState(conversationId)
  if (!state.groupId || !(await session.hasGroup(state.groupId))) return []
  return session.memberIdentities(state.groupId)
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
 * Whether the user has already chosen to start fresh on this device. Once they have, the restore
 * prompt must not reappear on the next reload — the device is minting its own identity, and nagging
 * to restore a backup it deliberately declined is a loop.
 */
export async function hasAcceptedFresh(): Promise<boolean> {
  return (await idbGet(FRESH_KEY)) != null
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
  historyRequested.clear()
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

// --- recovery code --------------------------------------------------------
//
// A recovery code is just a high-entropy passphrase the APP generates, so the user never has to
// invent or type one to be protected. It is fed to the same Argon2id-backed encryptBackup as a
// passphrase — encryptBackup takes opaque bytes, so a code and a passphrase are interchangeable.
//
// It is shown ONCE at setup and stored locally (never server-side) so the same device can re-show
// it; a new device needs the user to have written it down. Losing it loses the recoverable history
// — the same trade a forgotten passphrase carries.

/** Where the plaintext recovery code lives locally, for re-display. Never sent to the server. */
const RECOVERY_CODE_KEY = 'recovery-code'

// Crockford base32 — no I, L, O, U, so nothing is ambiguous to read back or type.
const CROCKFORD = '0123456789ABCDEFGHJKMNPQRSTVWXYZ'

/**
 * A fresh recovery code: 25 Crockford-base32 chars (125 bits) in five dash-separated groups, e.g.
 * `K7Q2M-9XPTW-3RJ4H-N8BVD-2FZ6Y`. High enough entropy that the Argon2id KDF is belt-and-braces.
 */
export function generateRecoveryCode(): string {
  const bytes = new Uint8Array(25)
  crypto.getRandomValues(bytes)
  const chars = Array.from(bytes, (b) => CROCKFORD[b % 32])
  const groups: string[] = []
  for (let i = 0; i < chars.length; i += 5) groups.push(chars.slice(i, i + 5).join(''))
  return groups.join('-')
}

/**
 * Normalises a code the user typed, so it can be entered loosely: uppercased, spaces/dashes
 * stripped, and Crockford's read-alike letters folded (I/L→1, O→0). The result is what was sealed
 * with, so a code entered `k7q2m 9xptw…` opens a backup made from `K7Q2M-9XPTW-…`.
 */
export function normalizeRecoveryCode(input: string): string {
  return input
    .toUpperCase()
    .replace(/[IL]/g, '1')
    .replace(/O/g, '0')
    .replace(/[^0-9A-Z]/g, '')
}

/** Re-reads the recovery code stored on this device, or null if none (e.g. a restored device). */
export async function loadRecoveryCode(): Promise<string | null> {
  const bytes = await idbGet(RECOVERY_CODE_KEY)
  return bytes ? new TextDecoder().decode(bytes) : null
}

/** Records the recovery code locally so the user can view it again on THIS device. */
async function saveRecoveryCode(code: string): Promise<void> {
  await idbSet(RECOVERY_CODE_KEY, new TextEncoder().encode(code))
}

/**
 * The default backup path: if this device holds keys but the user has no server backup yet, generate
 * a recovery code, seal a backup under it, and hand the code back to be shown ONCE. Returns null when
 * a backup already exists (nothing to set up) — so it is safe to call on every chat-surface open.
 *
 * This is what makes "your history follows you" true without the user ever choosing a passphrase.
 */
export async function ensureRecoveryBackup(userId: string): Promise<string | null> {
  try {
    // Make sure this device HAS an identity to back up. mlsSession mints or loads it; it throws
    // when a restore is required first (a fresh device with a backup waiting), in which case
    // auto-setup is not our job — the restore gate is.
    await mlsSession(userId)
  } catch {
    return null
  }
  if (await backupExists()) {
    // Already set up. But sessionPassphrase is in-memory and a reload (or PWA relaunch) clears it,
    // which would silently stop auto-backup. If THIS device holds the recovery code, re-unlock
    // auto-backup with it so the server copy keeps up across reloads. Nothing new to show.
    const localCode = await loadRecoveryCode()
    if (localCode) {
      sessionPassphrase = normalizeRecoveryCode(localCode)
      autoBackupSoon(userId)
    }
    return null
  }
  return sealUnderNewCode(userId)
}

/**
 * Re-seals the backup under a NEW recovery code and returns it — for "regenerate", when a user
 * believes their old code is compromised or lost. The old code stops working immediately.
 */
export async function regenerateRecoveryCode(userId: string): Promise<string> {
  return sealUnderNewCode(userId)
}

/**
 * Generates a code, seals the backup under its CANONICAL form, stores the pretty form for display.
 *
 * The seal uses the normalized code so a user who later types it loosely (lowercase, no dashes) still
 * opens it — see `restoreWithSecret`. The pretty, dash-grouped form is what we show and store.
 */
async function sealUnderNewCode(userId: string): Promise<string> {
  const pretty = generateRecoveryCode()
  await backupKeys(userId, normalizeRecoveryCode(pretty)) // seals state+transcript; sets sessionPassphrase
  await saveRecoveryCode(pretty)
  // The initial seal above races message decryption: messages read BEFORE the session unlocked here
  // called autoBackupSoon while it was still a no-op, so they are not in that seal. Schedule a
  // catch-up now that the passphrase is set — it re-seals with whatever has been decrypted by the
  // time it fires, and every later decrypt arms it normally.
  autoBackupSoon(userId)
  return pretty
}

/**
 * Restores from whatever the user typed, whether it is a recovery CODE or a legacy PASSPHRASE.
 *
 * A code was sealed in its normalized form; a passphrase was sealed verbatim. We cannot tell which
 * the user has, so try the input as-is first (opens a passphrase backup), and on the "wrong secret"
 * failure try its normalized form (opens a code backup, typed loosely). restoreKeys validates before
 * any side effect, so the first attempt failing leaves nothing to undo.
 */
export async function restoreWithSecret(userId: string, input: string): Promise<boolean> {
  try {
    return await restoreKeys(userId, input)
  } catch (e) {
    if (e instanceof IdentityAlreadySetUpError || e instanceof NeedsRestoreError) throw e
    const normalized = normalizeRecoveryCode(input)
    if (normalized === input) throw e // nothing new to try
    return restoreKeys(userId, normalized)
  }
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
 * Recovers a user's HISTORY onto this device from the server backup, under `passphrase`.
 * Returns false when there is no backup; throws on a wrong passphrase (the GCM tag fails).
 *
 * ----------------------------------------------------------------------------------------
 * IT DOES NOT ADOPT THE BACKED-UP DEVICE'S IDENTITY. That was the old behaviour, and it was
 * a trap: it made the restoring device a CLONE of the one that took the backup — the same
 * MLS leaf, the same keys. If that other device was still in use (a second phone, a laptop
 * kept open), the two shared one identity, and to MLS a message from either looked to the
 * OTHER like its own — which a sender can never decrypt. Both devices showed "not available
 * on this device" for everything the other said, and their ratchets forked on the next
 * Commit. Two devices of one person are meant to be two independent leaves; that is the
 * whole design (see the header of this file), and cloning breaks it.
 *
 * So a restore recovers the WORDS, not the leaf: the passphrase is proven, the transcript is
 * imported, and this device then comes up under its OWN fresh identity and is admitted to
 * each group as a new member — exactly as any new device is. It reads history from the
 * imported cache, and new messages as its own leaf. The backed-up key state is used ONLY to
 * check the passphrase, never to become this device.
 * ----------------------------------------------------------------------------------------
 */
export async function restoreKeys(userId: string, passphrase: string): Promise<boolean> {
  await ensureWasm()
  const backup = await api.getKeyBackup(true)
  if (!backup) return false

  // Prove the passphrase by opening the state blob (throws on a wrong one) — then discard
  // it. Its only job here is to validate, and to confirm the bytes really are a client state.
  const state = decryptBackup(
    new TextEncoder().encode(passphrase),
    base64ToBytes(backup.salt),
    base64ToBytes(backup.nonce),
    base64ToBytes(backup.ciphertext),
  )
  MlsClient.fromState(state)

  // Open the transcript too, before committing anything, so a bad blob fails cleanly rather
  // than half-way. Its own seal under the same passphrase.
  let bodies: Record<string, Record<string, string>> | null = null
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
        bodies = (parsed as { bodies: Record<string, Record<string, string>> }).bodies
      }
    } catch {
      // A history that will not open must not fail the restore — the device still comes up
      // working, just without the old scrollback.
    }
  }

  // Come up as a FRESH device, not the backed-up one. acceptFreshIdentity is what tells the
  // bootstrap to mint a new identity rather than demand a restore, and it shuts the restore
  // gate for good.
  await acceptFreshIdentity()
  ready = null
  readyUserId = ''
  settling.clear()

  // Mint that fresh identity now — before importing history, because bootstrapping wipes the
  // store to a clean slate for the new identity, and history written first would go with it.
  await mlsSession(userId)

  // Now the history, on top of the fresh identity. Imported after the wipe, so it survives.
  if (bodies) {
    try {
      await importContents(bodies)
    } catch {
      // Best effort; the device is already up and working without it.
    }
  }

  // Proven this session: keep it so auto-backup keeps this device's own backup current, and
  // schedule a catch-up so the imported history (and anything decrypted during restore) is re-sealed
  // under this device's own backup.
  sessionPassphrase = passphrase
  autoBackupSoon(userId)
  return true
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
