//! Pheme MLS spike.
//!
//! Purpose: prove that OpenMLS (RFC 9420) can do a two-party group round-trip —
//! create group, add a member, exchange an encrypted application message — AND
//! that the same code compiles to `wasm32-unknown-unknown` for the React client.
//! This is a feasibility gate for the E2EE plan, not production code.
//!
//! The server never appears here: in MLS it is an untrusted Delivery Service that
//! only relays the opaque bytes these functions produce.

use openmls::prelude::tls_codec::Deserialize as _;
use openmls::prelude::*;
use openmls_basic_credential::SignatureKeyPair;
use openmls_rust_crypto::OpenMlsRustCrypto;

const CIPHERSUITE: Ciphersuite =
    Ciphersuite::MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519;

/// One member's local cryptographic identity and key store.
struct Member {
    provider: OpenMlsRustCrypto,
    signer: SignatureKeyPair,
    credential_with_key: CredentialWithKey,
}

impl Member {
    fn new(name: &str) -> Self {
        let provider = OpenMlsRustCrypto::default();
        let credential = BasicCredential::new(name.as_bytes().to_vec());
        let signer = SignatureKeyPair::new(CIPHERSUITE.signature_algorithm())
            .expect("signature keypair");
        signer.store(provider.storage()).expect("store signer");
        let credential_with_key = CredentialWithKey {
            credential: credential.into(),
            signature_key: signer.public().into(),
        };
        Self {
            provider,
            signer,
            credential_with_key,
        }
    }

    /// A public KeyPackage others use to add this member to a group.
    fn key_package(&self) -> KeyPackageBundle {
        KeyPackage::builder()
            .build(
                CIPHERSUITE,
                &self.provider,
                &self.signer,
                self.credential_with_key.clone(),
            )
            .expect("key package")
    }
}

/// Runs the full round-trip in-process and returns the plaintext Bob decrypted,
/// which the caller asserts equals what Alice sent. Any protocol error surfaces
/// as an Err string.
pub fn roundtrip(plaintext: &[u8]) -> Result<Vec<u8>, String> {
    let alice = Member::new("alice");
    let bob = Member::new("bob");

    // Bob publishes a KeyPackage; Alice claims it to add him.
    let bob_kp = bob.key_package();

    // Alice creates the group. The ratchet-tree extension makes the Welcome
    // self-contained, so a joiner needs nothing beyond it — the right shape when
    // the server is an untrusted relay that stores no group state.
    let config = MlsGroupCreateConfig::builder()
        .use_ratchet_tree_extension(true)
        .build();
    let mut alice_group = MlsGroup::new(
        &alice.provider,
        &alice.signer,
        &config,
        alice.credential_with_key.clone(),
    )
    .map_err(|e| format!("create group: {e:?}"))?;

    // Alice adds Bob → a Commit for the group and a Welcome for Bob.
    let (_commit, welcome, _group_info) = alice_group
        .add_members(
            &alice.provider,
            &alice.signer,
            &[bob_kp.key_package().clone()],
        )
        .map_err(|e| format!("add member: {e:?}"))?;
    alice_group
        .merge_pending_commit(&alice.provider)
        .map_err(|e| format!("merge commit: {e:?}"))?;

    // Bob joins from the Welcome. It reaches him as opaque wire bytes via the
    // server, so serialize and re-parse it exactly as he would receive it.
    let welcome_wire = welcome.to_bytes().map_err(|e| format!("welcome bytes: {e:?}"))?;
    let welcome_in = MlsMessageIn::tls_deserialize(&mut welcome_wire.as_slice())
        .map_err(|e| format!("welcome parse: {e:?}"))?;
    let welcome = match welcome_in.extract() {
        MlsMessageBodyIn::Welcome(w) => w,
        _ => return Err("expected a Welcome message".into()),
    };
    let staged = StagedWelcome::new_from_welcome(
        &bob.provider,
        &MlsGroupJoinConfig::default(),
        welcome,
        None,
    )
    .map_err(|e| format!("stage welcome: {e:?}"))?;
    let mut bob_group = staged
        .into_group(&bob.provider)
        .map_err(|e| format!("join group: {e:?}"))?;

    // Alice encrypts an application message.
    let out = alice_group
        .create_message(&alice.provider, &alice.signer, plaintext)
        .map_err(|e| format!("encrypt: {e:?}"))?;

    // It travels as opaque bytes through the (untrusted) server.
    let wire = out
        .to_bytes()
        .map_err(|e| format!("serialize: {e:?}"))?;
    let incoming = MlsMessageIn::tls_deserialize(&mut wire.as_slice())
        .map_err(|e| format!("deserialize: {e:?}"))?;
    let protocol = incoming
        .try_into_protocol_message()
        .map_err(|e| format!("not a protocol message: {e:?}"))?;

    // Bob decrypts.
    let processed = bob_group
        .process_message(&bob.provider, protocol)
        .map_err(|e| format!("process: {e:?}"))?;
    match processed.into_content() {
        ProcessedMessageContent::ApplicationMessage(app) => Ok(app.into_bytes()),
        other => Err(format!("unexpected content: {other:?}")),
    }
}

// --- WASM surface (the shape the React client would call) --------------------

#[cfg(target_arch = "wasm32")]
mod wasm {
    use wasm_bindgen::prelude::*;

    /// Proves the crypto works from JS: encrypt→relay→decrypt, returning the
    /// decrypted text. A real client would split this into keygen / group / send
    /// / receive, but the spike only needs the round-trip to succeed in a browser.
    #[wasm_bindgen]
    pub fn roundtrip(plaintext: &str) -> Result<String, JsError> {
        let out = super::roundtrip(plaintext.as_bytes())
            .map_err(|e| JsError::new(&e))?;
        String::from_utf8(out).map_err(|e| JsError::new(&e.to_string()))
    }
}

#[cfg(all(test, not(target_arch = "wasm32")))]
mod tests {
    use super::*;

    #[test]
    fn two_party_message_round_trips() {
        let secret = b"the eagle lands at dawn";
        let got = roundtrip(secret).expect("round-trip");
        assert_eq!(got, secret, "Bob must decrypt exactly what Alice sent");
    }
}
