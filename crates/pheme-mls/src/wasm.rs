//! WASM bindings: the stateful Client, callable from the React app's crypto layer.
//!
//! Bytes cross the boundary as `Uint8Array` (wasm-bindgen maps `Vec<u8>` /
//! `&[u8]` to it). The app base64-encodes for the JSON transport itself; here
//! everything is raw bytes.

use wasm_bindgen::prelude::*;

use crate::Client;

#[wasm_bindgen]
pub struct MlsClient {
    inner: Client,
}

#[wasm_bindgen]
impl MlsClient {
    /// Creates a fresh identity for one DEVICE of one user. Both ids are required —
    /// an MLS leaf is a device, and a client that cannot say which device it is ends up
    /// sharing a leaf with the user's other devices, which then cannot decrypt.
    #[wasm_bindgen(constructor)]
    pub fn new(user_id: &str, device_id: &str) -> Result<MlsClient, JsError> {
        Ok(MlsClient {
            inner: Client::new(user_id, device_id).map_err(js)?,
        })
    }

    /// Restores a client from a previously exported state blob.
    #[wasm_bindgen(js_name = fromState)]
    pub fn from_state(state: &[u8]) -> Result<MlsClient, JsError> {
        Ok(MlsClient {
            inner: Client::import_state(state).map_err(js)?,
        })
    }

    /// This client's credential identity, `userId:deviceId`.
    ///
    /// It is the authoritative answer to "which device am I?". A restored backup carries
    /// the identity of the device it was taken FROM, and the groups in that state hold
    /// leaves under that name — so the browser it is restored into has to answer to it,
    /// whatever its own local storage happens to say.
    #[wasm_bindgen(getter)]
    pub fn identity(&self) -> String {
        String::from_utf8_lossy(self.inner.identity()).into_owned()
    }

    /// A single-use public KeyPackage to publish to the server.
    #[wasm_bindgen(js_name = keyPackage)]
    pub fn key_package(&self) -> Result<Vec<u8>, JsError> {
        self.inner.key_package().map_err(js)
    }

    /// A reusable last-resort KeyPackage, so the user can always be added to a group
    /// even after their single-use stock has been claimed.
    #[wasm_bindgen(js_name = lastResortKeyPackage)]
    pub fn last_resort_key_package(&self) -> Result<Vec<u8>, JsError> {
        self.inner.last_resort_key_package().map_err(js)
    }

    #[wasm_bindgen(js_name = createGroup)]
    pub fn create_group(&self, group_id: &[u8]) -> Result<(), JsError> {
        self.inner.create_group(group_id).map_err(js)
    }

    /// Discards a group so it can be rebuilt (repairing a member who could never join).
    #[wasm_bindgen(js_name = deleteGroup)]
    pub fn delete_group(&self, group_id: &[u8]) -> Result<(), JsError> {
        self.inner.delete_group(group_id).map_err(js)
    }

    /// Whether this client is already in the group (non-mutating).
    #[wasm_bindgen(js_name = hasGroup)]
    pub fn has_group(&self, group_id: &[u8]) -> bool {
        self.inner.has_group(group_id)
    }

    /// STAGES the addition of several devices in one Commit (all newcomers land at the
    /// same epoch). `key_packages` is a JS array of Uint8Array; one Welcome covers all.
    ///
    /// The Commit is NOT applied. Call `commitAccepted` once the server has taken it as
    /// the group's next epoch, or `commitRejected` if another member's Commit landed
    /// first. Applying it before the server agrees is what forks a client off the group
    /// for good.
    #[wasm_bindgen(js_name = stageAdd)]
    pub fn stage_add(
        &self,
        group_id: &[u8],
        key_packages: js_sys::Array,
    ) -> Result<AddOutput, JsError> {
        let kps: Vec<Vec<u8>> = key_packages
            .iter()
            .map(|v| js_sys::Uint8Array::new(&v).to_vec())
            .collect();
        let r = self.inner.stage_add(group_id, &kps).map_err(js)?;
        Ok(AddOutput {
            welcome: r.welcome,
            commit: r.commit,
        })
    }

    /// STAGES the removal of every device belonging to each of `user_ids` (a JS array of
    /// strings). Not applied until `commitAccepted`. Removing only one leaf would leave
    /// the removed member reading the group from their other device.
    ///
    /// This client's own leaves are never removed: MLS forbids committing your own
    /// removal, so leaving is not a Commit — see the crate docs.
    #[wasm_bindgen(js_name = stageRemoveUsers)]
    pub fn stage_remove_users(
        &self,
        group_id: &[u8],
        user_ids: js_sys::Array,
    ) -> Result<Vec<u8>, JsError> {
        let ids: Vec<String> = user_ids.iter().filter_map(|v| v.as_string()).collect();
        self.inner.stage_remove_users(group_id, &ids).map_err(js)
    }

    /// STAGES the removal of the exact leaves named by `identities` (`userId:deviceId`).
    ///
    /// For pruning a ghost device — one whose key material no longer exists anywhere —
    /// while leaving that person's live devices alone. Removing by USER would take their
    /// working phone out along with the ghost.
    #[wasm_bindgen(js_name = stageRemoveDevices)]
    pub fn stage_remove_devices(
        &self,
        group_id: &[u8],
        identities: js_sys::Array,
    ) -> Result<Vec<u8>, JsError> {
        let ids: Vec<String> = identities.iter().filter_map(|v| v.as_string()).collect();
        self.inner.stage_remove_devices(group_id, &ids).map_err(js)
    }

    /// Applies the Commit we staged, now that the server has accepted it.
    #[wasm_bindgen(js_name = commitAccepted)]
    pub fn commit_accepted(&self, group_id: &[u8]) -> Result<(), JsError> {
        self.inner.commit_accepted(group_id).map_err(js)
    }

    /// Throws away a Commit the server refused, leaving the group untouched so we can
    /// catch up on the winning Commit and try again.
    #[wasm_bindgen(js_name = commitRejected)]
    pub fn commit_rejected(&self, group_id: &[u8]) -> Result<(), JsError> {
        self.inner.commit_rejected(group_id).map_err(js)
    }

    /// The group's current epoch — what a Commit is proposed against.
    pub fn epoch(&self, group_id: &[u8]) -> Result<u64, JsError> {
        self.inner.epoch(group_id).map_err(js)
    }

    /// Derives a secret from the group for a purpose outside MLS's own messaging — Pheme
    /// uses it to key voice-call signalling, so the server cannot read the SDP and
    /// therefore cannot swap the DTLS fingerprint inside it.
    ///
    /// A pure read: it mutates neither the group nor the stored state, so unlike an MLS
    /// application message it can be called freely without churning the ratchet or the
    /// key store. It exports from the CURRENT EPOCH — see the crate docs.
    #[wasm_bindgen(js_name = exportSecret)]
    pub fn export_secret(
        &self,
        group_id: &[u8],
        label: &str,
        context: &[u8],
        length: usize,
    ) -> Result<Vec<u8>, JsError> {
        self.inner
            .export_secret(group_id, label, context, length)
            .map_err(js)
    }

    /// Every leaf's `userId:deviceId`, so the caller can spot member devices that are
    /// missing from the group and add exactly those.
    #[wasm_bindgen(js_name = memberIdentities)]
    pub fn member_identities(&self, group_id: &[u8]) -> Result<js_sys::Array, JsError> {
        let out = js_sys::Array::new();
        for id in self.inner.member_identities(group_id).map_err(js)? {
            out.push(&JsValue::from_str(&String::from_utf8_lossy(&id)));
        }
        Ok(out)
    }

    #[wasm_bindgen(js_name = joinFromWelcome)]
    pub fn join_from_welcome(&self, welcome: &[u8]) -> Result<(), JsError> {
        self.inner.join_from_welcome(welcome).map_err(js)
    }

    /// The self-contained GroupInfo a NON-MEMBER needs to join this group by external commit.
    /// A pure read; nothing is persisted.
    #[wasm_bindgen(js_name = exportGroupInfo)]
    pub fn export_group_info(&self, group_id: &[u8]) -> Result<Vec<u8>, JsError> {
        self.inner.export_group_info(group_id).map_err(js)
    }

    /// Joins an existing group by external commit, from a member's GroupInfo — adds this client's
    /// own leaf with no Welcome and no member online. Returns the commit to offer the server through
    /// the ordinary compare-and-set. The commit is left PENDING and, unlike a staged commit, cannot
    /// be cleared: on acceptance call `commitAccepted`, on refusal call `deleteGroup` and retry from
    /// fresh GroupInfo.
    #[wasm_bindgen(js_name = joinByExternalCommit)]
    pub fn join_by_external_commit(&self, group_info: &[u8]) -> Result<Vec<u8>, JsError> {
        self.inner.join_by_external_commit(group_info).map_err(js)
    }

    #[wasm_bindgen(js_name = applyCommit)]
    pub fn apply_commit(&self, group_id: &[u8], commit: &[u8]) -> Result<(), JsError> {
        self.inner.apply_commit(group_id, commit).map_err(js)
    }

    pub fn encrypt(&self, group_id: &[u8], plaintext: &[u8]) -> Result<Vec<u8>, JsError> {
        self.inner.encrypt(group_id, plaintext).map_err(js)
    }

    /// Decrypts an application message; returns undefined for a control message.
    pub fn decrypt(&self, group_id: &[u8], ciphertext: &[u8]) -> Result<Option<Vec<u8>>, JsError> {
        self.inner.decrypt(group_id, ciphertext).map_err(js)
    }

    /// The full client state to persist (IndexedDB).
    #[wasm_bindgen(js_name = exportState)]
    pub fn export_state(&self) -> Result<Vec<u8>, JsError> {
        self.inner.export_state().map_err(js)
    }

    /// The safety number for a group: the digits two people compare, out of band, to
    /// prove the server did not substitute a key and put itself in the middle.
    /// Derived from the group's own ratchet tree, not from anything the server says.
    #[wasm_bindgen(js_name = safetyNumber)]
    pub fn safety_number(&self, group_id: &[u8]) -> Result<String, JsError> {
        let keys = self.inner.member_keys(group_id).map_err(js)?;
        Ok(crate::safety_number(&keys))
    }

    /// This client's own long-term signature public key.
    #[wasm_bindgen(js_name = identityKey)]
    pub fn identity_key(&self) -> Vec<u8> {
        self.inner.identity_key()
    }
}

/// The Welcome + Commit produced by adding a member, returned to JS as an object.
#[wasm_bindgen(getter_with_clone)]
pub struct AddOutput {
    pub welcome: Vec<u8>,
    pub commit: Vec<u8>,
}

/// A sealed key backup: salt, nonce and ciphertext, all stored server-side.
#[wasm_bindgen(getter_with_clone)]
pub struct BackupBlob {
    pub salt: Vec<u8>,
    pub nonce: Vec<u8>,
    pub ciphertext: Vec<u8>,
}

/// A read-only client for rendering notification previews in a service worker.
///
/// Separate from `MlsClient` on purpose, and this is the whole reason it exists: a service
/// worker is a SECOND context holding the same key store as the page, and the single-client
/// rule says there must never be two writers. This one has no `exportState`, so there is
/// nowhere for an advanced ratchet to go — the worker physically cannot persist, however a
/// later edit is written. See `crate::PreviewClient`.
///
/// The page's own copy of the state is untouched and decrypts the message again for real when
/// the app opens, so nothing is lost by previewing it here.
///
/// Drop it as soon as the notification is shown: it holds plaintext.
#[wasm_bindgen]
pub struct MlsPreviewClient {
    inner: crate::PreviewClient,
}

#[wasm_bindgen]
impl MlsPreviewClient {
    /// Loads a read-only client from a state blob read out of IndexedDB.
    #[wasm_bindgen(js_name = fromState)]
    pub fn from_state(state: &[u8]) -> Result<MlsPreviewClient, JsError> {
        Ok(MlsPreviewClient {
            inner: crate::PreviewClient::import_state(state).map_err(js)?,
        })
    }

    /// Whether this client holds the group, so the caller can pick the right one without
    /// attempting a decrypt against each in turn.
    #[wasm_bindgen(js_name = hasGroup)]
    pub fn has_group(&self, group_id: &[u8]) -> bool {
        self.inner.has_group(group_id)
    }

    /// Decrypts one application message for display. `undefined` means there was nothing to
    /// preview — control traffic, or a message this client cannot read.
    pub fn decrypt(&self, group_id: &[u8], ciphertext: &[u8]) -> Result<Option<Vec<u8>>, JsError> {
        self.inner.decrypt(group_id, ciphertext).map_err(js)
    }
}

/// Seals exported client state under a recovery passphrase (Argon2id + AES-256-GCM).
#[wasm_bindgen(js_name = encryptBackup)]
pub fn encrypt_backup(passphrase: &[u8], plaintext: &[u8]) -> Result<BackupBlob, JsError> {
    let b = crate::backup::encrypt(passphrase, plaintext).map_err(js)?;
    Ok(BackupBlob {
        salt: b.salt,
        nonce: b.nonce,
        ciphertext: b.ciphertext,
    })
}

/// Recovers client state from a sealed backup. Errors on a wrong passphrase.
#[wasm_bindgen(js_name = decryptBackup)]
pub fn decrypt_backup(
    passphrase: &[u8],
    salt: &[u8],
    nonce: &[u8],
    ciphertext: &[u8],
) -> Result<Vec<u8>, JsError> {
    crate::backup::decrypt(passphrase, salt, nonce, ciphertext).map_err(js)
}

fn js(e: String) -> JsError {
    JsError::new(&e)
}
