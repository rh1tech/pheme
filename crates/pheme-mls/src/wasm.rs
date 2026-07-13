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
    /// Creates a fresh identity. `identity` is the user/device id bytes.
    #[wasm_bindgen(constructor)]
    pub fn new(identity: &[u8]) -> Result<MlsClient, JsError> {
        Ok(MlsClient {
            inner: Client::new(identity).map_err(js)?,
        })
    }

    /// Restores a client from a previously exported state blob.
    #[wasm_bindgen(js_name = fromState)]
    pub fn from_state(state: &[u8]) -> Result<MlsClient, JsError> {
        Ok(MlsClient {
            inner: Client::import_state(state).map_err(js)?,
        })
    }

    /// A single-use public KeyPackage to publish to the server.
    #[wasm_bindgen(js_name = keyPackage)]
    pub fn key_package(&self) -> Result<Vec<u8>, JsError> {
        self.inner.key_package().map_err(js)
    }

    #[wasm_bindgen(js_name = createGroup)]
    pub fn create_group(&self, group_id: &[u8]) -> Result<(), JsError> {
        self.inner.create_group(group_id).map_err(js)
    }

    /// Whether this client is already in the group (non-mutating).
    #[wasm_bindgen(js_name = hasGroup)]
    pub fn has_group(&self, group_id: &[u8]) -> bool {
        self.inner.has_group(group_id)
    }

    /// Adds a member; returns their Welcome and the group's Commit.
    #[wasm_bindgen(js_name = addMember)]
    pub fn add_member(&self, group_id: &[u8], key_package: &[u8]) -> Result<AddOutput, JsError> {
        let r = self.inner.add_member(group_id, key_package).map_err(js)?;
        Ok(AddOutput {
            welcome: r.welcome,
            commit: r.commit,
        })
    }

    /// Adds several members in one Commit (all newcomers land at the same epoch).
    /// `key_packages` is a JS array of Uint8Array. Returns a single Welcome for all.
    #[wasm_bindgen(js_name = addMembers)]
    pub fn add_members(
        &self,
        group_id: &[u8],
        key_packages: js_sys::Array,
    ) -> Result<AddOutput, JsError> {
        let kps: Vec<Vec<u8>> = key_packages
            .iter()
            .map(|v| js_sys::Uint8Array::new(&v).to_vec())
            .collect();
        let r = self.inner.add_members(group_id, &kps).map_err(js)?;
        Ok(AddOutput {
            welcome: r.welcome,
            commit: r.commit,
        })
    }

    #[wasm_bindgen(js_name = joinFromWelcome)]
    pub fn join_from_welcome(&self, welcome: &[u8]) -> Result<(), JsError> {
        self.inner.join_from_welcome(welcome).map_err(js)
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
}

/// The Welcome + Commit produced by adding a member, returned to JS as an object.
#[wasm_bindgen(getter_with_clone)]
pub struct AddOutput {
    pub welcome: Vec<u8>,
    pub commit: Vec<u8>,
}

fn js(e: String) -> JsError {
    JsError::new(&e)
}
