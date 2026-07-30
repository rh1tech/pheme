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
use openmls_traits::signatures::Signer as _;
use sha2::{Digest, Sha256};

const CIPHERSUITE: Ciphersuite = Ciphersuite::MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519;

/// How many past epochs' secrets a group keeps.
///
/// OpenMLS defaults this to 0: the moment a Commit is applied, every message from
/// the previous epoch becomes undecryptable forever. In a chat that is not a corner
/// case — it is what happens every time somebody joins or leaves, and it turns all
/// the history a client has not already decrypted into blanks. Application messages
/// also arrive out of order (an SSE Commit overtaking a page of older messages, a
/// user scrolling back), and every one of those decrypts against an older epoch.
///
/// Keeping a window of past epoch secrets is the RFC 9420 answer. The cost is
/// bounded and explicit: forward secrecy is weakened by exactly this window — a
/// device compromised now also exposes messages from its last 32 epochs.
const MAX_PAST_EPOCHS: usize = 32;

/// The bytes Pheme puts in an MLS credential: a domain-qualified device identifier,
/// `mimi://<domain>/d/<user>/<device>`.
///
/// **Domain-qualified**, so a member is named by which host they belong to. Without
/// the domain, alice on one host and alice on another would carry the same credential
/// and a federated group could not tell them apart — the whole reason cross-host
/// membership needs this. The form is the MIMI working group's device identifier, and
/// its user half is itself a `mimi://<domain>/u/<user>` (see `user_of`).
///
/// **An MLS leaf is a device, not a person.** Two devices of the same user are two
/// independent clients holding different private keys; each must occupy its own leaf
/// or it simply cannot decrypt the group's messages. Carrying the device id here also
/// gives removal something to work with: with a bare user, two leaves of one person
/// are indistinguishable, and removing "them" takes out whichever leaf was found first
/// while the other keeps on reading.
///
/// user and device are opaque host-local ids (hex Mongo ids, UUIDs) that contain no
/// `/`, so the four path segments are unambiguous.
pub fn identity(domain: &str, user_id: &str, device_id: &str) -> Vec<u8> {
    format!("mimi://{domain}/d/{user_id}/{device_id}").into_bytes()
}

/// Rejects an identifier component that would make a credential ambiguous.
///
/// `identity` builds a four-segment path and `user_of` reads it back by splitting
/// on `/`. That round trip is only sound while no component contains the
/// separator — and until this check existed, the guarantee was a comment. A user
/// id of `victim/junk` produced a credential that `user_of` resolved to
/// `mimi://<domain>/u/victim`: a DIFFERENT user than the credential names, which
/// is the identity that removal and roster comparison key on.
///
/// Not reachable today, since ids are server-issued — but this is the cross-host
/// identity now (F5a), and the cost of enforcing it is three comparisons.
fn check_component(what: &str, value: &str) -> Result<(), String> {
    if value.is_empty() {
        return Err(format!("a {what} is required"));
    }
    if value.contains('/') {
        return Err(format!(
            "a {what} may not contain '/': it is a path segment of the credential, \
             and one that splits would name a different member than it appears to"
        ));
    }
    // Control characters and whitespace would survive the split but make a
    // credential unprintable and its equality surprising.
    if value.chars().any(|c| c.is_control() || c.is_whitespace()) {
        return Err(format!(
            "a {what} may not contain whitespace or control characters"
        ));
    }
    Ok(())
}

/// The qualified USER identifier a credential belongs to: `mimi://<domain>/u/<user>`.
///
/// This is the key devices of one person are grouped under — for removal, for the
/// roster — so it must be stable across a user's devices and distinct across hosts.
/// A credential that does not parse as the device form falls back to its whole self,
/// so nothing silently resolves to the empty user.
pub fn user_of(identity: &[u8]) -> Vec<u8> {
    if let Ok(s) = std::str::from_utf8(identity) {
        if let Some(rest) = s.strip_prefix("mimi://") {
            // rest = <domain>/d/<user>/<device>, and EXACTLY that: a plain split
            // rather than splitn, so a credential carrying extra segments (a
            // foreign client, or one built before `check_component` existed)
            // falls back to its whole self instead of resolving to a truncated
            // user that happens to name somebody else.
            let parts: Vec<&str> = rest.split('/').collect();
            if parts.len() == 4 && parts[1] == "d" && !parts[2].is_empty() {
                return format!("mimi://{}/u/{}", parts[0], parts[2]).into_bytes();
            }
        }
    }
    identity.to_vec()
}

/// Builds the qualified user identifier for removal targets and roster comparison,
/// matching what `user_of` returns for that user's credentials.
pub fn user_key(domain: &str, user_id: &str) -> String {
    format!("mimi://{domain}/u/{user_id}")
}

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

/// A decrypted application message, together with the identity **MLS itself authenticated**.
///
/// The whole point of this type is the `sender` field, and it exists because the thing it replaces
/// was a lie. `decrypt` used to return bare plaintext, so every client above it had exactly one
/// answer to "who wrote this?" — the `senderId` the SERVER put on the message envelope. The server
/// is the untrusted Delivery Service. It can put any user id it likes there, on a ciphertext it
/// relayed from anyone, and the receiving client would render it under that name with no way to
/// tell. End-to-end encryption without sender authentication buys confidentiality and nothing else:
/// nobody can read your conversation, and anybody who runs the server can write to it as you.
///
/// MLS already knows the answer. Every application message is signed by the sending leaf's
/// signature key, `process_message` verifies that signature against the leaf's key in the group's
/// own ratchet tree, and the credential it hands back is the authenticated one. It was simply being
/// dropped on the floor. This carries it out.
///
/// `sender` is the credential's identity bytes — `mimi://<domain>/d/<user>/<device>` — of the leaf
/// that actually signed the message. `pheme_mls::user_of` reduces it to the qualified user, which
/// is what a UI compares against a membership roster.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Decrypted {
    pub plaintext: Vec<u8>,
    /// The authenticated credential identity of the sending leaf. Never server-supplied.
    pub sender: Vec<u8>,
    /// The epoch the message was framed in — what a caller pins when it needs to know which
    /// membership the sender was authenticated against.
    pub epoch: u64,
}

impl Client {
    /// Creates a fresh client identity for ONE DEVICE of one user.
    ///
    /// Both halves are required, and that is deliberate: an MLS leaf is a device, so a
    /// client that knows only which user it belongs to cannot be told apart from that
    /// user's other devices, and the group ends up holding one of them at random.
    pub fn new(domain: &str, user_id: &str, device_id: &str) -> Result<Self, String> {
        check_component("domain", domain)?;
        check_component("user id", user_id)?;
        check_component("device id", device_id)?;
        let identity = identity(domain, user_id, device_id);
        let provider = OpenMlsRustCrypto::default();
        let signer =
            SignatureKeyPair::new(CIPHERSUITE.signature_algorithm()).map_err(err("signer"))?;
        signer
            .store(provider.storage())
            .map_err(err("store signer"))?;
        let credential = BasicCredential::new(identity.clone());
        let credential_with_key = CredentialWithKey {
            credential: credential.into(),
            signature_key: signer.public().into(),
        };
        Ok(Self {
            provider,
            signer,
            credential_with_key,
            identity,
        })
    }

    /// This client's credential identity — `userId:deviceId`.
    pub fn identity(&self) -> &[u8] {
        &self.identity
    }

    /// A public KeyPackage to publish to the server, for others to add this client
    /// to a group. Each is single-use; publish several.
    pub fn key_package(&self) -> Result<Vec<u8>, String> {
        self.build_key_package(false)
    }

    /// A *last-resort* KeyPackage: one the client may be added with more than once.
    ///
    /// Ordinary KeyPackages are single-use — OpenMLS deletes the private init key the
    /// first time one is used to join, so a second Welcome built from the same
    /// KeyPackage fails with NoMatchingKeyPackage. That makes a user's published stock
    /// exhaustible, and anyone can exhaust it: claim them all in a loop and the user
    /// can no longer be added to any group. The last-resort extension is RFC 9420's
    /// answer — OpenMLS keeps the private key when it is set, so this KeyPackage can be
    /// handed out indefinitely and the user can always be reached.
    ///
    /// The cost is real but small and bounded: joins that fall back to this one reuse
    /// an init key, weakening forward secrecy for those joins only. Being unreachable
    /// on a stranger's whim is the worse failure.
    ///
    /// The extension must be set HERE, when the KeyPackage is built. A server-side flag
    /// alone does nothing: the deletion happens on the client, and it keys off this
    /// extension.
    pub fn last_resort_key_package(&self) -> Result<Vec<u8>, String> {
        self.build_key_package(true)
    }

    fn build_key_package(&self, last_resort: bool) -> Result<Vec<u8>, String> {
        let mut builder = KeyPackage::builder();
        if last_resort {
            builder = builder
                .mark_as_last_resort()
                // A KeyPackage must declare that it supports every extension it
                // carries, or the member adding it rejects it as UnsupportedExtension.
                // Setting the extension without also advertising it produces a
                // KeyPackage nobody can use.
                .leaf_node_capabilities(Capabilities::new(
                    None,
                    None,
                    Some(&[ExtensionType::LastResort]),
                    None,
                    None,
                ));
        }
        let bundle = builder
            .build(
                CIPHERSUITE,
                &self.provider,
                &self.signer,
                self.credential_with_key.clone(),
            )
            .map_err(err("key package"))?;
        bundle
            .key_package()
            .tls_serialize_detached()
            .map_err(err("serialize kp"))
    }

    /// Creates a new group. `group_id` is the opaque id the server minted for the
    /// conversation — NOT the conversation id itself.
    ///
    /// That indirection is what makes group establishment safe. The server accepts a
    /// group id for a conversation exactly once, so two devices racing to set one up
    /// cannot both win, and a device that loses the race discards its group and joins
    /// the winner's instead. When the id was the conversation id, `create_group` on a
    /// second device silently produced a DIFFERENT group under the same name, and the
    /// two halves of the conversation encrypted past each other forever.
    pub fn create_group(&self, group_id: &[u8]) -> Result<(), String> {
        let config = MlsGroupCreateConfig::builder()
            // Self-contained Welcomes: a joiner needs nothing the server would have
            // to store beyond the Welcome itself.
            .use_ratchet_tree_extension(true)
            .max_past_epochs(MAX_PAST_EPOCHS)
            // Handshake messages (Commits and Proposals) go out as PublicMessage,
            // so the delivery service can read the epoch a Commit is built on and
            // stop serialising the group on a number the client merely claims.
            // Application messages are ALWAYS encrypted regardless of this — RFC
            // 9420 §6 requires it — so nothing a person types is exposed. See the
            // server side in api/internal/chat and docs/development/federation.md (F4).
            //
            // MIXED, not pure plaintext: outgoing handshakes are plaintext, but
            // incoming ones are accepted in either framing, so a client that has
            // not adopted this yet is still understood. That tolerance is what
            // lets the rollout happen without a flag day.
            .wire_format_policy(MIXED_PLAINTEXT_WIRE_FORMAT_POLICY)
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

    /// The config a joiner adopts. It must match the creator's in both respects.
    ///
    /// `max_past_epochs`, or a member who joined from a Welcome would lose old messages
    /// on the first Commit while the creator kept them.
    ///
    /// `use_ratchet_tree_extension`, or the Welcomes THIS member later produces carry no
    /// ratchet tree and nobody can join from them (MissingRatchetTree). That matters
    /// because adding a member is no longer the creator's privilege: whoever notices a
    /// device missing from the group is the one who adds it, and most of them will have
    /// joined from a Welcome themselves.
    fn join_config() -> MlsGroupJoinConfig {
        MlsGroupJoinConfig::builder()
            .use_ratchet_tree_extension(true)
            .max_past_epochs(MAX_PAST_EPOCHS)
            // Match the creator's handshake framing, or a joiner rejects the very
            // Commits it needs to stay in step. See create_group.
            .wire_format_policy(MIXED_PLAINTEXT_WIRE_FORMAT_POLICY)
            .build()
    }

    /// The group's current epoch. The server compares this against the conversation's
    /// recorded epoch to serialise Commits, so no two members can fork the group.
    pub fn epoch(&self, group_id: &[u8]) -> Result<u64, String> {
        Ok(self.load_group(group_id)?.epoch().as_u64())
    }

    /// Discards a group this client created but that the server never accepted.
    ///
    /// The only legitimate use: two of our devices raced to establish the conversation's
    /// group, the server took the other one's id, and the group we built locally is now
    /// an orphan nobody else will ever be a member of. Dropping it lets us join the real
    /// one instead.
    ///
    /// It must NEVER be used to "repair" a group that other people are already in. That
    /// was the old rejoin path, and it is what destroyed conversations: discarding a live
    /// group throws away the key material for every message ever sent to it, for
    /// everyone, and no amount of rebuilding brings those back. A member who cannot
    /// decrypt gets ADDED to the existing group (see `stage_add`); the group is not torn
    /// down around them.
    pub fn delete_group(&self, group_id: &[u8]) -> Result<(), String> {
        let mut group =
            match MlsGroup::load(self.provider.storage(), &GroupId::from_slice(group_id))
                .map_err(err("load group"))?
            {
                Some(g) => g,
                None => return Ok(()), // nothing to discard
            };
        group
            .delete(self.provider.storage())
            .map_err(err("delete group"))?;
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

    /// STAGES the addition of several devices in a SINGLE Commit, without applying it.
    ///
    /// Batched, because adding one at a time advances the epoch per add and leaves the
    /// earlier joiners a Commit behind, unable to decrypt (WrongEpoch). One Welcome
    /// covers every newcomer.
    ///
    /// Nothing is merged here. The Commit is only real once the SERVER has accepted it
    /// — see `commit_accepted` / `commit_rejected`. This used to call
    /// `merge_pending_commit` immediately, which is wrong for the same reason writing to
    /// a database before the transaction commits is wrong: two members can propose a
    /// Commit against the same epoch, only one of them can win, and a client that has
    /// already advanced its own ratchet on a Commit the group never accepted is forked
    /// from everyone else — permanently, and silently.
    pub fn stage_add(
        &self,
        group_id: &[u8],
        key_packages: &[Vec<u8>],
    ) -> Result<AddResult, String> {
        if key_packages.is_empty() {
            return Err("nothing to add".into());
        }
        let mut validated = Vec::with_capacity(key_packages.len());
        for kp_bytes in key_packages {
            let kp_in = KeyPackageIn::tls_deserialize(&mut &**kp_bytes).map_err(err("parse kp"))?;
            let kp = kp_in
                .validate(self.provider.crypto(), ProtocolVersion::Mls10)
                .map_err(err("validate kp"))?;
            validated.push(kp);
        }

        let mut group = self.load_group(group_id)?;
        let (commit, welcome, _group_info) = group
            .add_members(&self.provider, &self.signer, &validated)
            .map_err(err("add members"))?;

        Ok(AddResult {
            welcome: welcome
                .tls_serialize_detached()
                .map_err(err("serialize welcome"))?,
            commit: commit
                .tls_serialize_detached()
                .map_err(err("serialize commit"))?,
        })
    }

    /// STAGES the removal of EVERY leaf belonging to each of `user_ids`, in one Commit.
    ///
    /// Every leaf, not the first one found. A person with a phone and a laptop holds two
    /// leaves; removing one and leaving the other means the member you just threw out of
    /// the group carries on reading it. That is a confidentiality failure, not an
    /// inconvenience, and it is only detectable at all because the device id is in the
    /// credential.
    ///
    /// Users who hold no leaf are ignored rather than refused: a member reconciling the
    /// group against the conversation's membership will routinely name someone who has
    /// already been pruned by another member.
    ///
    /// This client's own leaves are never removed, whatever is asked. MLS forbids
    /// committing your own removal (`CannotRemoveSelf`), so leaving a group is not a
    /// Commit at all — a member drops their server-side membership and destroys their
    /// local group state, and the members who remain prune the leaves they left behind
    /// the next time they reconcile.
    ///
    /// Not merged — the server decides. See `stage_add`.
    pub fn stage_remove_users(
        &self,
        group_id: &[u8],
        user_ids: &[String],
    ) -> Result<Vec<u8>, String> {
        let mut group = self.load_group(group_id)?;
        let me = user_of(&self.identity);
        let targets: Vec<Vec<u8>> = user_ids
            .iter()
            .map(|u| u.as_bytes().to_vec())
            .filter(|u| *u != me)
            .collect();
        if targets.is_empty() {
            return Err("nothing to remove".to_string());
        }
        let leaves: Vec<LeafNodeIndex> = group
            .members()
            .filter(|m| targets.contains(&user_of(m.credential.serialized_content())))
            .map(|m| m.index)
            .collect();
        if leaves.is_empty() {
            return Err("none of those users are in the group".to_string());
        }
        let (commit, _welcome, _group_info) = group
            .remove_members(&self.provider, &self.signer, &leaves)
            .map_err(err("remove members"))?;
        commit
            .tls_serialize_detached()
            .map_err(err("serialize commit"))
    }

    /// STAGES the removal of specific LEAVES, named by their full `userId:deviceId`.
    ///
    /// Distinct from `stage_remove_users`, and the difference matters: that one removes
    /// every device a person has, which is what you want when you throw them out of a
    /// group. This one removes exactly the leaves you name, which is what you want when a
    /// person is staying but one of their devices is a ghost — a browser whose storage was
    /// evicted, say, leaving a leaf in the tree whose private keys no longer exist
    /// anywhere. Pruning that by user would take their live phone out with it.
    ///
    /// This client's own leaves are never removed. Not merged — the server decides.
    pub fn stage_remove_devices(
        &self,
        group_id: &[u8],
        identities: &[String],
    ) -> Result<Vec<u8>, String> {
        let mut group = self.load_group(group_id)?;
        let targets: Vec<Vec<u8>> = identities
            .iter()
            .map(|i| i.as_bytes().to_vec())
            .filter(|i| *i != self.identity)
            .collect();
        if targets.is_empty() {
            return Err("nothing to remove".to_string());
        }
        let leaves: Vec<LeafNodeIndex> = group
            .members()
            .filter(|m| targets.contains(&m.credential.serialized_content().to_vec()))
            .map(|m| m.index)
            .collect();
        if leaves.is_empty() {
            return Err("none of those devices are in the group".to_string());
        }
        let (commit, _welcome, _group_info) = group
            .remove_members(&self.provider, &self.signer, &leaves)
            .map_err(err("remove members"))?;
        commit
            .tls_serialize_detached()
            .map_err(err("serialize commit"))
    }

    /// Applies a Commit this client staged, now that the server has accepted it as the
    /// group's next epoch. Only now does our ratchet advance.
    pub fn commit_accepted(&self, group_id: &[u8]) -> Result<(), String> {
        let mut group = self.load_group(group_id)?;
        group
            .merge_pending_commit(&self.provider)
            .map_err(err("merge commit"))
    }

    /// Throws away a Commit the server refused (another member's landed first). The
    /// group is left exactly as it was, so the caller can catch up on the winning
    /// Commit and try again.
    pub fn commit_rejected(&self, group_id: &[u8]) -> Result<(), String> {
        let mut group = self.load_group(group_id)?;
        group
            .clear_pending_commit(self.provider.storage())
            .map_err(err("clear pending"))
    }

    /// The credential identity (`userId:deviceId`) of every leaf in the group.
    ///
    /// This is what lets a member work out which devices are MISSING from the group —
    /// diff it against the devices the server says the conversation's members have
    /// published — and add exactly those.
    pub fn member_identities(&self, group_id: &[u8]) -> Result<Vec<Vec<u8>>, String> {
        let group = self.load_group(group_id)?;
        Ok(group
            .members()
            .map(|m| m.credential.serialized_content().to_vec())
            .collect())
    }

    /// Joins a group from a Welcome relayed by the server.
    ///
    /// DANGER — processing a Welcome CONSUMES the KeyPackage it is addressed to, even
    /// when the Welcome turns out to be invalid. OpenMLS looks the private init key up
    /// by the Welcome's hash_ref and deletes it *before* it has verified anything about
    /// the Welcome (see `keys_for_welcome` upstream, and the
    /// `forged_welcome_burns_an_ordinary_key_package` test below, which pins this
    /// behaviour). The Delivery Service knows every hash_ref it hands out, so anyone
    /// able to inject a message can burn a victim's published KeyPackages by sending
    /// garbage Welcomes addressed to them.
    ///
    /// Two things contain that. A last-resort KeyPackage is never deleted, so the user
    /// always keeps one they can be added with (see `last_resort_key_package`). And the
    /// CALLER must not feed arbitrary Welcomes in — only ones from the party that
    /// legitimately created the conversation.
    ///
    /// Re-joining a group we already hold is refused. OpenMLS already rejects it during
    /// staging (`WelcomeError::GroupAlreadyExists`), so this check is belt-and-braces
    /// rather than the thing doing the work — kept because relying on an upstream
    /// internal to protect our ratchet state is not something to leave implicit.
    pub fn join_from_welcome(&self, welcome: &[u8]) -> Result<(), String> {
        let msg = MlsMessageIn::tls_deserialize(&mut &*welcome).map_err(err("parse welcome"))?;
        let welcome = match msg.extract() {
            MlsMessageBodyIn::Welcome(w) => w,
            _ => return Err("not a Welcome".into()),
        };
        let staged =
            StagedWelcome::new_from_welcome(&self.provider, &Self::join_config(), welcome, None)
                .map_err(err("stage welcome"))?;
        if self.has_group(staged.group_context().group_id().as_slice()) {
            return Err("already a member of this group".into());
        }
        staged
            .into_group(&self.provider)
            .map_err(err("join group"))?;
        Ok(())
    }

    /// Exports the GroupInfo a NON-MEMBER needs to join this group by external commit.
    ///
    /// `with_ratchet_tree = true` makes it self-contained — the joiner needs nothing else. It is a
    /// signed snapshot of the current epoch that carries the `external_pub` key an external commit
    /// initialises against. Nothing is written: GroupInfo is derived state, not part of the ratchet.
    pub fn export_group_info(&self, group_id: &[u8]) -> Result<Vec<u8>, String> {
        let group = self.load_group(group_id)?;
        let msg = group
            .export_group_info(self.provider.crypto(), &self.signer, true)
            .map_err(err("export group info"))?;
        msg.tls_serialize_detached()
            .map_err(err("serialize group info"))
    }

    /// Joins an existing group by EXTERNAL COMMIT: adds this client's own leaf, with no Welcome and
    /// no member's help, from the GroupInfo a member exported.
    ///
    /// This is the answer to "a new device opens a chat whose group already exists". The alternatives
    /// are to wait for a member to be online to admit it, or to tear the group down and rebuild it —
    /// the first strands the device when nobody is around, the second strands everyone else when they
    /// do not all migrate. An external commit does neither: it adds one leaf to the existing group.
    ///
    /// Returns the external commit to offer the server through the ordinary compare-and-set. The new
    /// group is created here with that commit PENDING — and UNLIKE a staged commit it cannot be
    /// cleared: on acceptance call [`Client::commit_accepted`] to merge it; on refusal (someone else
    /// committed first) call [`Client::delete_group`] and start over from fresh GroupInfo.
    pub fn join_by_external_commit(&self, group_info: &[u8]) -> Result<Vec<u8>, String> {
        let msg =
            MlsMessageIn::tls_deserialize(&mut &*group_info).map_err(err("parse group info"))?;
        let verifiable = match msg.extract() {
            MlsMessageBodyIn::GroupInfo(gi) => gi,
            _ => return Err("not a GroupInfo".into()),
        };
        #[allow(deprecated)]
        let (_group, commit, _group_info) = MlsGroup::join_by_external_commit(
            &self.provider,
            &self.signer,
            // None: the GroupInfo was exported with the ratchet tree, so it is self-contained.
            None,
            verifiable,
            &Self::join_config(),
            None,
            None,
            &[],
            self.credential_with_key.clone(),
        )
        .map_err(err("external join"))?;
        commit
            .tls_serialize_detached()
            .map_err(err("serialize external commit"))
    }

    /// Applies a Commit (membership change) another member produced. Idempotent
    /// enough to skip our own already-merged commits.
    pub fn apply_commit(&self, group_id: &[u8], commit: &[u8]) -> Result<(), String> {
        let mut group = self.load_group(group_id)?;
        let msg = MlsMessageIn::tls_deserialize(&mut &*commit).map_err(err("parse commit"))?;
        let protocol = msg
            .try_into_protocol_message()
            .map_err(err("not protocol"))?;
        let processed = match group.process_message(&self.provider, protocol) {
            Ok(p) => p,
            // Our own commit, already merged when we produced it — nothing to do.
            Err(ProcessMessageError::ValidationError(ValidationError::CannotDecryptOwnMessage)) => {
                return Ok(())
            }
            Err(e) => return Err(format!("process commit: {e:?}")),
        };
        if let ProcessedMessageContent::StagedCommitMessage(staged) = processed.into_content() {
            group
                .merge_staged_commit(&self.provider, *staged)
                .map_err(err("merge staged"))?;
        }
        Ok(())
    }

    /// Derives a secret from the group, for a purpose OUTSIDE MLS's own messaging.
    ///
    /// This is RFC 9420's exporter. It gives every member of the group — and nobody else —
    /// the same bytes for the same (label, context), without sending anything and without
    /// touching the ratchet. Pheme uses it to key the signalling of a voice call: the SDP
    /// carries the DTLS fingerprint that the media encryption is bound to, so encrypting
    /// the SDP under a key the server does not have is exactly what stops the server
    /// putting itself in the middle of a call. The safety number two people already compare
    /// for chat therefore covers their calls too.
    ///
    /// Why this and not an ordinary MLS application message, which would also be private:
    ///
    ///   * MLS decryption is ONE-SHOT — the message key is deleted on use — and this
    ///     client's state is shared across a browser's tabs behind a single lock. Whichever
    ///     tab won the lock would consume the call offer, and the tab the user is actually
    ///     looking at would fail to decrypt it. Chat papers over that with a plaintext
    ///     cache; a ringing phone cannot.
    ///   * An application message mutates the ratchet on encrypt AND decrypt, and every
    ///     mutation serialises the whole key store to disk. Signalling is chatty.
    ///
    /// This call is a pure read: it mutates neither the group nor the storage.
    ///
    /// **It exports from the CURRENT EPOCH.** A membership change moves the epoch and
    /// therefore the secret, so a caller must pin the epoch it derived at (send it, and
    /// derive once at the start of a call rather than per message) — see the call layer.
    ///
    /// **It gives GROUP authenticity, not SENDER authenticity.** Any member device can
    /// derive any other's key, so a group member could forge a signal attributed to another
    /// member. Between two people that is meaningless — the forger is either you or the
    /// person you are talking to. It would NOT be safe for group calls without also signing
    /// the payload against `member_keys`.
    pub fn export_secret(
        &self,
        group_id: &[u8],
        label: &str,
        context: &[u8],
        length: usize,
    ) -> Result<Vec<u8>, String> {
        let group = self.load_group(group_id)?;
        group
            .export_secret(self.provider.crypto(), label, context, length)
            .map_err(err("export secret"))
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
    ///
    /// The plaintext comes back with the **authenticated** sender credential; see [`Decrypted`] for
    /// why that is the whole point of the return type.
    pub fn decrypt(&self, group_id: &[u8], ciphertext: &[u8]) -> Result<Option<Decrypted>, String> {
        let mut group = self.load_group(group_id)?;
        self.open(&mut group, ciphertext, true)
    }

    /// Decrypts an application message WITHOUT merging anything.
    ///
    /// Same as [`Client::decrypt`] except that a Commit is refused rather than merged. It is
    /// the decrypt a notification preview runs, and the difference is the whole point: a
    /// preview must never move the epoch. See [`PreviewClient`], which is the only way to
    /// reach this and cannot persist.
    ///
    /// This still mutates the IN-MEMORY provider — `process_message` consumes the message
    /// key, and there is no way to ask OpenMLS not to. That is exactly why the caller must be
    /// unable to write the result back: the mutation has to die with the process.
    pub fn decrypt_no_merge(
        &self,
        group_id: &[u8],
        ciphertext: &[u8],
    ) -> Result<Option<Decrypted>, String> {
        let mut group = self.load_group(group_id)?;
        self.open(&mut group, ciphertext, false)
    }

    /// The shared body of both decrypts: process the message, keep what MLS authenticated about
    /// its sender, and refuse anything that is not an application message from a member.
    ///
    /// `merge_commits` is the only difference between the two callers. A preview must never move
    /// the epoch (see [`PreviewClient`]), so it passes false and a Commit is simply dropped.
    fn open(
        &self,
        group: &mut MlsGroup,
        ciphertext: &[u8],
        merge_commits: bool,
    ) -> Result<Option<Decrypted>, String> {
        let msg = MlsMessageIn::tls_deserialize(&mut &*ciphertext).map_err(err("parse msg"))?;
        let protocol = msg
            .try_into_protocol_message()
            .map_err(err("not protocol"))?;
        let processed = group
            .process_message(&self.provider, protocol)
            .map_err(err("decrypt"))?;

        // Read BEFORE into_content consumes the message. This is the authenticated triple:
        // process_message verified the content's signature against the leaf key in the ratchet
        // tree, and `credential` is the credential that key belongs to.
        let epoch = processed.epoch().as_u64();
        let sender_leaf = match processed.sender() {
            Sender::Member(index) => Some(*index),
            _ => None,
        };
        let credential = processed.credential().serialized_content().to_vec();

        match processed.into_content() {
            ProcessedMessageContent::ApplicationMessage(app) => {
                // RFC 9420 §6 only ever frames application messages as PrivateMessage from a
                // member, and OpenMLS enforces it — but "an upstream enforces it" is not the sort
                // of thing to leave implicit when the consequence is an unattributed message
                // rendered as though it were attributed. An external sender has no leaf, and so no
                // identity a UI could show; refuse rather than invent one.
                let leaf = sender_leaf
                    .ok_or("application message from a sender with no leaf in the group")?;
                // Belt and braces: the credential process_message authenticated must be the
                // credential the tree holds for that leaf. They are read from the same place today;
                // asserting it means a future divergence is a failed decrypt rather than a
                // misattributed message.
                let in_tree = group
                    .member(leaf)
                    .ok_or("the sending leaf is not in the group")?
                    .serialized_content()
                    .to_vec();
                if in_tree != credential {
                    return Err("the sender credential does not match the group's leaf".into());
                }
                Ok(Some(Decrypted {
                    plaintext: app.into_bytes(),
                    sender: credential,
                    epoch,
                }))
            }
            ProcessedMessageContent::StagedCommitMessage(staged) if merge_commits => {
                group
                    .merge_staged_commit(&self.provider, *staged)
                    .map_err(err("merge staged"))?;
                Ok(None)
            }
            // Deliberately NOT merged on the preview path. A preview that advanced the epoch in a
            // copy of the state it then threw away would leave the real client behind by a commit
            // it never saw.
            _ => Ok(None),
        }
    }

    // --- signed history handoff ---------------------------------------------------------------
    //
    // See `crate::history` for why the exporter secret alone is not enough, and what the canonical
    // transcript looks like. These four methods are the only way in: the transcript is built here,
    // from typed fields, so neither binding can assemble a different one.

    /// The leaf signature key the group's own ratchet tree holds for `identity`.
    ///
    /// From the tree, never from anything a message claims — that is the entire basis of the
    /// verification. An identity nobody holds a leaf under is refused rather than defaulted.
    fn member_signature_key(&self, group_id: &[u8], identity: &str) -> Result<Vec<u8>, String> {
        let group = self.load_group(group_id)?;
        let mut found: Option<Vec<u8>> = None;
        for member in group.members() {
            if member.credential.serialized_content() == identity.as_bytes() {
                if found.is_some() {
                    // Two leaves under one credential is not a state this code can resolve, and
                    // guessing would mean accepting a signature from whichever it happened to see
                    // first. Credentials carry a device id precisely so this cannot happen.
                    return Err("two leaves claim that identity".into());
                }
                found = Some(member.signature_key.as_slice().to_vec());
            }
        }
        found.ok_or_else(|| format!("{identity} is not a member of this group"))
    }

    fn verify_history(
        &self,
        group_id: &[u8],
        signer_identity: &str,
        transcript: &[u8],
        signature: &[u8],
    ) -> Result<(), String> {
        let key = self.member_signature_key(group_id, signer_identity)?;
        self.provider
            .crypto()
            .verify_signature(
                CIPHERSUITE.signature_algorithm(),
                transcript,
                &key,
                signature,
            )
            .map_err(err("history signature"))
    }

    /// Signs this device's request for a conversation's pre-join history.
    ///
    /// The requester is always *this* client — it signs with its own leaf key and the transcript
    /// names its own credential — so there is no way for a caller to sign a request on somebody
    /// else's behalf, by mistake or otherwise.
    pub fn sign_history_request(
        &self,
        group_id: &[u8],
        conversation_id: &str,
        epoch: u64,
        nonce: &[u8],
    ) -> Result<Vec<u8>, String> {
        if !self.has_group(group_id) {
            return Err("not a member of this group".into());
        }
        let identity = String::from_utf8_lossy(&self.identity).into_owned();
        let transcript =
            history::request_transcript(conversation_id, group_id, epoch, &identity, nonce);
        self.signer
            .sign(&transcript)
            .map_err(err("sign history request"))
    }

    /// Verifies a history request against the claimed requester's leaf key.
    ///
    /// A member that answers an unverified request seals its whole transcript to a key derived for
    /// an identity that never asked for it — which is how an insider makes a co-member hand over a
    /// conversation on somebody else's behalf.
    pub fn verify_history_request(
        &self,
        group_id: &[u8],
        conversation_id: &str,
        epoch: u64,
        requester: &str,
        nonce: &[u8],
        signature: &[u8],
    ) -> Result<(), String> {
        let transcript =
            history::request_transcript(conversation_id, group_id, epoch, requester, nonce);
        self.verify_history(group_id, requester, &transcript, signature)
    }

    /// Signs this device's offer of a sealed transcript to `requester`.
    ///
    /// The digest is computed HERE, over the ciphertext that will actually be uploaded, so the
    /// signature commits to the bytes rather than to the `history_id` pointing at them. Without
    /// that, the server — which stores the blob — could swap its contents behind an otherwise
    /// perfectly valid signature.
    #[allow(clippy::too_many_arguments)]
    pub fn sign_history_offer(
        &self,
        group_id: &[u8],
        conversation_id: &str,
        epoch: u64,
        requester: &str,
        history_id: &str,
        salt: &[u8],
        nonce: &[u8],
        request_nonce: &[u8],
        ciphertext: &[u8],
    ) -> Result<Vec<u8>, String> {
        if !self.has_group(group_id) {
            return Err("not a member of this group".into());
        }
        let identity = String::from_utf8_lossy(&self.identity).into_owned();
        let transcript = history::offer_transcript(
            conversation_id,
            group_id,
            epoch,
            &identity,
            requester,
            history_id,
            salt,
            nonce,
            request_nonce,
            &history::digest(ciphertext),
        );
        self.signer
            .sign(&transcript)
            .map_err(err("sign history offer"))
    }

    /// Verifies an offer against the claimed offerer's leaf key AND against the blob's own bytes.
    ///
    /// `requester` is bound to this client's own identity, not taken from the offer: an offer
    /// addressed to another device must not verify here, however well it is signed.
    #[allow(clippy::too_many_arguments)]
    pub fn verify_history_offer(
        &self,
        group_id: &[u8],
        conversation_id: &str,
        epoch: u64,
        offerer: &str,
        history_id: &str,
        salt: &[u8],
        nonce: &[u8],
        request_nonce: &[u8],
        ciphertext: &[u8],
        signature: &[u8],
    ) -> Result<(), String> {
        let requester = String::from_utf8_lossy(&self.identity).into_owned();
        let transcript = history::offer_transcript(
            conversation_id,
            group_id,
            epoch,
            offerer,
            &requester,
            history_id,
            salt,
            nonce,
            request_nonce,
            &history::digest(ciphertext),
        );
        self.verify_history(group_id, offerer, &transcript, signature)
    }

    /// Serialises the entire client state — the MLS key store (identity keypair +
    /// every group's ratchet state) plus the public key needed to reload the
    /// signer — into one blob for persistence (IndexedDB / secure storage).
    pub fn export_state(&self) -> Result<Vec<u8>, String> {
        // The MemoryStorage `serialize` helper is behind a test-utils feature, so
        // persist its public `values` map directly instead — a flat list of
        // (key, value) byte pairs, which is exactly what the store is.
        let values = self
            .provider
            .storage()
            .values
            .read()
            .map_err(err("read store"))?;
        let store: Vec<(Vec<u8>, Vec<u8>)> =
            values.iter().map(|(k, v)| (k.clone(), v.clone())).collect();
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
            let mut dst = provider
                .storage()
                .values
                .write()
                .map_err(err("write store"))?;
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

/// A client that can read a message and can never write the ratchet back.
///
/// This is how a notification preview decrypts, on all three platforms, and it is a distinct
/// TYPE rather than a rule because the rule is otherwise impossible to keep. A preview runs
/// somewhere the real client does not — a service worker, an iOS NotificationServiceExtension,
/// an Android background isolate — and every one of those is a second context holding the same
/// key store. The single-client invariant everything here rests on says there must never be
/// two writers.
///
/// So a preview is not made safe by being careful. It is made safe by being unable:
///
///   - It has no `export_state`. There is nowhere for the advanced ratchet to go, so it cannot
///     be persisted by a later edit that "just needed to save something".
///   - It refuses Commits (see [`Client::decrypt_no_merge`]), so it cannot move the epoch even
///     in memory.
///
/// The consequence, and the thing that makes the whole design work: the real client's state is
/// untouched, still holding an unconsumed key for that message, and decrypts it again for real
/// when the app opens. "A message decrypts exactly once" is a property of a COPY of the state,
/// not a global fact — so a copy that is thrown away costs nothing.
///
/// Drop this the moment the notification is shown. It holds plaintext.
pub struct PreviewClient {
    inner: Client,
}

impl PreviewClient {
    /// Loads a read-only client from an exported state blob.
    pub fn import_state(state: &[u8]) -> Result<Self, String> {
        Ok(Self {
            inner: Client::import_state(state)?,
        })
    }

    /// Decrypts one application message for display, with the sender MLS authenticated. `None`
    /// means it was not an application message — a Commit or other control traffic — and there is
    /// nothing to preview.
    ///
    /// The sender comes back so a notification can be titled with the identity that actually signed
    /// the message rather than the one the push payload claims. The push is delivered BY the
    /// untrusted server; taking a name from it is taking the attacker's word for who is messaging
    /// you, on the lock screen, where nobody checks.
    pub fn decrypt(&self, group_id: &[u8], ciphertext: &[u8]) -> Result<Option<Decrypted>, String> {
        self.inner.decrypt_no_merge(group_id, ciphertext)
    }

    /// Whether this client holds the group, so a caller can pick the right one without
    /// attempting a decrypt against each.
    pub fn has_group(&self, group_id: &[u8]) -> bool {
        self.inner.has_group(group_id)
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

/// Sender-authenticated device-to-device history handoff: the canonical, domain-separated
/// transcripts a requester and an offerer sign with their MLS leaf keys.
pub mod history;

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

    /// A conversation's group id, as the server would mint it: opaque and unrelated to
    /// the conversation id, so a group can never be silently created twice.
    const GID: &[u8] = b"grp-0123456789abcdef";
    const DOM: &str = "test.example";

    /// Establishes a group with the given members and hands everyone their state.
    /// Mirrors what the client does: create, stage one Add for ALL devices, and — since
    /// the server accepted it — merge.
    fn establish(owner: &Client, group_id: &[u8], members: &[&Client]) -> AddResult {
        owner.create_group(group_id).unwrap();
        let kps: Vec<Vec<u8>> = members.iter().map(|c| c.key_package().unwrap()).collect();
        let add = owner.stage_add(group_id, &kps).unwrap();
        owner.commit_accepted(group_id).unwrap();
        for m in members {
            m.join_from_welcome(&add.welcome).unwrap();
        }
        add
    }

    /// Just the plaintext of a decrypt, for the many assertions that are only about what was
    /// said. The tests that are about WHO said it read `Decrypted::sender` directly — see
    /// `a_decrypt_carries_the_authenticated_sender`.
    trait Body {
        fn body(self) -> Option<Vec<u8>>;
    }
    impl Body for Result<Option<Decrypted>, String> {
        fn body(self) -> Option<Vec<u8>> {
            self.unwrap().map(|d| d.plaintext)
        }
    }

    // F4: handshake Commits are PublicMessage now, so the delivery service can read
    // the epoch a Commit is built on rather than trusting a number the client sends
    // alongside it. This asserts the framing, and prints a fixture the Go epoch parser
    // (api/internal/mlswire) is tested against, so the two never drift.
    #[test]
    fn commits_are_public_and_carry_a_readable_epoch() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        alice.create_group(GID).unwrap();

        // A Commit built at epoch 0 (the add is the group's first Commit).
        let kp = bob.key_package().unwrap();
        let add = alice.stage_add(GID, &[kp]).unwrap();

        // The commit deserializes as an MlsMessage whose body is a PublicMessage.
        let msg = MlsMessageIn::tls_deserialize(&mut &add.commit[..]).unwrap();
        match msg.extract() {
            MlsMessageBodyIn::PublicMessage(pm) => {
                assert_eq!(pm.epoch().as_u64(), 0, "the add is built on epoch 0");
                assert_eq!(pm.content_type(), ContentType::Commit);
            }
            other => panic!("commit was not a PublicMessage: {other:?}"),
        }

        // Emit the fixture. Run with --nocapture to regenerate it for the Go test.
        eprintln!("GO_FIXTURE_COMMIT_EPOCH0={}", hex_of(&add.commit));
    }

    fn hex_of(b: &[u8]) -> String {
        b.iter().map(|x| format!("{x:02x}")).collect()
    }

    #[test]
    fn stateful_two_party_round_trip() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let ct = alice.encrypt(GID, b"the eagle lands at dawn").unwrap();
        assert_eq!(
            bob.decrypt(GID, &ct).body().as_deref(),
            Some(&b"the eagle lands at dawn"[..])
        );

        let ct2 = bob.encrypt(GID, b"acknowledged").unwrap();
        assert_eq!(
            alice.decrypt(GID, &ct2).body().as_deref(),
            Some(&b"acknowledged"[..])
        );
    }

    // THE BUG. Bob is signed in on two devices. Both must be leaves of the group, and
    // both must read every message — including the ones Bob's OTHER device sent.
    //
    // Before device-aware membership there was one leaf per user, so exactly one of
    // Bob's devices was in the group and the other showed a conversation full of
    // blanks. This is the test that would have caught it.
    #[test]
    fn every_device_of_a_member_can_read() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob_phone = Client::new(DOM, "bob", "phone").unwrap();
        let bob_laptop = Client::new(DOM, "bob", "laptop").unwrap();

        establish(&alice, GID, &[&bob_phone, &bob_laptop]);

        // Alice speaks: BOTH of Bob's devices read it.
        let ct = alice.encrypt(GID, b"hello bob").unwrap();
        assert_eq!(
            bob_phone.decrypt(GID, &ct).body().as_deref(),
            Some(&b"hello bob"[..])
        );
        assert_eq!(
            bob_laptop.decrypt(GID, &ct).body().as_deref(),
            Some(&b"hello bob"[..])
        );

        // Bob speaks from his phone: Alice reads it, AND so does Bob's own laptop —
        // a sender cannot decrypt their own message, but a different device of the
        // same person is a different leaf, so it can.
        let ct2 = bob_phone.encrypt(GID, b"sent from my phone").unwrap();
        assert_eq!(
            alice.decrypt(GID, &ct2).body().as_deref(),
            Some(&b"sent from my phone"[..])
        );
        assert_eq!(
            bob_laptop.decrypt(GID, &ct2).body().as_deref(),
            Some(&b"sent from my phone"[..]),
            "a user's second device must read what their first device sent",
        );
    }

    // A device that joins later is added to the EXISTING group — the group is never
    // torn down and rebuilt around it. Everyone already in it keeps reading, and the
    // newcomer reads everything from its epoch onward.
    #[test]
    fn a_new_device_is_added_without_destroying_the_group() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob_phone = Client::new(DOM, "bob", "phone").unwrap();
        establish(&alice, GID, &[&bob_phone]);

        let early = alice.encrypt(GID, b"before the laptop existed").unwrap();
        assert_eq!(
            bob_phone.decrypt(GID, &early).body().as_deref(),
            Some(&b"before the laptop existed"[..])
        );

        // Bob signs in on a laptop. Alice notices the device is missing from the group
        // and adds it — one Commit, one Welcome.
        let bob_laptop = Client::new(DOM, "bob", "laptop").unwrap();
        let add = alice
            .stage_add(GID, &[bob_laptop.key_package().unwrap()])
            .unwrap();
        alice.commit_accepted(GID).unwrap();
        bob_laptop.join_from_welcome(&add.welcome).unwrap();
        bob_phone.apply_commit(GID, &add.commit).unwrap();

        // Everyone — old devices and new — reads what comes next.
        let ct = alice.encrypt(GID, b"now you both see this").unwrap();
        assert_eq!(
            bob_phone.decrypt(GID, &ct).body().as_deref(),
            Some(&b"now you both see this"[..])
        );
        assert_eq!(
            bob_laptop.decrypt(GID, &ct).body().as_deref(),
            Some(&b"now you both see this"[..])
        );

        // And the laptop can speak; the phone and Alice both hear it.
        let ct2 = bob_laptop.encrypt(GID, b"laptop here").unwrap();
        assert_eq!(
            alice.decrypt(GID, &ct2).body().as_deref(),
            Some(&b"laptop here"[..])
        );
        assert_eq!(
            bob_phone.decrypt(GID, &ct2).body().as_deref(),
            Some(&b"laptop here"[..])
        );
    }

    // Removing a member must remove EVERY device they have. Taking out one leaf and
    // leaving the other means the person you just removed carries on reading the group
    // from their other device — a confidentiality failure, not a cosmetic one.
    #[test]
    fn removing_a_user_removes_all_of_their_devices() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob_phone = Client::new(DOM, "bob", "phone").unwrap();
        let bob_laptop = Client::new(DOM, "bob", "laptop").unwrap();
        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        establish(&alice, GID, &[&bob_phone, &bob_laptop, &carol]);

        assert_eq!(alice.member_identities(GID).unwrap().len(), 4);

        let commit = alice
            .stage_remove_users(GID, &[user_key(DOM, "bob")])
            .unwrap();
        alice.commit_accepted(GID).unwrap();
        carol.apply_commit(GID, &commit).unwrap();

        // Alice and Carol are all that is left.
        assert_eq!(alice.member_identities(GID).unwrap().len(), 2);

        let ct = alice.encrypt(GID, b"bob is gone").unwrap();
        assert_eq!(
            carol.decrypt(GID, &ct).body().as_deref(),
            Some(&b"bob is gone"[..])
        );
        assert!(
            bob_phone.decrypt(GID, &ct).is_err(),
            "removed device must not decrypt"
        );
        assert!(
            bob_laptop.decrypt(GID, &ct).is_err(),
            "the removed member's OTHER device must not decrypt either",
        );
    }

    // Two members stage a Commit against the same epoch. Only one can win — the server
    // decides — and the LOSER must come away unforked: it throws its Commit away,
    // applies the winner's, and can still read the group.
    //
    // Merging a Commit before the server has accepted it (which is what add_members used
    // to do) is what makes this unrecoverable: the loser advances its own ratchet to an
    // epoch nobody else is in, and is silently cut off for good.
    #[test]
    fn a_rejected_commit_leaves_the_loser_able_to_catch_up() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        let dave = Client::new(DOM, "dave", "dev-d").unwrap();

        // Both members stage an Add against epoch 1, concurrently.
        let base = alice.epoch(GID).unwrap();
        assert_eq!(base, bob.epoch(GID).unwrap());
        let alice_add = alice
            .stage_add(GID, &[carol.key_package().unwrap()])
            .unwrap();
        let _bob_add = bob.stage_add(GID, &[dave.key_package().unwrap()]).unwrap();

        // The server accepts Alice's (it got there first) and refuses Bob's.
        alice.commit_accepted(GID).unwrap();
        bob.commit_rejected(GID).unwrap();

        // Bob discards his and applies the winner. He is back in step with the group.
        bob.apply_commit(GID, &alice_add.commit).unwrap();
        carol.join_from_welcome(&alice_add.welcome).unwrap();
        assert_eq!(alice.epoch(GID).unwrap(), bob.epoch(GID).unwrap());
        assert_eq!(alice.epoch(GID).unwrap(), base + 1);

        let ct = alice.encrypt(GID, b"still one group").unwrap();
        assert_eq!(
            bob.decrypt(GID, &ct).body().as_deref(),
            Some(&b"still one group"[..])
        );
        assert_eq!(
            carol.decrypt(GID, &ct).body().as_deref(),
            Some(&b"still one group"[..])
        );

        // And Bob can retry his add against the new epoch.
        let retry = bob.stage_add(GID, &[dave.key_package().unwrap()]).unwrap();
        bob.commit_accepted(GID).unwrap();
        alice.apply_commit(GID, &retry.commit).unwrap();
        carol.apply_commit(GID, &retry.commit).unwrap();
        dave.join_from_welcome(&retry.welcome).unwrap();

        let ct2 = dave.encrypt(GID, b"dave made it in").unwrap();
        assert_eq!(
            alice.decrypt(GID, &ct2).body().as_deref(),
            Some(&b"dave made it in"[..])
        );
        assert_eq!(
            bob.decrypt(GID, &ct2).body().as_deref(),
            Some(&b"dave made it in"[..])
        );
    }

    // A new device joins an existing group with NOBODY'S help: no Welcome, no member admitting it.
    //
    // This is the mechanism that lets a freshly installed phone open a chat whose group already
    // exists and just work — instead of waiting to be admitted, or tearing the group down. A member
    // exports the GroupInfo; the newcomer turns it into an external commit that adds its own leaf; the
    // existing members apply it. Non-destructive: everyone keeps their keys, and the newcomer still
    // cannot read a word from before it joined.
    #[test]
    fn a_new_device_joins_by_external_commit_without_a_welcome() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        // Said before the newcomer existed — it must never be able to read this.
        let before = alice.encrypt(GID, b"before carol").unwrap();

        // Carol is not in the group. Alice, who is, exports the GroupInfo; Carol external-joins from it.
        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        let group_info = alice.export_group_info(GID).unwrap();
        let commit = carol.join_by_external_commit(&group_info).unwrap();

        // The server accepts the external commit. Carol merges it; the existing members apply it.
        carol.commit_accepted(GID).unwrap();
        alice.apply_commit(GID, &commit).unwrap();
        bob.apply_commit(GID, &commit).unwrap();

        // One group, one epoch, three members.
        assert_eq!(alice.epoch(GID).unwrap(), carol.epoch(GID).unwrap());
        assert_eq!(bob.epoch(GID).unwrap(), carol.epoch(GID).unwrap());
        assert_eq!(alice.member_identities(GID).unwrap().len(), 3);

        // Carol reads what comes after, and the others read Carol.
        let after = alice.encrypt(GID, b"after carol").unwrap();
        assert_eq!(
            carol.decrypt(GID, &after).body().as_deref(),
            Some(&b"after carol"[..])
        );
        let from_carol = carol.encrypt(GID, b"carol is in").unwrap();
        assert_eq!(
            alice.decrypt(GID, &from_carol).body().as_deref(),
            Some(&b"carol is in"[..])
        );
        assert_eq!(
            bob.decrypt(GID, &from_carol).body().as_deref(),
            Some(&b"carol is in"[..])
        );

        // But not what was said before she arrived.
        assert!(
            carol.decrypt(GID, &before).is_err(),
            "a newcomer must not read pre-join history"
        );
    }

    // Two newcomers external-join against the same epoch. Like any commit, only one can win the
    // server's compare-and-set; the loser must recover — discard its group and rejoin from fresh
    // GroupInfo — rather than fork off.
    #[test]
    fn a_rejected_external_join_recovers_from_fresh_group_info() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        let dave = Client::new(DOM, "dave", "dev-d").unwrap();

        // Both external-join against the same epoch, concurrently.
        let gi = alice.export_group_info(GID).unwrap();
        let carol_commit = carol.join_by_external_commit(&gi).unwrap();
        let _dave_commit = dave.join_by_external_commit(&gi).unwrap();

        // The server accepts Carol's and refuses Dave's. Carol merges; the members apply.
        carol.commit_accepted(GID).unwrap();
        alice.apply_commit(GID, &carol_commit).unwrap();
        bob.apply_commit(GID, &carol_commit).unwrap();

        // Dave lost. An external commit cannot be cleared, so he discards the whole group and rejoins
        // from GroupInfo exported at the NEW epoch.
        dave.delete_group(GID).unwrap();
        let gi2 = alice.export_group_info(GID).unwrap();
        let dave_commit = dave.join_by_external_commit(&gi2).unwrap();
        dave.commit_accepted(GID).unwrap();
        alice.apply_commit(GID, &dave_commit).unwrap();
        bob.apply_commit(GID, &dave_commit).unwrap();
        carol.apply_commit(GID, &dave_commit).unwrap();

        // All four in one group.
        assert_eq!(alice.member_identities(GID).unwrap().len(), 4);
        let ct = dave.encrypt(GID, b"dave made it in too").unwrap();
        assert_eq!(
            alice.decrypt(GID, &ct).body().as_deref(),
            Some(&b"dave made it in too"[..])
        );
        assert_eq!(
            carol.decrypt(GID, &ct).body().as_deref(),
            Some(&b"dave made it in too"[..])
        );
    }

    // A message sent BEFORE a membership change must still decrypt AFTER it.
    //
    // This is what max_past_epochs buys. Messages do not arrive in order — a Commit can
    // overtake them on the live stream, and paging back through history replays them
    // long after. With OpenMLS's default of zero retained epochs, every one of those is
    // lost, which is a conversation full of blanks after anybody joins.
    #[test]
    fn messages_from_a_past_epoch_still_decrypt_after_a_commit() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        // Alice speaks. Bob does not read it yet — it is still in flight.
        let in_flight = alice.encrypt(GID, b"sent before carol joined").unwrap();

        // Carol joins; Bob applies the Commit and moves to the new epoch.
        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        let add = alice
            .stage_add(GID, &[carol.key_package().unwrap()])
            .unwrap();
        alice.commit_accepted(GID).unwrap();
        bob.apply_commit(GID, &add.commit).unwrap();
        carol.join_from_welcome(&add.welcome).unwrap();

        // Only NOW does the older message reach Bob. He is an epoch past it, and must
        // still be able to read it.
        assert_eq!(
            bob.decrypt(GID, &in_flight).body().as_deref(),
            Some(&b"sent before carol joined"[..]),
            "a message from the previous epoch must survive a membership change",
        );
    }

    // The property IndexedDB persistence rests on: a client's whole state survives
    // export → drop → import, and the restored client can still decrypt.
    #[test]
    fn state_survives_persistence_round_trip() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let saved = bob.export_state().unwrap();
        drop(bob);
        let bob = Client::import_state(&saved).unwrap();

        let ct = alice.encrypt(GID, b"still works after reload").unwrap();
        assert_eq!(
            bob.decrypt(GID, &ct).body().as_deref(),
            Some(&b"still works after reload"[..])
        );

        let ct2 = bob.encrypt(GID, b"reply from restored bob").unwrap();
        assert_eq!(
            alice.decrypt(GID, &ct2).body().as_deref(),
            Some(&b"reply from restored bob"[..])
        );
    }

    // The untrusted Delivery Service can replay an old Welcome. Re-joining would roll
    // our ratchet back to the Welcome's stale epoch, so it must be refused — and the
    // live group state must survive the attempt intact.
    #[test]
    fn replayed_welcome_is_refused_and_state_survives() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        let add = establish(&alice, GID, &[&bob]);

        let ct = alice.encrypt(GID, b"before the replay").unwrap();
        assert_eq!(
            bob.decrypt(GID, &ct).body().as_deref(),
            Some(&b"before the replay"[..])
        );

        assert!(bob.join_from_welcome(&add.welcome).is_err());

        let ct2 = alice.encrypt(GID, b"after the replay").unwrap();
        assert_eq!(
            bob.decrypt(GID, &ct2).body().as_deref(),
            Some(&b"after the replay"[..])
        );
    }

    // The safety number is what catches a malicious Delivery Service. Both honest
    // members derive it from the group's own ratchet tree, so they agree; a substituted
    // KeyPackage changes it, which is the signal the two humans need.
    #[test]
    fn safety_number_agrees_between_members_and_changes_under_substitution() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let from_alice = safety_number(&alice.member_keys(GID).unwrap());
        let from_bob = safety_number(&bob.member_keys(GID).unwrap());
        assert_eq!(from_alice, from_bob);

        let groups: Vec<&str> = from_alice.split(' ').collect();
        assert_eq!(groups.len(), 12);
        assert!(groups
            .iter()
            .all(|g| g.len() == 5 && g.chars().all(|c| c.is_ascii_digit())));

        // The server hands Alice an impostor's KeyPackage in place of Bob's.
        let mallory = Client::new(DOM, "bob", "dev-b").unwrap(); // claims to be Bob's device
        let alice2 = Client::new(DOM, "alice", "dev-a").unwrap();
        let group2 = b"grp-second";
        establish(&alice2, group2, &[&mallory]);
        let under_mitm = safety_number(&alice2.member_keys(group2).unwrap());

        assert_ne!(
            under_mitm, from_alice,
            "a substituted KeyPackage must change the safety number, or it detects nothing",
        );
    }

    // An ORDINARY KeyPackage is single-use: OpenMLS deletes the private init key on the
    // first join. This is why a user's published stock is exhaustible.
    #[test]
    fn ordinary_key_package_is_single_use() {
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        let kp = bob.key_package().unwrap();

        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        alice.create_group(b"group-a").unwrap();
        let add_a = alice.stage_add(b"group-a", &[kp.clone()]).unwrap();
        alice.commit_accepted(b"group-a").unwrap();
        bob.join_from_welcome(&add_a.welcome).unwrap();

        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        carol.create_group(b"group-c").unwrap();
        let add_c = carol.stage_add(b"group-c", &[kp]).unwrap();
        carol.commit_accepted(b"group-c").unwrap();
        assert!(bob.join_from_welcome(&add_c.welcome).is_err());
    }

    // A LAST-RESORT KeyPackage keeps its private key, so the same one can be handed out
    // repeatedly and a user can always be reached.
    #[test]
    fn last_resort_key_package_can_be_used_repeatedly() {
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        let kp = bob.last_resort_key_package().unwrap();

        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        alice.create_group(b"group-a").unwrap();
        let add_a = alice.stage_add(b"group-a", &[kp.clone()]).unwrap();
        alice.commit_accepted(b"group-a").unwrap();
        bob.join_from_welcome(&add_a.welcome).unwrap();

        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        carol.create_group(b"group-c").unwrap();
        let add_c = carol.stage_add(b"group-c", &[kp]).unwrap();
        carol.commit_accepted(b"group-c").unwrap();
        bob.join_from_welcome(&add_c.welcome)
            .expect("a last-resort KeyPackage must still work after being used once");

        let ct = carol.encrypt(b"group-c", b"second group works").unwrap();
        assert_eq!(
            bob.decrypt(b"group-c", &ct).body().as_deref(),
            Some(&b"second group works"[..])
        );
        let ct2 = alice
            .encrypt(b"group-a", b"first group still works")
            .unwrap();
        assert_eq!(
            bob.decrypt(b"group-a", &ct2).body().as_deref(),
            Some(&b"first group still works"[..])
        );
    }

    // A forged Welcome burns the ordinary KeyPackage it names: OpenMLS deletes the
    // private key as soon as it matches the hash_ref, BEFORE validating anything. Pinned
    // here because the mitigations (last-resort packages, and only processing Welcomes
    // from conversation members) exist entirely because of it.
    #[test]
    fn forged_welcome_burns_an_ordinary_key_package() {
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        let kp = bob.key_package().unwrap();

        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        alice.create_group(b"group-a").unwrap();
        let valid = alice.stage_add(b"group-a", &[kp]).unwrap().welcome;
        alice.commit_accepted(b"group-a").unwrap();

        let mut forged = valid.clone();
        let n = forged.len();
        for b in forged[n - 24..].iter_mut() {
            *b ^= 0xff;
        }

        assert!(bob.join_from_welcome(&forged).is_err());
        assert!(
            bob.join_from_welcome(&valid).is_err(),
            "OpenMLS is expected to have burned the key package here; if this now passes, \
             upstream changed and the mitigations can be revisited",
        );
    }

    // The last-resort package is immune to that burn, which is what turns a permanent
    // denial of service into a temporary loss of the single-use stock.
    #[test]
    fn forged_welcome_cannot_burn_the_last_resort_key_package() {
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        let kp = bob.last_resort_key_package().unwrap();

        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        alice.create_group(b"group-a").unwrap();
        let valid = alice.stage_add(b"group-a", &[kp]).unwrap().welcome;
        alice.commit_accepted(b"group-a").unwrap();

        let mut forged = valid.clone();
        let n = forged.len();
        for b in forged[n - 24..].iter_mut() {
            *b ^= 0xff;
        }

        assert!(bob.join_from_welcome(&forged).is_err());
        bob.join_from_welcome(&valid)
            .expect("a forged Welcome must not burn the last-resort key package");

        let ct = alice.encrypt(b"group-a", b"still reachable").unwrap();
        assert_eq!(
            bob.decrypt(b"group-a", &ct).body().as_deref(),
            Some(&b"still reachable"[..])
        );
    }

    // A third client not in the group cannot decrypt — the whole point.
    #[test]
    fn outsider_cannot_decrypt() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        let mallory = Client::new(DOM, "mallory", "dev-m").unwrap();
        establish(&alice, GID, &[&bob]);

        let ct = alice.encrypt(GID, b"secret").unwrap();
        assert!(mallory.decrypt(GID, &ct).is_err());
    }

    // MLS forbids committing your own removal, so leaving a group cannot be a Commit.
    // A member leaves by dropping their membership and destroying their local state; the
    // members who remain prune the leaves they left behind when they next reconcile. This
    // pins both halves: the refusal, and the pruning that stands in for it.
    #[test]
    fn a_member_cannot_commit_their_own_removal_but_others_can_prune_them() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "phone").unwrap();
        let bob_laptop = Client::new(DOM, "bob", "laptop").unwrap();
        establish(&alice, GID, &[&bob, &bob_laptop]);

        // Bob cannot remove himself, on either device.
        assert!(bob
            .stage_remove_users(GID, &[user_key(DOM, "bob")])
            .is_err());
        // Nor can he do it by sneaking his own id into a longer list.
        assert!(bob
            .stage_remove_users(GID, &[user_key(DOM, "bob"), user_key(DOM, "bob")])
            .is_err());

        // Alice prunes him — both of his devices — and Bob is out.
        let commit = alice
            .stage_remove_users(GID, &[user_key(DOM, "bob")])
            .unwrap();
        alice.commit_accepted(GID).unwrap();
        assert_eq!(alice.member_identities(GID).unwrap().len(), 1);

        let ct = alice.encrypt(GID, b"after bob left").unwrap();
        assert!(bob.decrypt(GID, &ct).is_err());
        assert!(bob_laptop.decrypt(GID, &ct).is_err());
        let _ = commit;
    }

    // Removing somebody who is not in the group is a no-op to be tolerated, not an error
    // to fail on: two members reconciling at once will both name a leaver, and only one
    // of them can win.
    #[test]
    fn removing_a_user_who_is_not_in_the_group_is_refused_not_fatal() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let err = alice
            .stage_remove_users(GID, &[user_key(DOM, "nobody")])
            .unwrap_err();
        assert!(err.contains("none of those users"), "got: {err}");
        // And the group is untouched — Alice can still talk to Bob.
        let ct = alice.encrypt(GID, b"unharmed").unwrap();
        assert_eq!(
            bob.decrypt(GID, &ct).body().as_deref(),
            Some(&b"unharmed"[..])
        );
    }

    // Pruning a ghost device must leave that person's LIVE devices alone.
    //
    // A browser whose storage is evicted comes back with new key material under a new
    // device id, leaving a leaf in the tree whose private keys no longer exist anywhere.
    // That leaf should go. But removing it BY USER would take the same person's working
    // phone out of the group with it — so the prune has to name the leaf, not the person.
    #[test]
    fn pruning_a_ghost_device_leaves_the_persons_other_devices_alone() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob_phone = Client::new(DOM, "bob", "phone").unwrap();
        let bob_ghost = Client::new(DOM, "bob", "evicted-browser").unwrap();
        establish(&alice, GID, &[&bob_phone, &bob_ghost]);
        assert_eq!(alice.member_identities(GID).unwrap().len(), 3);

        // Prune only the ghost leaf.
        let commit = alice
            .stage_remove_devices(
                GID,
                &[String::from_utf8(identity(DOM, "bob", "evicted-browser")).unwrap()],
            )
            .unwrap();
        alice.commit_accepted(GID).unwrap();
        bob_phone.apply_commit(GID, &commit).unwrap();

        assert_eq!(alice.member_identities(GID).unwrap().len(), 2);

        // Bob's phone is untouched and still reads the group.
        let ct = alice.encrypt(GID, b"your phone still works").unwrap();
        assert_eq!(
            bob_phone.decrypt(GID, &ct).body().as_deref(),
            Some(&b"your phone still works"[..]),
            "pruning a ghost device must not cut off the same person's live device",
        );
        // The ghost is out.
        assert!(bob_ghost.decrypt(GID, &ct).is_err());
    }

    // The same client must never prune itself, however it is asked.
    #[test]
    fn a_client_never_removes_its_own_leaf() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        assert!(alice
            .stage_remove_devices(
                GID,
                &[String::from_utf8(identity(DOM, "alice", "dev-a")).unwrap()]
            )
            .is_err());
        assert!(alice
            .stage_remove_users(GID, &[user_key(DOM, "alice")])
            .is_err());

        // Still a healthy two-member group.
        let ct = alice.encrypt(GID, b"intact").unwrap();
        assert_eq!(
            bob.decrypt(GID, &ct).body().as_deref(),
            Some(&b"intact"[..])
        );
    }

    // The exporter is what makes a voice call end-to-end encrypted: every member device
    // derives the SAME bytes for the same call, without anything being sent, so the server
    // never sees the key that protects the SDP — and therefore cannot swap the DTLS
    // fingerprint inside it and put itself in the middle of the call.
    #[test]
    fn every_device_of_every_member_derives_the_same_call_key() {
        let alice = Client::new(DOM, "alice", "laptop").unwrap();
        let bob_phone = Client::new(DOM, "bob", "phone").unwrap();
        let bob_desktop = Client::new(DOM, "bob", "desktop").unwrap();
        establish(&alice, GID, &[&bob_phone, &bob_desktop]);

        let call = b"call-abc";
        let from_alice = alice.export_secret(GID, "pheme-call-v1", call, 32).unwrap();
        let from_phone = bob_phone
            .export_secret(GID, "pheme-call-v1", call, 32)
            .unwrap();
        let from_desktop = bob_desktop
            .export_secret(GID, "pheme-call-v1", call, 32)
            .unwrap();

        assert_eq!(from_alice.len(), 32);
        assert_eq!(from_alice, from_phone);
        assert_eq!(
            from_alice, from_desktop,
            "every device in the group must derive the same call key, or the device that \
             answers cannot decrypt the offer",
        );
    }

    // Each SENDING device gets its own key, by putting its identity in the context.
    //
    // That is not decoration. All of a person's devices could otherwise encrypt under one
    // key with independently chosen nonces, and an AES-GCM nonce collision between two of
    // them is catastrophic — it leaks the authentication key. Separating the keys removes
    // the possibility rather than relying on 96 random bits not repeating.
    #[test]
    fn each_sending_device_derives_a_distinct_key() {
        let alice = Client::new(DOM, "alice", "laptop").unwrap();
        let bob_phone = Client::new(DOM, "bob", "phone").unwrap();
        let bob_desktop = Client::new(DOM, "bob", "desktop").unwrap();
        establish(&alice, GID, &[&bob_phone, &bob_desktop]);

        let phone_key = alice
            .export_secret(GID, "pheme-call-v1", b"call-1|bob:phone", 32)
            .unwrap();
        let desktop_key = alice
            .export_secret(GID, "pheme-call-v1", b"call-1|bob:desktop", 32)
            .unwrap();
        assert_ne!(phone_key, desktop_key);

        // And any member can derive the key of any sender, which is how they decrypt it.
        assert_eq!(
            phone_key,
            bob_desktop
                .export_secret(GID, "pheme-call-v1", b"call-1|bob:phone", 32)
                .unwrap(),
            "a member must be able to derive another device's key in order to read it",
        );
    }

    // A different call gets a different key, so signals from an old call cannot be replayed
    // into a new one even by a server that kept them.
    #[test]
    fn a_different_call_derives_a_different_key() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let one = alice
            .export_secret(GID, "pheme-call-v1", b"call-1", 32)
            .unwrap();
        let two = alice
            .export_secret(GID, "pheme-call-v1", b"call-2", 32)
            .unwrap();
        assert_ne!(one, two);
    }

    // --- authenticated sender attribution ------------------------------------------------
    //
    // The server is the untrusted Delivery Service, and until this existed it was also the
    // only source of "who sent this". These pin the replacement.

    /// A decrypt names the leaf that actually signed the message — not whoever relayed it.
    #[test]
    fn a_decrypt_carries_the_authenticated_sender() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob = Client::new(DOM, "bob", "laptop").unwrap();
        establish(&alice, GID, &[&bob]);

        let ct = alice.encrypt(GID, b"it was me").unwrap();
        let opened = bob
            .decrypt(GID, &ct)
            .unwrap()
            .expect("an application message");
        assert_eq!(opened.plaintext, b"it was me");
        assert_eq!(opened.sender, b"mimi://test.example/d/alice/phone");
        assert_eq!(user_of(&opened.sender), b"mimi://test.example/u/alice");
    }

    /// Two devices of one person are two leaves, and the attribution says WHICH — a person's
    /// laptop and their phone are distinguishable, and both reduce to the same user.
    #[test]
    fn the_sender_names_the_device_and_reduces_to_the_user() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob_phone = Client::new(DOM, "bob", "phone").unwrap();
        let bob_laptop = Client::new(DOM, "bob", "laptop").unwrap();
        establish(&alice, GID, &[&bob_phone, &bob_laptop]);

        let ct = bob_laptop.encrypt(GID, b"from the laptop").unwrap();
        let at_alice = alice.decrypt(GID, &ct).unwrap().unwrap();
        let at_phone = bob_phone.decrypt(GID, &ct).unwrap().unwrap();

        assert_eq!(at_alice.sender, b"mimi://test.example/d/bob/laptop");
        assert_eq!(at_alice.sender, at_phone.sender, "every reader agrees");
        assert_eq!(user_of(&at_alice.sender), b"mimi://test.example/u/bob");
    }

    /// The epoch comes back with the sender, so a caller can say WHICH membership the sender
    /// was authenticated against.
    #[test]
    fn a_decrypt_carries_the_epoch_it_was_framed_in() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let first = alice.encrypt(GID, b"epoch one").unwrap();
        assert_eq!(bob.decrypt(GID, &first).unwrap().unwrap().epoch, 1);

        // A membership change moves the epoch, and the next message is framed in the new one.
        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        let add = alice
            .stage_add(GID, &[carol.key_package().unwrap()])
            .unwrap();
        alice.commit_accepted(GID).unwrap();
        bob.apply_commit(GID, &add.commit).unwrap();

        let second = alice.encrypt(GID, b"epoch two").unwrap();
        assert_eq!(bob.decrypt(GID, &second).unwrap().unwrap().epoch, 2);
    }

    /// A message read LATE — after the group has moved on — is still attributed to the leaf that
    /// signed it, and still reports the epoch it was framed in rather than the current one.
    ///
    /// This is the ordinary case in a chat, not a corner: an SSE Commit overtakes a page of older
    /// messages, or a user scrolls back. If attribution came from "who is at this leaf index now"
    /// it would drift every time somebody joined or left, and a message would end up rendered under
    /// a member who was not even in the group when it was written.
    #[test]
    fn a_past_epoch_decrypt_still_names_the_leaf_that_signed_it() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob = Client::new(DOM, "bob", "laptop").unwrap();
        establish(&alice, GID, &[&bob]);

        // Written at epoch 1 and deliberately NOT read yet.
        let old = alice.encrypt(GID, b"said before the change").unwrap();

        // The group moves on. Two commits, so the message is genuinely behind the head.
        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        let add = alice
            .stage_add(GID, &[carol.key_package().unwrap()])
            .unwrap();
        alice.commit_accepted(GID).unwrap();
        bob.apply_commit(GID, &add.commit).unwrap();
        let dave = Client::new(DOM, "dave", "dev-d").unwrap();
        let add2 = alice
            .stage_add(GID, &[dave.key_package().unwrap()])
            .unwrap();
        alice.commit_accepted(GID).unwrap();
        bob.apply_commit(GID, &add2.commit).unwrap();
        assert_eq!(bob.epoch(GID).unwrap(), 3);

        let opened = bob
            .decrypt(GID, &old)
            .unwrap()
            .expect("a past-epoch application message is still readable");
        assert_eq!(opened.plaintext, b"said before the change");
        assert_eq!(opened.sender, b"mimi://test.example/d/alice/phone");
        assert_eq!(
            opened.epoch, 1,
            "the epoch it was FRAMED in, not the current one"
        );
    }

    /// The same, on the preview path: a notification for a message that arrived while the app was
    /// catching up must not be titled by whoever happens to hold that leaf index now.
    #[test]
    fn a_past_epoch_preview_still_names_the_leaf_that_signed_it() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob = Client::new(DOM, "bob", "laptop").unwrap();
        establish(&alice, GID, &[&bob]);

        let old = alice.encrypt(GID, b"before the join").unwrap();
        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        let add = alice
            .stage_add(GID, &[carol.key_package().unwrap()])
            .unwrap();
        alice.commit_accepted(GID).unwrap();
        bob.apply_commit(GID, &add.commit).unwrap();

        let preview = PreviewClient::import_state(&bob.export_state().unwrap()).unwrap();
        let opened = preview.decrypt(GID, &old).unwrap().unwrap();
        assert_eq!(opened.sender, b"mimi://test.example/d/alice/phone");
        assert_eq!(opened.epoch, 1);
    }

    /// A preview attributes too. A notification titled with the name the PUSH claims is a
    /// notification titled by the server; the lock screen is exactly where nobody checks.
    #[test]
    fn a_preview_decrypt_carries_the_authenticated_sender() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob = Client::new(DOM, "bob", "laptop").unwrap();
        establish(&alice, GID, &[&bob]);

        let ct = alice.encrypt(GID, b"ping").unwrap();
        let preview = PreviewClient::import_state(&bob.export_state().unwrap()).unwrap();
        let opened = preview.decrypt(GID, &ct).unwrap().unwrap();
        assert_eq!(opened.plaintext, b"ping");
        assert_eq!(opened.sender, b"mimi://test.example/d/alice/phone");
    }

    // --- signed history handoff ------------------------------------------------------------

    /// The happy path: a member signs a request, a co-member verifies it against the leaf key
    /// the ratchet tree holds.
    #[test]
    fn a_history_request_verifies_against_the_requester_leaf() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob = Client::new(DOM, "bob", "laptop").unwrap();
        establish(&alice, GID, &[&bob]);

        let nonce = b"nonce-0123456789";
        let sig = bob.sign_history_request(GID, "conv-1", 1, nonce).unwrap();
        alice
            .verify_history_request(
                GID,
                "conv-1",
                1,
                "mimi://test.example/d/bob/laptop",
                nonce,
                &sig,
            )
            .expect("a co-member's signature must verify");
    }

    /// THE FORGERY. Carol is a perfectly legitimate member — she derives the same exporter
    /// secret as everyone else, so under exporter-AEAD alone she could mint a request in
    /// Bob's name and have Alice seal the entire conversation to a key bound to him.
    ///
    /// She does not hold Bob's leaf signature key, so the signature does not verify.
    #[test]
    fn a_member_cannot_sign_a_history_request_as_another_member() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob = Client::new(DOM, "bob", "laptop").unwrap();
        let carol = Client::new(DOM, "carol", "phone").unwrap();
        establish(&alice, GID, &[&bob, &carol]);

        let nonce = b"nonce-0123456789";
        let carols_signature = carol.sign_history_request(GID, "conv-1", 1, nonce).unwrap();

        assert!(
            alice
                .verify_history_request(
                    GID,
                    "conv-1",
                    1,
                    "mimi://test.example/d/bob/laptop", // ...but claiming to be Bob
                    nonce,
                    &carols_signature,
                )
                .is_err(),
            "a member signing under another member's identity must be refused",
        );
    }

    /// Every bound field is actually bound: change any one of them after signing and the
    /// signature stops verifying.
    #[test]
    fn a_history_request_is_bound_to_every_field() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob = Client::new(DOM, "bob", "laptop").unwrap();
        establish(&alice, GID, &[&bob]);

        let nonce = b"nonce-0123456789";
        let sig = bob.sign_history_request(GID, "conv-1", 1, nonce).unwrap();
        let bob_id = "mimi://test.example/d/bob/laptop";

        assert!(
            alice
                .verify_history_request(GID, "conv-2", 1, bob_id, nonce, &sig)
                .is_err(),
            "another conversation",
        );
        assert!(
            alice
                .verify_history_request(GID, "conv-1", 2, bob_id, nonce, &sig)
                .is_err(),
            "another epoch",
        );
        assert!(
            alice
                .verify_history_request(GID, "conv-1", 1, bob_id, b"different-nonce!", &sig)
                .is_err(),
            "another nonce",
        );
        let mut tampered = sig.clone();
        tampered[0] ^= 0x01;
        assert!(
            alice
                .verify_history_request(GID, "conv-1", 1, bob_id, nonce, &tampered)
                .is_err(),
            "a flipped bit in the signature",
        );
    }

    /// An identity nobody holds a leaf under is refused, rather than falling back to some
    /// default key.
    #[test]
    fn a_history_request_from_a_non_member_is_refused() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob = Client::new(DOM, "bob", "laptop").unwrap();
        let mallory = Client::new(DOM, "mallory", "phone").unwrap();
        establish(&alice, GID, &[&bob]);

        // Mallory cannot even sign: she does not hold the group.
        assert!(mallory
            .sign_history_request(GID, "conv-1", 1, b"nonce-0123456789")
            .is_err());
        // And a signature purporting to be from her is refused: no leaf, no key.
        assert!(alice
            .verify_history_request(
                GID,
                "conv-1",
                1,
                "mimi://test.example/d/mallory/phone",
                b"nonce-0123456789",
                &[0u8; 64],
            )
            .is_err());
    }

    /// An offer verifies for the device it names, carries the blob's digest, and is refused
    /// the moment either the blob or the claimed offerer changes.
    #[test]
    fn a_history_offer_is_bound_to_its_blob_and_its_offerer() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob = Client::new(DOM, "bob", "laptop").unwrap();
        let carol = Client::new(DOM, "carol", "phone").unwrap();
        establish(&alice, GID, &[&bob, &carol]);

        let bob_id = "mimi://test.example/d/bob/laptop";
        let alice_id = "mimi://test.example/d/alice/phone";
        let sealed = b"sealed-history-ciphertext";
        let req_nonce = b"nonce-0123456789";

        let sig = alice
            .sign_history_offer(
                GID, "conv-1", 1, bob_id, "hist-1", b"salt", b"nonce", req_nonce, sealed,
            )
            .unwrap();

        bob.verify_history_offer(
            GID, "conv-1", 1, alice_id, "hist-1", b"salt", b"nonce", req_nonce, sealed, &sig,
        )
        .expect("the offer Alice actually signed, opened by the device it names");

        // The server stores the blob. Swapping its bytes must break the signature, or the
        // signature would be over a pointer rather than over the history.
        assert!(
            bob.verify_history_offer(
                GID,
                "conv-1",
                1,
                alice_id,
                "hist-1",
                b"salt",
                b"nonce",
                req_nonce,
                b"tampered-history-ciphertext",
                &sig,
            )
            .is_err(),
            "a blob swapped behind the offer",
        );

        // Carol re-signing Alice's offer under her own identity does not make it Alice's.
        let carols = carol
            .sign_history_offer(
                GID, "conv-1", 1, bob_id, "hist-1", b"salt", b"nonce", req_nonce, sealed,
            )
            .unwrap();
        assert!(
            bob.verify_history_offer(
                GID, "conv-1", 1, alice_id, "hist-1", b"salt", b"nonce", req_nonce, sealed,
                &carols,
            )
            .is_err(),
            "one member's signature under another member's name",
        );
        // Under her OWN name it verifies — she is a member, and this is a legitimate offer.
        bob.verify_history_offer(
            GID,
            "conv-1",
            1,
            "mimi://test.example/d/carol/phone",
            "hist-1",
            b"salt",
            b"nonce",
            req_nonce,
            sealed,
            &carols,
        )
        .expect("a member offering under their own identity");
    }

    /// An offer addressed to one device must not verify on another, however well it is
    /// signed: the requester is bound into the transcript, and each verifier supplies its own.
    #[test]
    fn a_history_offer_addressed_elsewhere_does_not_verify_here() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob = Client::new(DOM, "bob", "laptop").unwrap();
        let carol = Client::new(DOM, "carol", "phone").unwrap();
        establish(&alice, GID, &[&bob, &carol]);

        let sig = alice
            .sign_history_offer(
                GID,
                "conv-1",
                1,
                "mimi://test.example/d/bob/laptop",
                "hist-1",
                b"salt",
                b"nonce",
                b"nonce-0123456789",
                b"sealed",
            )
            .unwrap();

        assert!(
            carol
                .verify_history_offer(
                    GID,
                    "conv-1",
                    1,
                    "mimi://test.example/d/alice/phone",
                    "hist-1",
                    b"salt",
                    b"nonce",
                    b"nonce-0123456789",
                    b"sealed",
                    &sig,
                )
                .is_err(),
            "Carol opening an offer sealed and signed for Bob",
        );
    }

    /// A request signature can never be replayed as an offer signature. The labels differ, so
    /// the transcripts differ, so the signature does not carry across.
    #[test]
    fn request_and_offer_signatures_are_not_interchangeable() {
        let alice = Client::new(DOM, "alice", "phone").unwrap();
        let bob = Client::new(DOM, "bob", "laptop").unwrap();
        establish(&alice, GID, &[&bob]);

        let nonce = b"nonce-0123456789";
        let request_sig = bob.sign_history_request(GID, "conv-1", 1, nonce).unwrap();
        assert!(
            bob.verify_history_offer(
                GID,
                "conv-1",
                1,
                "mimi://test.example/d/bob/laptop",
                "hist-1",
                b"salt",
                b"nonce",
                nonce,
                b"sealed",
                &request_sig,
            )
            .is_err(),
            "a request signature accepted as an offer would defeat the domain separation",
        );
    }

    // An outsider cannot derive the call key — they do not have the group at all. This is
    // the property that keeps the SERVER out of the call: it relays the signalling and
    // cannot read a byte of it.
    #[test]
    fn an_outsider_cannot_derive_the_call_key() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        let mallory = Client::new(DOM, "mallory", "dev-m").unwrap();
        establish(&alice, GID, &[&bob]);

        assert!(mallory
            .export_secret(GID, "pheme-call-v1", b"call-1", 32)
            .is_err());
    }

    // The exporter is bound to the CURRENT epoch, so a membership change moves it. The call
    // layer must therefore pin the epoch it derived at and derive once per call, not per
    // message — this test is the reason that rule exists.
    #[test]
    fn the_call_key_changes_when_the_epoch_does() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let before = alice
            .export_secret(GID, "pheme-call-v1", b"call-1", 32)
            .unwrap();

        // Somebody signs in on a new device: a Commit, and a new epoch.
        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        let add = alice
            .stage_add(GID, &[carol.key_package().unwrap()])
            .unwrap();
        alice.commit_accepted(GID).unwrap();
        bob.apply_commit(GID, &add.commit).unwrap();

        let after = alice
            .export_secret(GID, "pheme-call-v1", b"call-1", 32)
            .unwrap();
        assert_ne!(
            before, after,
            "the exporter is per-epoch; a caller that re-derives mid-call would desync",
        );
        // And both members still agree with each other at the new epoch.
        assert_eq!(
            after,
            bob.export_secret(GID, "pheme-call-v1", b"call-1", 32)
                .unwrap()
        );
    }

    // Deriving must not disturb the group: it is a read. If it consumed or advanced
    // anything, placing a call would corrupt the chat.
    #[test]
    fn exporting_does_not_touch_the_group() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let epoch_before = alice.epoch(GID).unwrap();
        for _ in 0..5 {
            alice
                .export_secret(GID, "pheme-call-v1", b"call-1", 32)
                .unwrap();
        }
        assert_eq!(alice.epoch(GID).unwrap(), epoch_before);

        // Chat still works in both directions afterwards.
        let ct = alice.encrypt(GID, b"still fine").unwrap();
        assert_eq!(
            bob.decrypt(GID, &ct).body().as_deref(),
            Some(&b"still fine"[..])
        );
    }

    #[test]
    fn identity_is_domain_qualified_and_splits_on_the_user() {
        let id = identity("a.example", "507f1f77bcf86cd799439011", "a-b-c-uuid");
        assert_eq!(
            id,
            b"mimi://a.example/d/507f1f77bcf86cd799439011/a-b-c-uuid"
        );
        // user_of yields the qualified USER identifier, stable across a person's devices.
        assert_eq!(
            user_of(&id),
            b"mimi://a.example/u/507f1f77bcf86cd799439011".to_vec()
        );
        // And it matches what user_key builds for removal targets.
        assert_eq!(
            user_of(&id),
            user_key("a.example", "507f1f77bcf86cd799439011").into_bytes()
        );
    }

    #[test]
    fn same_user_on_different_hosts_is_distinct() {
        // The whole reason for qualification: alice@a and alice@b are two people.
        let a = user_of(&identity("a.example", "alice", "d1"));
        let b = user_of(&identity("b.example", "alice", "d1"));
        assert_ne!(a, b);
    }

    #[test]
    fn a_credential_that_does_not_parse_falls_back_to_itself() {
        // Never resolves to the empty user, whatever the bytes are.
        assert_eq!(user_of(b"not-a-mimi-uri"), b"not-a-mimi-uri".to_vec());
    }

    // THE PROPERTY THE WHOLE PREVIEW FEATURE RESTS ON.
    //
    // A notification preview decrypts in a second context — a service worker, an iOS
    // NotificationServiceExtension, an Android background isolate — while the real client sits
    // untouched on disk. MLS keys are single-use, so the obvious fear is that previewing a
    // message burns it and the app renders a blank forever.
    //
    // It does not, and this is why: "decrypts exactly once" is a property of a COPY of the
    // state, not a global fact. The preview client is imported from a snapshot and dropped
    // without exporting, so the real client still holds an unconsumed key for that message.
    //
    // If this test ever fails, the read-only design is broken and previews must be turned off:
    // the symptom in production would be silent, permanent message loss.
    #[test]
    fn a_preview_decrypt_leaves_the_real_client_able_to_read_the_message() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let ct = alice.encrypt(GID, b"the quick brown fox").unwrap();

        // Bob's device is asleep. The extension wakes with a snapshot of his state.
        let snapshot = bob.export_state().unwrap();
        let preview = PreviewClient::import_state(&snapshot).unwrap();
        assert_eq!(
            preview.decrypt(GID, &ct).body().as_deref(),
            Some(&b"the quick brown fox"[..]),
            "the preview must be able to read the message it is previewing",
        );
        // The notification is shown; the extension dies. Nothing was written back.
        drop(preview);

        // Bob opens the app. The message must still decrypt, for real, into the transcript.
        assert_eq!(
            bob.decrypt(GID, &ct).body().as_deref(),
            Some(&b"the quick brown fox"[..]),
            "the real client lost the message: previewing consumed the key it needed, which \
             means every previewed message would render blank in the app",
        );
    }

    // The preview must never move the epoch, even in the copy it throws away.
    //
    // Client::decrypt merges a staged Commit inline. If a preview did that, it would advance an
    // epoch in state that is then discarded — and worse, invite somebody to "fix" the resulting
    // inconsistency by persisting it, which is the two-writer race the single-client rule exists
    // to prevent. A preview declines Commits entirely: there is nothing in one to show a user.
    #[test]
    fn a_preview_refuses_a_commit_and_leaves_the_epoch_alone() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let carol = Client::new(DOM, "carol", "dev-c").unwrap();
        let add = alice
            .stage_add(GID, &[carol.key_package().unwrap()])
            .unwrap();
        alice.commit_accepted(GID).unwrap();

        let snapshot = bob.export_state().unwrap();
        let preview = PreviewClient::import_state(&snapshot).unwrap();
        assert_eq!(
            preview.decrypt(GID, &add.commit).unwrap(),
            None,
            "a Commit is not a message and has no preview: it must come back as nothing",
        );
        drop(preview);

        // Bob has still never seen the Commit, so applying it for real must work.
        bob.apply_commit(GID, &add.commit).unwrap();
        assert_eq!(bob.member_identities(GID).unwrap().len(), 3);

        // And a message in the NEW epoch decrypts, proving the ratchet is where it should be.
        let ct = alice.encrypt(GID, b"after carol joined").unwrap();
        assert_eq!(
            bob.decrypt(GID, &ct).body().as_deref(),
            Some(&b"after carol joined"[..])
        );
    }

    // Previewing the same message twice — two devices, or a retried push — must also be safe.
    #[test]
    fn repeated_previews_are_harmless() {
        let alice = Client::new(DOM, "alice", "dev-a").unwrap();
        let bob = Client::new(DOM, "bob", "dev-b").unwrap();
        establish(&alice, GID, &[&bob]);

        let ct = alice.encrypt(GID, b"delivered twice").unwrap();
        let snapshot = bob.export_state().unwrap();

        for _ in 0..3 {
            let preview = PreviewClient::import_state(&snapshot).unwrap();
            assert_eq!(
                preview.decrypt(GID, &ct).body().as_deref(),
                Some(&b"delivered twice"[..]),
            );
        }

        assert_eq!(
            bob.decrypt(GID, &ct).body().as_deref(),
            Some(&b"delivered twice"[..])
        );
    }
}

#[cfg(test)]
mod credential_shape_tests {
    use super::*;

    // The credential is a four-segment path that `user_of` reads back by
    // splitting on '/'. A component carrying the separator made a credential
    // resolve to a DIFFERENT user than it names — the identity removal and
    // roster comparison key on.
    #[test]
    fn a_slashed_component_is_refused() {
        for (domain, user, device) in [
            ("a.example", "victim/junk", "dev-1"),
            ("a.example", "user-1", "dev/1"),
            ("a.example/d/victim", "user-1", "dev-1"),
        ] {
            assert!(
                Client::new(domain, user, device).is_err(),
                "accepted a credential component containing '/': {domain} {user} {device}"
            );
        }
    }

    #[test]
    fn blank_and_whitespace_components_are_refused() {
        assert!(Client::new("", "u", "d").is_err());
        assert!(Client::new("a.example", "", "d").is_err());
        assert!(Client::new("a.example", "u", "").is_err());
        assert!(Client::new("a.example", "u ser", "d").is_err());
        assert!(Client::new("a.example", "u\nser", "d").is_err());
    }

    // A well-formed credential still resolves to its user.
    #[test]
    fn a_well_formed_credential_still_resolves() {
        let id = identity("a.example", "507f1f77bcf86cd799439011", "a-b-c-uuid");
        assert_eq!(
            user_of(&id),
            b"mimi://a.example/u/507f1f77bcf86cd799439011".to_vec()
        );
    }

    // A credential with extra segments falls back to its whole self rather than
    // resolving to a truncated user that may name somebody else.
    #[test]
    fn an_overlong_credential_does_not_resolve_to_a_truncated_user() {
        let id = b"mimi://a.example/d/victim/junk/dev-1".to_vec();
        assert_eq!(
            user_of(&id),
            id,
            "an ambiguous credential resolved to a user"
        );
    }
}
