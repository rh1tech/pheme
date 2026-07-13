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

export class MlsClient {
    free(): void;
    [Symbol.dispose](): void;
    /**
     * Adds a member; returns their Welcome and the group's Commit.
     */
    addMember(group_id: Uint8Array, key_package: Uint8Array): AddOutput;
    /**
     * Adds several members in one Commit (all newcomers land at the same epoch).
     * `key_packages` is a JS array of Uint8Array. Returns a single Welcome for all.
     */
    addMembers(group_id: Uint8Array, key_packages: Array<any>): AddOutput;
    applyCommit(group_id: Uint8Array, commit: Uint8Array): void;
    createGroup(group_id: Uint8Array): void;
    /**
     * Decrypts an application message; returns undefined for a control message.
     */
    decrypt(group_id: Uint8Array, ciphertext: Uint8Array): Uint8Array | undefined;
    /**
     * Discards a group so it can be rebuilt (repairing a member who could never join).
     */
    deleteGroup(group_id: Uint8Array): void;
    encrypt(group_id: Uint8Array, plaintext: Uint8Array): Uint8Array;
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
     * Creates a fresh identity. `identity` is the user/device id bytes.
     */
    constructor(identity: Uint8Array);
    /**
     * The safety number for a group: the digits two people compare, out of band, to
     * prove the server did not substitute a key and put itself in the middle.
     * Derived from the group's own ratchet tree, not from anything the server says.
     */
    safetyNumber(group_id: Uint8Array): string;
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
    readonly __wbg_get_addoutput_commit: (a: number) => [number, number];
    readonly __wbg_get_addoutput_welcome: (a: number) => [number, number];
    readonly __wbg_get_backupblob_ciphertext: (a: number) => [number, number];
    readonly __wbg_mlsclient_free: (a: number, b: number) => void;
    readonly __wbg_set_addoutput_commit: (a: number, b: number, c: number) => void;
    readonly __wbg_set_addoutput_welcome: (a: number, b: number, c: number) => void;
    readonly __wbg_set_backupblob_ciphertext: (a: number, b: number, c: number) => void;
    readonly decryptBackup: (a: number, b: number, c: number, d: number, e: number, f: number, g: number, h: number) => [number, number, number, number];
    readonly encryptBackup: (a: number, b: number, c: number, d: number) => [number, number, number];
    readonly mlsclient_addMember: (a: number, b: number, c: number, d: number, e: number) => [number, number, number];
    readonly mlsclient_addMembers: (a: number, b: number, c: number, d: any) => [number, number, number];
    readonly mlsclient_applyCommit: (a: number, b: number, c: number, d: number, e: number) => [number, number];
    readonly mlsclient_createGroup: (a: number, b: number, c: number) => [number, number];
    readonly mlsclient_decrypt: (a: number, b: number, c: number, d: number, e: number) => [number, number, number, number];
    readonly mlsclient_deleteGroup: (a: number, b: number, c: number) => [number, number];
    readonly mlsclient_encrypt: (a: number, b: number, c: number, d: number, e: number) => [number, number, number, number];
    readonly mlsclient_exportState: (a: number) => [number, number, number, number];
    readonly mlsclient_fromState: (a: number, b: number) => [number, number, number];
    readonly mlsclient_hasGroup: (a: number, b: number, c: number) => number;
    readonly mlsclient_identityKey: (a: number) => [number, number];
    readonly mlsclient_joinFromWelcome: (a: number, b: number, c: number) => [number, number];
    readonly mlsclient_keyPackage: (a: number) => [number, number, number, number];
    readonly mlsclient_lastResortKeyPackage: (a: number) => [number, number, number, number];
    readonly mlsclient_new: (a: number, b: number) => [number, number, number];
    readonly mlsclient_safetyNumber: (a: number, b: number, c: number) => [number, number, number, number];
    readonly __wbg_set_backupblob_nonce: (a: number, b: number, c: number) => void;
    readonly __wbg_set_backupblob_salt: (a: number, b: number, c: number) => void;
    readonly __wbg_get_backupblob_nonce: (a: number) => [number, number];
    readonly __wbg_get_backupblob_salt: (a: number) => [number, number];
    readonly __wbindgen_exn_store: (a: number) => void;
    readonly __externref_table_alloc: () => number;
    readonly __wbindgen_externrefs: WebAssembly.Table;
    readonly __wbindgen_malloc: (a: number, b: number) => number;
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
