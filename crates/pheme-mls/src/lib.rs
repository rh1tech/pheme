//! Pheme MLS client core (RFC 9420, via OpenMLS).
//!
//! A stateful client that a browser (WASM) or Flutter app drives to run
//! end-to-end-encrypted conversations. The server never appears here: in MLS it
//! is an untrusted Delivery Service that stores public KeyPackages and relays the
//! opaque bytes these methods produce, in order.
//!
//! The whole client state — the identity keypair and every group's ratchet state
//! — lives in one serializable provider, so it can be exported to bytes,
//! persisted (IndexedDB on web, secure storage on mobile), and re-imported on the
//! next launch. Private keys never leave the device.

use openmls::prelude::tls_codec::{Deserialize as _, Serialize as _};
use openmls::prelude::*;
use openmls_basic_credential::SignatureKeyPair;
use openmls_rust_crypto::OpenMlsRustCrypto;
use sha2::{Digest, Sha256};

const CIPHERSUITE: Ciphersuite = Ciphersuite::MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519;

/// A Pheme MLS client: one identity plus every group it belongs to, all held in a
/// single serializable provider.
pub struct Client {
    provider: OpenMlsRustCrypto,
    signer: SignatureKeyPair,
    credential_with_key: CredentialWithKey,
    /// The identity bytes embedded in the credential, kept so state can be exported.
    identity: Vec<u8>,
}

/// The result of adding members to a group: the Welcome for the newcomers and the
/// Commit the existing members must apply. Both are opaque wire bytes.
pub struct AddResult {
    pub welcome: Vec<u8>,
    pub commit: Vec<u8>,
}

impl Client {
    /// Creates a fresh client identity. `identity` is the stable user/device label
    /// embedded in the MLS credential (Pheme uses the user id).
    pub fn new(identity: &[u8]) -> Result<Self, String> {
        let provider = OpenMlsRustCrypto::default();
        let signer =
            SignatureKeyPair::new(CIPHERSUITE.signature_algorithm()).map_err(err("signer"))?;
        signer.store(provider.storage()).map_err(err("store signer"))?;
        let credential = BasicCredential::new(identity.to_vec());
        let credential_with_key = CredentialWithKey {
            credential: credential.into(),
            signature_key: signer.public().into(),
        };
        Ok(Self {
            provider,
            signer,
            credential_with_key,
            identity: identity.to_vec(),
        })
    }

    /// A public KeyPackage to publish to the server, for others to add this client
    /// to a group. Each is single-use; publish several.
    pub fn key_package(&self) -> Result<Vec<u8>, String> {
        let bundle = KeyPackage::builder()
            .build(CIPHERSUITE, &self.provider, &self.signer, self.credential_with_key.clone())
            .map_err(err("key package"))?;
        bundle.key_package().tls_serialize_detached().map_err(err("serialize kp"))
    }

    /// Creates a new group this client owns. `group_id` is the conversation id.
    pub fn create_group(&self, group_id: &[u8]) -> Result<(), String> {
        let config = MlsGroupCreateConfig::builder()
            // Self-contained Welcomes: a joiner needs nothing the server would have
            // to store beyond the Welcome itself.
            .use_ratchet_tree_extension(true)
            .build();
        MlsGroup::new_with_group_id(
            &self.provider,
            &self.signer,
            &config,
            GroupId::from_slice(group_id),
            self.credential_with_key.clone(),
        )
        .map_err(err("create group"))?;
        Ok(())
    }

    fn load_group(&self, group_id: &[u8]) -> Result<MlsGroup, String> {
        MlsGroup::load(self.provider.storage(), &GroupId::from_slice(group_id))
            .map_err(err("load group"))?
            .ok_or_else(|| "group not found".to_string())
    }

    /// Whether this client already holds the group — without mutating any ratchet
    /// state. (Encrypting a probe message would advance the ratchet and desync the
    /// peer, so callers must use this instead.)
    pub fn has_group(&self, group_id: &[u8]) -> bool {
        matches!(
            MlsGroup::load(self.provider.storage(), &GroupId::from_slice(group_id)),
            Ok(Some(_))
        )
    }

    /// Adds a member (by their published KeyPackage) to a group we belong to.
    pub fn add_member(&self, group_id: &[u8], key_package: &[u8]) -> Result<AddResult, String> {
        self.add_members(group_id, std::slice::from_ref(&key_package.to_vec()))
    }

    /// Adds several members in a SINGLE Commit, so every newcomer joins at the same
    /// epoch. Adding them one at a time instead advances the epoch per add, leaving
    /// earlier joiners a Commit behind and unable to decrypt (WrongEpoch) — the
    /// reason initial group members must be batched here. Returns one Welcome
    /// addressed to all of them.
    pub fn add_members(&self, group_id: &[u8], key_packages: &[Vec<u8>]) -> Result<AddResult, String> {
        let mut validated = Vec::with_capacity(key_packages.len());
        for kp_bytes in key_packages {
            let kp_in =
                KeyPackageIn::tls_deserialize(&mut &**kp_bytes).map_err(err("parse kp"))?;
            let kp = kp_in
                .validate(self.provider.crypto(), ProtocolVersion::Mls10)
                .map_err(err("validate kp"))?;
            validated.push(kp);
        }

        let mut group = self.load_group(group_id)?;
        let (commit, welcome, _group_info) = group
            .add_members(&self.provider, &self.signer, &validated)
            .map_err(err("add members"))?;
        group.merge_pending_commit(&self.provider).map_err(err("merge commit"))?;

        Ok(AddResult {
            welcome: welcome.tls_serialize_detached().map_err(err("serialize welcome"))?,
            commit: commit.tls_serialize_detached().map_err(err("serialize commit"))?,
        })
    }

    /// Joins a group from a Welcome relayed by the server.
    ///
    /// Refuses to join a group we are already in. The Delivery Service is untrusted
    /// and nothing stops it replaying an old Welcome; joining again would hand our
    /// live group state to `into_group`, which stores unconditionally by group id and
    /// would roll the ratchet back to the stale epoch the Welcome carries —
    /// resurrecting secrets that should be forward-secret and desyncing us from the
    /// group. The caller guards this too, but the crypto core must not depend on it.
    pub fn join_from_welcome(&self, welcome: &[u8]) -> Result<(), String> {
        let msg = MlsMessageIn::tls_deserialize(&mut &*welcome).map_err(err("parse welcome"))?;
        let welcome = match msg.extract() {
            MlsMessageBodyIn::Welcome(w) => w,
            _ => return Err("not a Welcome".into()),
        };
        let staged =
            StagedWelcome::new_from_welcome(&self.provider, &MlsGroupJoinConfig::default(), welcome, None)
                .map_err(err("stage welcome"))?;
        if self.has_group(staged.group_context().group_id().as_slice()) {
            return Err("already a member of this group".into());
        }
        staged.into_group(&self.provider).map_err(err("join group"))?;
        Ok(())
    }

    /// Applies a Commit (membership change) another member produced. Idempotent
    /// enough to skip our own already-merged commits.
    pub fn apply_commit(&self, group_id: &[u8], commit: &[u8]) -> Result<(), String> {
        let mut group = self.load_group(group_id)?;
        let msg = MlsMessageIn::tls_deserialize(&mut &*commit).map_err(err("parse commit"))?;
        let protocol = msg.try_into_protocol_message().map_err(err("not protocol"))?;
        let processed = match group.process_message(&self.provider, protocol) {
            Ok(p) => p,
            // Our own commit, already merged when we produced it — nothing to do.
            Err(ProcessMessageError::ValidationError(ValidationError::CannotDecryptOwnMessage)) => {
                return Ok(())
            }
            Err(e) => return Err(format!("process commit: {e:?}")),
        };
        if let ProcessedMessageContent::StagedCommitMessage(staged) = processed.into_content() {
            group.merge_staged_commit(&self.provider, *staged).map_err(err("merge staged"))?;
        }
        Ok(())
    }

    /// The signature public keys of every member of a group, sorted.
    ///
    /// These come from the group's own ratchet tree — the state MLS itself
    /// authenticates — not from whatever the server most recently claimed. That is
    /// what makes them usable as the basis of a safety number: if the Delivery
    /// Service substituted its own KeyPackage for a peer's when the group was
    /// formed, the key here is the impostor's, and the safety number the two humans
    /// compare will not match.
    pub fn member_keys(&self, group_id: &[u8]) -> Result<Vec<Vec<u8>>, String> {
        let group = self.load_group(group_id)?;
        let mut keys: Vec<Vec<u8>> = group
            .members()
            .map(|m| m.signature_key.as_slice().to_vec())
            .collect();
        keys.sort();
        Ok(keys)
    }

    /// This client's own signature public key — its long-term MLS identity.
    pub fn identity_key(&self) -> Vec<u8> {
        self.signer.public().to_vec()
    }

    /// Encrypts an application message for a group.
    pub fn encrypt(&self, group_id: &[u8], plaintext: &[u8]) -> Result<Vec<u8>, String> {
        let mut group = self.load_group(group_id)?;
        let out = group
            .create_message(&self.provider, &self.signer, plaintext)
            .map_err(err("encrypt"))?;
        out.tls_serialize_detached().map_err(err("serialize msg"))
    }

    /// Decrypts an application message. Returns None for a control message
    /// (Commit) — the caller routes those to apply_commit.
    pub fn decrypt(&self, group_id: &[u8], ciphertext: &[u8]) -> Result<Option<Vec<u8>>, String> {
        let mut group = self.load_group(group_id)?;
        let msg = MlsMessageIn::tls_deserialize(&mut &*ciphertext).map_err(err("parse msg"))?;
        let protocol = msg.try_into_protocol_message().map_err(err("not protocol"))?;
        let processed = group.process_message(&self.provider, protocol).map_err(err("decrypt"))?;
        match processed.into_content() {
            ProcessedMessageContent::ApplicationMessage(app) => Ok(Some(app.into_bytes())),
            ProcessedMessageContent::StagedCommitMessage(staged) => {
                group.merge_staged_commit(&self.provider, *staged).map_err(err("merge staged"))?;
                Ok(None)
            }
            _ => Ok(None),
        }
    }

    /// Serialises the entire client state — the MLS key store (identity keypair +
    /// every group's ratchet state) plus the public key needed to reload the
    /// signer — into one blob for persistence (IndexedDB / secure storage).
    pub fn export_state(&self) -> Result<Vec<u8>, String> {
        // The MemoryStorage `serialize` helper is behind a test-utils feature, so
        // persist its public `values` map directly instead — a flat list of
        // (key, value) byte pairs, which is exactly what the store is.
        let values = self.provider.storage().values.read().map_err(err("read store"))?;
        let store: Vec<(Vec<u8>, Vec<u8>)> = values.iter().map(|(k, v)| (k.clone(), v.clone())).collect();
        let envelope = Envelope {
            store,
            public_key: self.signer.public().to_vec(),
            identity: self.identity.clone(),
        };
        serde_json::to_vec(&envelope).map_err(err("encode envelope"))
    }

    /// Restores a client from previously exported state.
    pub fn import_state(state: &[u8]) -> Result<Self, String> {
        let envelope: Envelope = serde_json::from_slice(state).map_err(err("decode envelope"))?;
        let provider = OpenMlsRustCrypto::default();
        {
            let mut dst = provider.storage().values.write().map_err(err("write store"))?;
            for (k, v) in envelope.store {
                dst.insert(k, v);
            }
        }
        let signer = SignatureKeyPair::read(
            provider.storage(),
            &envelope.public_key,
            CIPHERSUITE.signature_algorithm(),
        )
        .ok_or("signer missing from state")?;
        let credential = BasicCredential::new(envelope.identity.clone());
        let credential_with_key = CredentialWithKey {
            credential: credential.into(),
            signature_key: signer.public().into(),
        };
        Ok(Self {
            provider,
            signer,
            credential_with_key,
            identity: envelope.identity,
        })
    }
}

#[derive(serde::Serialize, serde::Deserialize)]
struct Envelope {
    store: Vec<(Vec<u8>, Vec<u8>)>,
    public_key: Vec<u8>,
    identity: Vec<u8>,
}

/// Formats an error with a context prefix. A plain fn (not `impl Fn`) so it can be
/// passed to `map_err` without nested-impl-Trait.
fn err<E: std::fmt::Debug>(context: &'static str) -> impl Fn(E) -> String {
    move |e| format!("{context}: {e:?}")
}

/// Encrypted key backup (passphrase-derived, server-stored ciphertext).
pub mod backup;

/// Turns a group's member signature keys into a safety number: the digits two
/// people read to each other to prove no one is in the middle.
///
/// The server is untrusted, and it is the server that hands out the KeyPackages a
/// group is built from. Nothing in the protocol stops it handing out its own key
/// under a victim's name and quietly joining every new conversation. The only thing
/// that catches that is the two humans comparing something derived from the keys
/// actually in the group — out of band, where the server cannot reach. If the
/// numbers differ, someone is in the middle.
///
/// Derived by hashing the sorted keys, then rendering the digest as decimal groups
/// (the Signal convention: digits are easy to read aloud and to compare over a
/// phone call, and carry no encoding traps the way hex or base64 do).
pub fn safety_number(member_keys: &[Vec<u8>]) -> String {
    let mut sorted = member_keys.to_vec();
    sorted.sort(); // order must not depend on who is asking
    let mut hasher = Sha256::new();
    // Length-prefix each key so two different key sets cannot hash to the same
    // input by running into each other at the boundary.
    for key in &sorted {
        hasher.update((key.len() as u32).to_be_bytes());
        hasher.update(key);
    }
    let first = hasher.finalize();

    // 12 groups of 5 digits needs 60 bytes of digest; one SHA-256 gives 32. Extend
    // with a second round so every group is derived from a full 5 bytes — a short
    // final group would carry visibly less entropy than the rest.
    let second = Sha256::digest(first);
    let mut material = Vec::with_capacity(64);
    material.extend_from_slice(&first);
    material.extend_from_slice(&second);

    material
        .chunks_exact(5)
        .take(12)
        .map(|chunk| {
            let n = chunk.iter().fold(0u64, |acc, &b| (acc << 8) | u64::from(b));
            format!("{:05}", n % 100_000)
        })
        .collect::<Vec<_>>()
        .join(" ")
}

// --- WASM surface -----------------------------------------------------------

#[cfg(target_arch = "wasm32")]
mod wasm;

#[cfg(all(test, not(target_arch = "wasm32")))]
mod tests {
    use super::*;

    #[test]
    fn stateful_two_party_round_trip() {
        let alice = Client::new(b"alice").unwrap();
        let bob = Client::new(b"bob").unwrap();
        let group_id = b"conversation-1";

        let bob_kp = bob.key_package().unwrap();
        alice.create_group(group_id).unwrap();
        let add = alice.add_member(group_id, &bob_kp).unwrap();
        bob.join_from_welcome(&add.welcome).unwrap();

        let ct = alice.encrypt(group_id, b"the eagle lands at dawn").unwrap();
        let pt = bob.decrypt(group_id, &ct).unwrap();
        assert_eq!(pt.as_deref(), Some(&b"the eagle lands at dawn"[..]));

        // Reply the other way.
        let ct2 = bob.encrypt(group_id, b"acknowledged").unwrap();
        let pt2 = alice.decrypt(group_id, &ct2).unwrap();
        assert_eq!(pt2.as_deref(), Some(&b"acknowledged"[..]));
    }

    // The property IndexedDB persistence rests on: a client's whole state survives
    // export → drop → import, and the restored client can still decrypt.
    #[test]
    fn state_survives_persistence_round_trip() {
        let alice = Client::new(b"alice").unwrap();
        let bob = Client::new(b"bob").unwrap();
        let group_id = b"conversation-1";

        alice.create_group(group_id).unwrap();
        let add = alice.add_member(group_id, &bob.key_package().unwrap()).unwrap();
        bob.join_from_welcome(&add.welcome).unwrap();

        // Persist Bob, then rebuild him from the blob (a page reload / new session).
        let saved = bob.export_state().unwrap();
        drop(bob);
        let bob = Client::import_state(&saved).unwrap();

        // Alice sends; the restored Bob decrypts.
        let ct = alice.encrypt(group_id, b"still works after reload").unwrap();
        assert_eq!(
            bob.decrypt(group_id, &ct).unwrap().as_deref(),
            Some(&b"still works after reload"[..]),
        );

        // And the restored Bob can still send back (his ratchet state is intact).
        let ct2 = bob.encrypt(group_id, b"reply from restored bob").unwrap();
        assert_eq!(
            alice.decrypt(group_id, &ct2).unwrap().as_deref(),
            Some(&b"reply from restored bob"[..]),
        );
    }

    // A group of three: both members added in ONE Commit must land at the same
    // epoch, so each can decrypt the owner's first message. Adding them one at a
    // time would leave the earlier joiner an epoch behind (WrongEpoch).
    #[test]
    fn group_of_three_batched_add() {
        let owner = Client::new(b"owner").unwrap();
        let bob = Client::new(b"bob").unwrap();
        let carol = Client::new(b"carol").unwrap();
        let group_id = b"group-1";

        owner.create_group(group_id).unwrap();
        let add = owner
            .add_members(group_id, &[bob.key_package().unwrap(), carol.key_package().unwrap()])
            .unwrap();
        bob.join_from_welcome(&add.welcome).unwrap();
        carol.join_from_welcome(&add.welcome).unwrap();

        let ct = owner.encrypt(group_id, b"hi team").unwrap();
        assert_eq!(bob.decrypt(group_id, &ct).unwrap().as_deref(), Some(&b"hi team"[..]));
        assert_eq!(carol.decrypt(group_id, &ct).unwrap().as_deref(), Some(&b"hi team"[..]));

        // A member speaks; the owner and the other member both read it.
        let ct2 = bob.encrypt(group_id, b"from bob").unwrap();
        assert_eq!(owner.decrypt(group_id, &ct2).unwrap().as_deref(), Some(&b"from bob"[..]));
        assert_eq!(carol.decrypt(group_id, &ct2).unwrap().as_deref(), Some(&b"from bob"[..]));
    }

    // The untrusted Delivery Service can replay an old Welcome. Re-joining would
    // roll our ratchet back to the Welcome's stale epoch, so it must be refused —
    // and the live group state must survive the attempt intact.
    #[test]
    fn replayed_welcome_is_refused_and_state_survives() {
        let alice = Client::new(b"alice").unwrap();
        let bob = Client::new(b"bob").unwrap();
        let group_id = b"conversation-1";

        alice.create_group(group_id).unwrap();
        let add = alice.add_member(group_id, &bob.key_package().unwrap()).unwrap();
        bob.join_from_welcome(&add.welcome).unwrap();

        // Bob is in the group and exchanging messages.
        let ct = alice.encrypt(group_id, b"before the replay").unwrap();
        assert_eq!(bob.decrypt(group_id, &ct).unwrap().as_deref(), Some(&b"before the replay"[..]));

        // The server replays the original Welcome. Bob must refuse it.
        assert!(bob.join_from_welcome(&add.welcome).is_err());

        // And his group state is untouched: he still decrypts the next message.
        let ct2 = alice.encrypt(group_id, b"after the replay").unwrap();
        assert_eq!(bob.decrypt(group_id, &ct2).unwrap().as_deref(), Some(&b"after the replay"[..]));
    }

    // The safety number is what catches a malicious Delivery Service.
    //
    // Both honest members derive it from the group's own ratchet tree, so they agree.
    // If the server had substituted its own KeyPackage for Bob's, the group would
    // contain the impostor's key instead — and the number Alice reads out would not
    // match the one Bob sees, which is exactly the signal the two humans need.
    #[test]
    fn safety_number_agrees_between_members_and_changes_under_substitution() {
        let alice = Client::new(b"alice").unwrap();
        let bob = Client::new(b"bob").unwrap();
        let group_id = b"conversation-1";

        alice.create_group(group_id).unwrap();
        let add = alice.add_member(group_id, &bob.key_package().unwrap()).unwrap();
        bob.join_from_welcome(&add.welcome).unwrap();

        // Both members compute the same number from their own view of the group.
        let from_alice = safety_number(&alice.member_keys(group_id).unwrap());
        let from_bob = safety_number(&bob.member_keys(group_id).unwrap());
        assert_eq!(from_alice, from_bob);

        // 12 groups of 5 digits — every group carries a full 5 bytes of digest.
        let groups: Vec<&str> = from_alice.split(' ').collect();
        assert_eq!(groups.len(), 12);
        assert!(groups.iter().all(|g| g.len() == 5 && g.chars().all(|c| c.is_ascii_digit())));

        // Now the same conversation, but the server hands Alice an impostor's
        // KeyPackage in place of Bob's. Alice's group holds the impostor's key, so her
        // safety number differs from the one she and Bob would have shared.
        let mallory = Client::new(b"bob").unwrap(); // claims to be Bob
        let alice2 = Client::new(b"alice").unwrap();
        let group2 = b"conversation-2";
        alice2.create_group(group2).unwrap();
        alice2.add_member(group2, &mallory.key_package().unwrap()).unwrap();
        let under_mitm = safety_number(&alice2.member_keys(group2).unwrap());

        assert_ne!(
            under_mitm, from_alice,
            "a substituted KeyPackage must change the safety number, or it detects nothing",
        );
    }

    // A third client not in the group cannot decrypt — the whole point.
    #[test]
    fn outsider_cannot_decrypt() {
        let alice = Client::new(b"alice").unwrap();
        let bob = Client::new(b"bob").unwrap();
        let mallory = Client::new(b"mallory").unwrap();
        let group_id = b"conversation-1";

        alice.create_group(group_id).unwrap();
        let add = alice.add_member(group_id, &bob.key_package().unwrap()).unwrap();
        bob.join_from_welcome(&add.welcome).unwrap();

        let ct = alice.encrypt(group_id, b"secret").unwrap();
        // Mallory has no such group; decryption must fail, not return plaintext.
        assert!(mallory.decrypt(group_id, &ct).is_err());
    }
}
