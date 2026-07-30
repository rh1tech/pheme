/* tslint:disable */
/* eslint-disable */

/**
 * The Welcome + Commit produced by adding a member, returned to JS as an object.
 */
export class AddOutput {
    private constructor();
    free(): void;
    [Symbol.dispose](): void;
    commit: Uint8Array;
    welcome: Uint8Array;
}

/**
 * A sealed key backup: salt, nonce and ciphertext, all stored server-side.
 */
export class BackupBlob {
    private constructor();
    free(): void;
    [Symbol.dispose](): void;
    ciphertext: Uint8Array;
    nonce: Uint8Array;
    salt: Uint8Array;
}

/**
 * A decrypted application message with the sender **MLS authenticated**.
 *
 * `sender` is the credential identity of the leaf that signed the message —
 * `mimi://<domain>/d/<user>/<device>` — verified against the group's own ratchet tree during
 * decryption. It is the only trustworthy answer to "who wrote this": the `senderId` on the message
 * envelope is written by the server, which relays these bytes and can put any name it likes there.
 *
 * `epoch` is the epoch the message was framed in, so a caller can say which membership the sender
 * was authenticated against.
 */
export class DecryptedMessage {
    private constructor();
    free(): void;
    [Symbol.dispose](): void;
    epoch: bigint;
    plaintext: Uint8Array;
    sender: string;
}

export class MlsClient {
    free(): void;
    [Symbol.dispose](): void;
    applyCommit(group_id: Uint8Array, commit: Uint8Array): void;
    /**
     * Applies the Commit we staged, now that the server has accepted it.
     */
    commitAccepted(group_id: Uint8Array): void;
    /**
     * Throws away a Commit the server refused, leaving the group untouched so we can
     * catch up on the winning Commit and try again.
     */
    commitRejected(group_id: Uint8Array): void;
    createGroup(group_id: Uint8Array): void;
    /**
     * Decrypts an application message; returns undefined for a control message.
     *
     * The result carries the sender MLS itself authenticated — see `DecryptedMessage`. The old
     * signature returned bare bytes, which left the app with nothing to attribute a message by
     * except the `senderId` the untrusted server wrote on the envelope.
     */
    decrypt(group_id: Uint8Array, ciphertext: Uint8Array): DecryptedMessage | undefined;
    /**
     * Discards a group so it can be rebuilt (repairing a member who could never join).
     */
    deleteGroup(group_id: Uint8Array): void;
    encrypt(group_id: Uint8Array, plaintext: Uint8Array): Uint8Array;
    /**
     * The group's current epoch — what a Commit is proposed against.
     */
    epoch(group_id: Uint8Array): bigint;
    /**
     * The self-contained GroupInfo a NON-MEMBER needs to join this group by external commit.
     * A pure read; nothing is persisted.
     */
    exportGroupInfo(group_id: Uint8Array): Uint8Array;
    /**
     * Derives a secret from the group for a purpose outside MLS's own messaging — Pheme
     * uses it to key voice-call signalling, so the server cannot read the SDP and
     * therefore cannot swap the DTLS fingerprint inside it.
     *
     * A pure read: it mutates neither the group nor the stored state, so unlike an MLS
     * application message it can be called freely without churning the ratchet or the
     * key store. It exports from the CURRENT EPOCH — see the crate docs.
     */
    exportSecret(group_id: Uint8Array, label: string, context: Uint8Array, length: number): Uint8Array;
    /**
     * The full client state to persist (IndexedDB).
     */
    exportState(): Uint8Array;
    /**
     * Restores a client from a previously exported state blob.
     */
    static fromState(state: Uint8Array): MlsClient;
    /**
     * Whether this client is already in the group (non-mutating).
     */
    hasGroup(group_id: Uint8Array): boolean;
    /**
     * This client's own long-term signature public key.
     */
    identityKey(): Uint8Array;
    /**
     * Joins an existing group by external commit, from a member's GroupInfo — adds this client's
     * own leaf with no Welcome and no member online. Returns the commit to offer the server through
     * the ordinary compare-and-set. The commit is left PENDING and, unlike a staged commit, cannot
     * be cleared: on acceptance call `commitAccepted`, on refusal call `deleteGroup` and retry from
     * fresh GroupInfo.
     */
    joinByExternalCommit(group_info: Uint8Array): Uint8Array;
    joinFromWelcome(welcome: Uint8Array): void;
    /**
     * A single-use public KeyPackage to publish to the server.
     */
    keyPackage(): Uint8Array;
    /**
     * A reusable last-resort KeyPackage, so the user can always be added to a group
     * even after their single-use stock has been claimed.
     */
    lastResortKeyPackage(): Uint8Array;
    /**
     * Every leaf's `userId:deviceId`, so the caller can spot member devices that are
     * missing from the group and add exactly those.
     */
    memberIdentities(group_id: Uint8Array): Array<any>;
    /**
     * Creates a fresh identity for one DEVICE of one user, on a given home host.
     * All three are required: the domain qualifies the member so that a person on
     * one host is distinct from a same-named person on another (the credential is
     * `mimi://<domain>/d/<user>/<device>`), and the device id is what keeps a
     * user's leaves apart.
     */
    constructor(domain: string, user_id: string, device_id: string);
    /**
     * The safety number for a group: the digits two people compare, out of band, to
     * prove the server did not substitute a key and put itself in the middle.
     * Derived from the group's own ratchet tree, not from anything the server says.
     */
    safetyNumber(group_id: Uint8Array): string;
    /**
     * Signs an offer of a sealed transcript. The digest of `ciphertext` goes into the
     * signature, so the server cannot swap the blob behind it.
     */
    signHistoryOffer(group_id: Uint8Array, conversation_id: string, epoch: bigint, requester: string, history_id: string, salt: Uint8Array, nonce: Uint8Array, request_nonce: Uint8Array, ciphertext: Uint8Array): Uint8Array;
    /**
     * Signs this device's request for a conversation's pre-join history.
     */
    signHistoryRequest(group_id: Uint8Array, conversation_id: string, epoch: bigint, nonce: Uint8Array): Uint8Array;
    /**
     * STAGES the addition of several devices in one Commit (all newcomers land at the
     * same epoch). `key_packages` is a JS array of Uint8Array; one Welcome covers all.
     *
     * The Commit is NOT applied. Call `commitAccepted` once the server has taken it as
     * the group's next epoch, or `commitRejected` if another member's Commit landed
     * first. Applying it before the server agrees is what forks a client off the group
     * for good.
     */
    stageAdd(group_id: Uint8Array, key_packages: Array<any>): AddOutput;
    /**
     * STAGES the removal of the exact leaves named by `identities` (`userId:deviceId`).
     *
     * For pruning a ghost device — one whose key material no longer exists anywhere —
     * while leaving that person's live devices alone. Removing by USER would take their
     * working phone out along with the ghost.
     */
    stageRemoveDevices(group_id: Uint8Array, identities: Array<any>): Uint8Array;
    /**
     * STAGES the removal of every device belonging to each of `user_ids` (a JS array of
     * strings). Not applied until `commitAccepted`. Removing only one leaf would leave
     * the removed member reading the group from their other device.
     *
     * This client's own leaves are never removed: MLS forbids committing your own
     * removal, so leaving is not a Commit — see the crate docs.
     */
    stageRemoveUsers(group_id: Uint8Array, user_ids: Array<any>): Uint8Array;
    /**
     * Verifies an offer against the claimed offerer's leaf key and the blob's own bytes. The
     * requester is THIS client, so an offer addressed to another device never verifies here.
     */
    verifyHistoryOffer(group_id: Uint8Array, conversation_id: string, epoch: bigint, offerer: string, history_id: string, salt: Uint8Array, nonce: Uint8Array, request_nonce: Uint8Array, ciphertext: Uint8Array, signature: Uint8Array): void;
    /**
     * Verifies a request against the claimed requester's leaf key in the group's ratchet tree.
     * Throws when the identity holds no leaf, or the signature is not theirs.
     */
    verifyHistoryRequest(group_id: Uint8Array, conversation_id: string, epoch: bigint, requester: string, nonce: Uint8Array, signature: Uint8Array): void;
    /**
     * This client's credential identity, `userId:deviceId`.
     *
     * It is the authoritative answer to "which device am I?". A restored backup carries
     * the identity of the device it was taken FROM, and the groups in that state hold
     * leaves under that name — so the browser it is restored into has to answer to it,
     * whatever its own local storage happens to say.
     */
    readonly identity: string;
}

/**
 * A read-only client for rendering notification previews in a service worker.
 *
 * Separate from `MlsClient` on purpose, and this is the whole reason it exists: a service
 * worker is a SECOND context holding the same key store as the page, and the single-client
 * rule says there must never be two writers. This one has no `exportState`, so there is
 * nowhere for an advanced ratchet to go — the worker physically cannot persist, however a
 * later edit is written. See `crate::PreviewClient`.
 *
 * The page's own copy of the state is untouched and decrypts the message again for real when
 * the app opens, so nothing is lost by previewing it here.
 *
 * Drop it as soon as the notification is shown: it holds plaintext.
 */
export class MlsPreviewClient {
    private constructor();
    free(): void;
    [Symbol.dispose](): void;
    /**
     * Decrypts one application message for display. `undefined` means there was nothing to
     * preview — control traffic, or a message this client cannot read.
     *
     * The result carries the authenticated sender, so a notification can be titled by the leaf
     * that signed the message rather than by whatever name the push payload asserts.
     */
    decrypt(group_id: Uint8Array, ciphertext: Uint8Array): DecryptedMessage | undefined;
    /**
     * Loads a read-only client from a state blob read out of IndexedDB.
     */
    static fromState(state: Uint8Array): MlsPreviewClient;
    /**
     * Whether this client holds the group, so the caller can pick the right one without
     * attempting a decrypt against each in turn.
     */
    hasGroup(group_id: Uint8Array): boolean;
}

/**
 * Recovers client state from a sealed backup. Errors on a wrong passphrase.
 */
export function decryptBackup(passphrase: Uint8Array, salt: Uint8Array, nonce: Uint8Array, ciphertext: Uint8Array): Uint8Array;

/**
 * Seals exported client state under a recovery passphrase (Argon2id + AES-256-GCM).
 */
export function encryptBackup(passphrase: Uint8Array, plaintext: Uint8Array): BackupBlob;

export type InitInput = RequestInfo | URL | Response | BufferSource | WebAssembly.Module;

export interface InitOutput {
    readonly memory: WebAssembly.Memory;
    readonly __wbg_addoutput_free: (a: number, b: number) => void;
    readonly __wbg_backupblob_free: (a: number, b: number) => void;
    readonly __wbg_decryptedmessage_free: (a: number, b: number) => void;
    readonly __wbg_get_addoutput_commit: (a: number) => [number, number];
    readonly __wbg_get_addoutput_welcome: (a: number) => [number, number];
    readonly __wbg_get_backupblob_ciphertext: (a: number) => [number, number];
    readonly __wbg_get_decryptedmessage_epoch: (a: number) => bigint;
    readonly __wbg_get_decryptedmessage_sender: (a: number) => [number, number];
    readonly __wbg_mlsclient_free: (a: number, b: number) => void;
    readonly __wbg_set_addoutput_commit: (a: number, b: number, c: number) => void;
    readonly __wbg_set_addoutput_welcome: (a: number, b: number, c: number) => void;
    readonly __wbg_set_backupblob_ciphertext: (a: number, b: number, c: number) => void;
    readonly __wbg_set_decryptedmessage_epoch: (a: number, b: bigint) => void;
    readonly __wbg_set_decryptedmessage_plaintext: (a: number, b: number, c: number) => void;
    readonly decryptBackup: (a: number, b: number, c: number, d: number, e: number, f: number, g: number, h: number) => [number, number, number, number];
    readonly encryptBackup: (a: number, b: number, c: number, d: number) => [number, number, number];
    readonly mlsclient_applyCommit: (a: number, b: number, c: number, d: number, e: number) => [number, number];
    readonly mlsclient_commitAccepted: (a: number, b: number, c: number) => [number, number];
    readonly mlsclient_commitRejected: (a: number, b: number, c: number) => [number, number];
    readonly mlsclient_createGroup: (a: number, b: number, c: number) => [number, number];
    readonly mlsclient_decrypt: (a: number, b: number, c: number, d: number, e: number) => [number, number, number];
    readonly mlsclient_deleteGroup: (a: number, b: number, c: number) => [number, number];
    readonly mlsclient_encrypt: (a: number, b: number, c: number, d: number, e: number) => [number, number, number, number];
    readonly mlsclient_epoch: (a: number, b: number, c: number) => [bigint, number, number];
    readonly mlsclient_exportGroupInfo: (a: number, b: number, c: number) => [number, number, number, number];
    readonly mlsclient_exportSecret: (a: number, b: number, c: number, d: number, e: number, f: number, g: number, h: number) => [number, number, number, number];
    readonly mlsclient_exportState: (a: number) => [number, number, number, number];
    readonly mlsclient_fromState: (a: number, b: number) => [number, number, number];
    readonly mlsclient_hasGroup: (a: number, b: number, c: number) => number;
    readonly mlsclient_identity: (a: number) => [number, number];
    readonly mlsclient_identityKey: (a: number) => [number, number];
    readonly mlsclient_joinByExternalCommit: (a: number, b: number, c: number) => [number, number, number, number];
    readonly mlsclient_joinFromWelcome: (a: number, b: number, c: number) => [number, number];
    readonly mlsclient_keyPackage: (a: number) => [number, number, number, number];
    readonly mlsclient_lastResortKeyPackage: (a: number) => [number, number, number, number];
    readonly mlsclient_memberIdentities: (a: number, b: number, c: number) => [number, number, number];
    readonly mlsclient_new: (a: number, b: number, c: number, d: number, e: number, f: number) => [number, number, number];
    readonly mlsclient_safetyNumber: (a: number, b: number, c: number) => [number, number, number, number];
    readonly mlsclient_signHistoryOffer: (a: number, b: number, c: number, d: number, e: number, f: bigint, g: number, h: number, i: number, j: number, k: number, l: number, m: number, n: number, o: number, p: number, q: number, r: number) => [number, number, number, number];
    readonly mlsclient_signHistoryRequest: (a: number, b: number, c: number, d: number, e: number, f: bigint, g: number, h: number) => [number, number, number, number];
    readonly mlsclient_stageAdd: (a: number, b: number, c: number, d: any) => [number, number, number];
    readonly mlsclient_stageRemoveDevices: (a: number, b: number, c: number, d: any) => [number, number, number, number];
    readonly mlsclient_stageRemoveUsers: (a: number, b: number, c: number, d: any) => [number, number, number, number];
    readonly mlsclient_verifyHistoryOffer: (a: number, b: number, c: number, d: number, e: number, f: bigint, g: number, h: number, i: number, j: number, k: number, l: number, m: number, n: number, o: number, p: number, q: number, r: number, s: number, t: number) => [number, number];
    readonly mlsclient_verifyHistoryRequest: (a: number, b: number, c: number, d: number, e: number, f: bigint, g: number, h: number, i: number, j: number, k: number, l: number) => [number, number];
    readonly mlspreviewclient_decrypt: (a: number, b: number, c: number, d: number, e: number) => [number, number, number];
    readonly mlspreviewclient_fromState: (a: number, b: number) => [number, number, number];
    readonly __wbg_get_backupblob_nonce: (a: number) => [number, number];
    readonly __wbg_get_backupblob_salt: (a: number) => [number, number];
    readonly __wbg_get_decryptedmessage_plaintext: (a: number) => [number, number];
    readonly __wbg_set_backupblob_nonce: (a: number, b: number, c: number) => void;
    readonly __wbg_set_backupblob_salt: (a: number, b: number, c: number) => void;
    readonly __wbg_set_decryptedmessage_sender: (a: number, b: number, c: number) => void;
    readonly __wbg_mlspreviewclient_free: (a: number, b: number) => void;
    readonly mlspreviewclient_hasGroup: (a: number, b: number, c: number) => number;
    readonly __wbindgen_malloc: (a: number, b: number) => number;
    readonly __wbindgen_realloc: (a: number, b: number, c: number, d: number) => number;
    readonly __wbindgen_exn_store: (a: number) => void;
    readonly __externref_table_alloc: () => number;
    readonly __wbindgen_externrefs: WebAssembly.Table;
    readonly __externref_table_dealloc: (a: number) => void;
    readonly __wbindgen_free: (a: number, b: number, c: number) => void;
    readonly __wbindgen_start: () => void;
}

export type SyncInitInput = BufferSource | WebAssembly.Module;

/**
 * Instantiates the given `module`, which can either be bytes or
 * a precompiled `WebAssembly.Module`.
 *
 * @param {{ module: SyncInitInput }} module - Passing `SyncInitInput` directly is deprecated.
 *
 * @returns {InitOutput}
 */
export function initSync(module: { module: SyncInitInput } | SyncInitInput): InitOutput;

/**
 * If `module_or_path` is {RequestInfo} or {URL}, makes a request and
 * for everything else, calls `WebAssembly.instantiate` directly.
 *
 * @param {{ module_or_path: InitInput | Promise<InitInput> }} module_or_path - Passing `InitInput` directly is deprecated.
 *
 * @returns {Promise<InitOutput>}
 */
export default function __wbg_init (module_or_path?: { module_or_path: InitInput | Promise<InitInput> } | InitInput | Promise<InitInput>): Promise<InitOutput>;
