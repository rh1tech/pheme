//! The Dart-facing MLS surface. The mobile counterpart of `crates/pheme-mls/src/wasm.rs`.
//!
//! Two things here are load-bearing and neither is obvious from the signatures.
//!
//! **One client, one lock.** Every `Client` method takes `&self` and mutates through interior
//! mutability, and flutter_rust_bridge dispatches Rust calls on a worker-thread pool — so two Dart
//! futures can genuinely be inside the ratchet at the same time. Nothing in the type system stops
//! that. The client therefore lives behind a global mutex rather than in a handle Dart could hold
//! two copies of, which makes "exactly one client per process" structural instead of a convention
//! the Dart side has to remember.
//!
//! **A mutation and its state export are one operation.** `export_state()` reads the whole key
//! store; called outside the lock that performed the mutation, it can read a half-written one. So
//! every mutating call returns the new state blob alongside its result, exported while the lock is
//! still held, and Dart persists what it is given rather than asking for it afterwards. Anything
//! that returns a `state` field must be written to disk before its result is acted on.

use anyhow::{anyhow, Result};
use parking_lot::{const_mutex, Mutex};
use pheme_mls::Client;

/// The one live client. `None` until `mlsCreate`/`mlsLoad`.
static CLIENT: Mutex<Option<Client>> = const_mutex(None);

// ---------------------------------------------------------------------------
// Return shapes.
//
// Each mutating call hands back the state to persist. Dart never calls "export state" on its own —
// there is no safe moment for it to do so.
// ---------------------------------------------------------------------------

/// A mutation with no other result.
pub struct Applied {
    pub state: Vec<u8>,
}

/// A mutation whose result is one blob: a key package, or a commit, or a ciphertext.
pub struct Bytes {
    pub bytes: Vec<u8>,
    pub state: Vec<u8>,
}

/// A staged Add: one Welcome covering every newcomer, and the Commit that adds them.
///
/// NOT applied. The server arbitrates: `mlsCommitAccepted` on 200, `mlsCommitRejected` on 409.
pub struct Staged {
    pub welcome: Vec<u8>,
    pub commit: Vec<u8>,
    pub state: Vec<u8>,
}

/// A decrypted message. `plaintext` is `None` for a control message — which is a success, not a
/// failure, and must not be treated as one.
pub struct Opened {
    pub plaintext: Option<Vec<u8>>,
    pub state: Vec<u8>,
}

/// A sealed key backup, stored server-side. The server sees these three fields and never the
/// passphrase or the plaintext.
pub struct BackupBlob {
    pub salt: Vec<u8>,
    pub nonce: Vec<u8>,
    pub ciphertext: Vec<u8>,
}

// ---------------------------------------------------------------------------
// Lock helpers.
// ---------------------------------------------------------------------------

/// A pure read. Touches neither the ratchet nor the stored state.
fn read<T>(f: impl FnOnce(&Client) -> Result<T, String>) -> Result<T> {
    let guard = CLIENT.lock();
    let client = guard.as_ref().ok_or_else(|| anyhow!("mls: no client loaded"))?;
    f(client).map_err(|e| anyhow!(e))
}

/// A mutation. Exports the resulting state while still holding the lock — see the module docs.
fn mutate<T>(f: impl FnOnce(&Client) -> Result<T, String>) -> Result<(T, Vec<u8>)> {
    let guard = CLIENT.lock();
    let client = guard.as_ref().ok_or_else(|| anyhow!("mls: no client loaded"))?;
    let value = f(client).map_err(|e| anyhow!(e))?;
    let state = client.export_state().map_err(|e| anyhow!(e))?;
    Ok((value, state))
}

fn applied(r: Result<((), Vec<u8>)>) -> Result<Applied> {
    r.map(|(_, state)| Applied { state })
}

fn bytes(r: Result<(Vec<u8>, Vec<u8>)>) -> Result<Bytes> {
    r.map(|(bytes, state)| Bytes { bytes, state })
}

// ---------------------------------------------------------------------------
// Lifecycle.
// ---------------------------------------------------------------------------

/// Mints a fresh identity for one DEVICE of one user and installs it as the live client.
///
/// Both ids are required. An MLS leaf is a device, not a person: a client that cannot say which
/// device it is shares a leaf with the user's other devices, and then none of them can decrypt.
pub fn mls_create(user_id: String, device_id: String) -> Result<Vec<u8>> {
    let client = Client::new(&user_id, &device_id).map_err(|e| anyhow!(e))?;
    let state = client.export_state().map_err(|e| anyhow!(e))?;
    *CLIENT.lock() = Some(client);
    Ok(state)
}

/// Restores the live client from a persisted state blob.
pub fn mls_load(state: Vec<u8>) -> Result<()> {
    let client = Client::import_state(&state).map_err(|e| anyhow!(e))?;
    *CLIENT.lock() = Some(client);
    Ok(())
}

pub fn mls_is_loaded() -> bool {
    CLIENT.lock().is_some()
}

/// Drops the client. Logout: the caller must delete the persisted blob and its data key too.
pub fn mls_unload() {
    *CLIENT.lock() = None;
}

/// This client's credential identity, `userId:deviceId`.
///
/// The authoritative answer to "which device am I?". A restored backup carries the identity of the
/// device it was taken FROM, and the groups inside it hold leaves under that name — so the device
/// it is restored onto has to answer to it, whatever its own storage says.
pub fn mls_identity() -> Result<String> {
    read(|c| Ok(String::from_utf8_lossy(c.identity()).into_owned()))
}

/// This client's long-term signature public key.
pub fn mls_identity_key() -> Result<Vec<u8>> {
    read(|c| Ok(c.identity_key()))
}

// ---------------------------------------------------------------------------
// Key packages.
//
// Both of these mint and STORE a private init key, so both mutate.
// ---------------------------------------------------------------------------

/// A single-use public KeyPackage to publish.
pub fn mls_key_package() -> Result<Bytes> {
    bytes(mutate(|c| c.key_package()))
}

/// A reusable last-resort KeyPackage, so a user can still be added to a group after their
/// single-use stock has been claimed.
pub fn mls_last_resort_key_package() -> Result<Bytes> {
    bytes(mutate(|c| c.last_resort_key_package()))
}

// ---------------------------------------------------------------------------
// Groups.
// ---------------------------------------------------------------------------

pub fn mls_create_group(group_id: Vec<u8>) -> Result<Applied> {
    applied(mutate(|c| c.create_group(&group_id)))
}

/// Discards a group so it can be rebuilt — repairing a device that could never join.
pub fn mls_delete_group(group_id: Vec<u8>) -> Result<Applied> {
    applied(mutate(|c| c.delete_group(&group_id)))
}

/// Whether this client already holds the group. A pure read.
pub fn mls_has_group(group_id: Vec<u8>) -> Result<bool> {
    read(|c| Ok(c.has_group(&group_id)))
}

pub fn mls_join_from_welcome(welcome: Vec<u8>) -> Result<Applied> {
    applied(mutate(|c| c.join_from_welcome(&welcome)))
}

/// Exports the GroupInfo a NON-MEMBER needs to join by external commit. A pure read: GroupInfo is a
/// signed snapshot of the current epoch, and producing it changes nothing.
pub fn mls_export_group_info(group_id: Vec<u8>) -> Result<Vec<u8>> {
    read(|c| c.export_group_info(&group_id))
}

/// Joins an existing group by EXTERNAL COMMIT — adds this device's own leaf from a member's exported
/// GroupInfo, with no Welcome and no member's help. Returns the external commit to offer the server.
///
/// The group is created here with that commit PENDING. On acceptance call `mlsCommitAccepted` to merge
/// it; on refusal call `mlsDeleteGroup` (NOT `mlsCommitRejected` — an external commit cannot be
/// cleared) and rejoin from fresh GroupInfo.
pub fn mls_join_by_external_commit(group_info: Vec<u8>) -> Result<Bytes> {
    bytes(mutate(|c| c.join_by_external_commit(&group_info)))
}

pub fn mls_apply_commit(group_id: Vec<u8>, commit: Vec<u8>) -> Result<Applied> {
    applied(mutate(|c| c.apply_commit(&group_id, &commit)))
}

/// STAGES the addition of several devices in one Commit, so every newcomer lands at the same epoch.
/// One Welcome covers all of them.
///
/// The Commit is NOT applied. Post it, then call `mlsCommitAccepted` once the server has taken it as
/// the group's next epoch, or `mlsCommitRejected` if another member's Commit landed first. Applying
/// a Commit the server refused forks this device off the group permanently, and silently.
pub fn mls_stage_add(group_id: Vec<u8>, key_packages: Vec<Vec<u8>>) -> Result<Staged> {
    mutate(|c| c.stage_add(&group_id, &key_packages)).map(|(r, state)| Staged {
        welcome: r.welcome,
        commit: r.commit,
        state,
    })
}

/// STAGES the removal of every device belonging to each of `user_ids`.
///
/// Removing one leaf would leave the removed member reading the group from their other phone.
pub fn mls_stage_remove_users(group_id: Vec<u8>, user_ids: Vec<String>) -> Result<Bytes> {
    bytes(mutate(|c| c.stage_remove_users(&group_id, &user_ids)))
}

/// STAGES the removal of the exact leaves named by `identities` (`userId:deviceId`).
///
/// For pruning a ghost device — one whose key material no longer exists anywhere — without taking
/// that person's working phone out along with it.
pub fn mls_stage_remove_devices(group_id: Vec<u8>, identities: Vec<String>) -> Result<Bytes> {
    bytes(mutate(|c| c.stage_remove_devices(&group_id, &identities)))
}

/// Merges the Commit we staged, now that the server has accepted it.
pub fn mls_commit_accepted(group_id: Vec<u8>) -> Result<Applied> {
    applied(mutate(|c| c.commit_accepted(&group_id)))
}

/// Discards a Commit the server refused, leaving the group untouched so we can catch up on the
/// winning Commit and try again.
pub fn mls_commit_rejected(group_id: Vec<u8>) -> Result<Applied> {
    applied(mutate(|c| c.commit_rejected(&group_id)))
}

/// The group's current epoch — what a Commit is proposed against. A pure read.
pub fn mls_epoch(group_id: Vec<u8>) -> Result<u64> {
    read(|c| c.epoch(&group_id))
}

/// Every leaf's `userId:deviceId`, so the caller can spot member devices missing from the group and
/// add exactly those. A pure read.
pub fn mls_member_identities(group_id: Vec<u8>) -> Result<Vec<String>> {
    read(|c| {
        Ok(c.member_identities(&group_id)?
            .iter()
            .map(|id| String::from_utf8_lossy(id).into_owned())
            .collect())
    })
}

// ---------------------------------------------------------------------------
// Messages.
// ---------------------------------------------------------------------------

/// Encrypts an application message.
///
/// MLS destroys the message key as it goes, so the sender can never decrypt this again. The caller
/// must cache the plaintext locally or the message it just sent will render blank forever.
pub fn mls_encrypt(group_id: Vec<u8>, plaintext: Vec<u8>) -> Result<Bytes> {
    bytes(mutate(|c| c.encrypt(&group_id, &plaintext)))
}

/// Decrypts an application message. Consumes the key: a message decrypts exactly once, so the
/// plaintext must be cached on first sight.
///
/// `plaintext == None` means this was a control message, not a failure.
pub fn mls_decrypt(group_id: Vec<u8>, ciphertext: Vec<u8>) -> Result<Opened> {
    mutate(|c| c.decrypt(&group_id, &ciphertext)).map(|(plaintext, state)| Opened { plaintext, state })
}

/// Derives a secret from the group for a purpose outside MLS's own messaging — Pheme keys voice-call
/// signalling with it, so the server cannot read the SDP and therefore cannot swap the DTLS
/// fingerprint inside it and sit in the middle of the call.
///
/// A pure read: it churns neither the ratchet nor the key store. It exports from the CURRENT EPOCH
/// only, which is why a caller must catch up before deriving and a receiver that is ahead of the
/// sender cannot derive the sender's key at all.
pub fn mls_export_secret(
    group_id: Vec<u8>,
    label: String,
    context: Vec<u8>,
    length: usize,
) -> Result<Vec<u8>> {
    read(|c| c.export_secret(&group_id, &label, &context, length))
}

/// The digits two people read to each other, out of band, to prove the server did not substitute a
/// key and put itself in the middle. Derived from the group's own ratchet tree, never from anything
/// the server says.
pub fn mls_safety_number(group_id: Vec<u8>) -> Result<String> {
    read(|c| {
        let keys = c.member_keys(&group_id)?;
        Ok(pheme_mls::safety_number(&keys))
    })
}

// ---------------------------------------------------------------------------
// Key backup: Argon2id + AES-256-GCM, sealed under a passphrase, stored server-side.
// ---------------------------------------------------------------------------

/// Seals the live client's state under a recovery passphrase.
pub fn mls_backup_encrypt(passphrase: Vec<u8>, plaintext: Vec<u8>) -> Result<BackupBlob> {
    let b = pheme_mls::backup::encrypt(&passphrase, &plaintext).map_err(|e| anyhow!(e))?;
    Ok(BackupBlob {
        salt: b.salt,
        nonce: b.nonce,
        ciphertext: b.ciphertext,
    })
}

/// Recovers client state from a sealed backup. Errors on a wrong passphrase.
pub fn mls_backup_decrypt(
    passphrase: Vec<u8>,
    salt: Vec<u8>,
    nonce: Vec<u8>,
    ciphertext: Vec<u8>,
) -> Result<Vec<u8>> {
    pheme_mls::backup::decrypt(&passphrase, &salt, &nonce, &ciphertext).map_err(|e| anyhow!(e))
}

#[flutter_rust_bridge::frb(init)]
pub fn init_app() {
    flutter_rust_bridge::setup_default_user_utils();
}

#[cfg(test)]
mod tests {
    use super::*;

    /// The client is a process-wide global, so these tests cannot run in parallel with each other.
    /// They swap the live client mid-test on purpose — that is how one process plays two devices.
    static TEST: Mutex<()> = const_mutex(());

    /// Mints a device and returns the state to persist for it.
    fn fresh(user: &str, device: &str) -> Vec<u8> {
        mls_create(user.into(), device.into()).unwrap()
    }

    #[test]
    fn identity_is_user_colon_device() {
        let _guard = TEST.lock();
        fresh("alice", "phone");
        assert_eq!(mls_identity().unwrap(), "alice:phone");
    }

    #[test]
    fn a_message_round_trips_from_one_device_to_another() {
        let _guard = TEST.lock();

        // Bob publishes a key package. His state must be captured AFTER minting it: the key package
        // is public, but its private half lives in the state, and without that half he cannot open
        // the Welcome that was sealed to it. Persisting the pre-mint state is exactly the bug the
        // `Bytes { bytes, state }` shape exists to prevent.
        fresh("bob", "phone");
        let bob_kp = mls_key_package().unwrap();
        let (bob_state, bob_key_package) = (bob_kp.state, bob_kp.bytes);

        // Alice mints the group, adds Bob's device, and sends.
        fresh("alice", "phone");
        let gid = b"group-1".to_vec();
        mls_create_group(gid.clone()).unwrap();
        let staged = mls_stage_add(gid.clone(), vec![bob_key_package]).unwrap();
        mls_commit_accepted(gid.clone()).unwrap();
        let sent = mls_encrypt(gid.clone(), b"hello".to_vec()).unwrap();
        let (alice_state, ciphertext) = (sent.state, sent.bytes);

        // Bob joins from the Welcome and reads it.
        mls_load(bob_state).unwrap();
        mls_join_from_welcome(staged.welcome).unwrap();
        let opened = mls_decrypt(gid.clone(), ciphertext.clone()).unwrap();
        assert_eq!(opened.plaintext.unwrap(), b"hello");

        // Bob cannot read it a second time. A message decrypts exactly once, which is why the Dart
        // side must cache the plaintext on first sight.
        assert!(mls_decrypt(gid.clone(), ciphertext.clone()).is_err());

        // And Alice can never read her own message back. Same reason the Dart side writes a sent
        // body straight into the cache instead of round-tripping it through the group.
        mls_load(alice_state).unwrap();
        assert!(mls_decrypt(gid, ciphertext).is_err());
    }

    #[test]
    fn state_survives_an_export_import_round_trip() {
        let _guard = TEST.lock();
        fresh("carol", "laptop");
        let gid = b"group-2".to_vec();
        let state = mls_create_group(gid.clone()).unwrap().state;

        mls_unload();
        assert!(!mls_is_loaded());

        mls_load(state).unwrap();
        assert!(mls_has_group(gid).unwrap());
        assert_eq!(mls_identity().unwrap(), "carol:laptop");
    }

    #[test]
    fn a_rejected_commit_leaves_the_group_where_it_was() {
        let _guard = TEST.lock();
        fresh("erin", "phone");
        let kp = mls_key_package().unwrap().bytes;

        fresh("dave", "phone");
        let gid = b"group-3".to_vec();
        mls_create_group(gid.clone()).unwrap();
        let before = mls_epoch(gid.clone()).unwrap();

        mls_stage_add(gid.clone(), vec![kp]).unwrap();
        // The server took someone else's commit instead. Discarding must not move us.
        mls_commit_rejected(gid.clone()).unwrap();

        assert_eq!(mls_epoch(gid).unwrap(), before);
    }

    #[test]
    fn a_backup_opens_with_the_right_passphrase_and_not_the_wrong_one() {
        let _guard = TEST.lock();
        let state = fresh("frank", "phone");
        let blob = mls_backup_encrypt(b"correct horse".to_vec(), state.clone()).unwrap();

        let out = mls_backup_decrypt(
            b"correct horse".to_vec(),
            blob.salt.clone(),
            blob.nonce.clone(),
            blob.ciphertext.clone(),
        )
        .unwrap();
        assert_eq!(out, state);

        assert!(mls_backup_decrypt(
            b"wrong horse".to_vec(),
            blob.salt,
            blob.nonce,
            blob.ciphertext
        )
        .is_err());
    }
}
