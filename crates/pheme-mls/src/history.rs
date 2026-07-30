//! Signed device-to-device history handoff.
//!
//! A device that joins an existing conversation holds none of what was said before it arrived —
//! MLS gives a new leaf no access to the past. Another device of the same account can hand it over:
//! sealed under the group's exporter secret, uploaded as an opaque blob, pointed at by a control
//! message.
//!
//! # Why the exporter secret is not enough
//!
//! The seal proves the sender is **a member of the group**. It does not prove **which** member,
//! because every member derives the same exporter secret — that is what makes it usable at all.
//! So under exporter-AEAD alone:
//!
//!   * any member can mint a history request that *claims* to come from a device that never asked,
//!     and any member can answer one addressed to somebody else;
//!   * any member can mint an offer that claims to come from another member, and stuff it with a
//!     transcript of their own invention — every message in a conversation, attributed to whoever
//!     they like, arriving on a fresh device that has nothing to compare it against.
//!
//! The cryptographic fix is a second, *sender*-authenticated layer over the same bytes: the member signs a
//! canonical description of what it is asking for or offering with the SAME leaf signature key MLS
//! already authenticates it by, and the receiver verifies that signature against the leaf key the
//! group's own ratchet tree holds for the identity being claimed. A member impersonating another
//! member does not have that member's private key, so the signature does not verify and the
//! transfer is refused. That proves WHO offered a transcript, not that every per-message body in it
//! is historically true: any participant can sign invented history as themselves. The web and
//! mobile orchestration therefore accepts offers only from another device of the requester's same
//! domain-qualified account.
//!
//! # The canonical transcript
//!
//! Signatures are over bytes, so the two ends must build **exactly** the same bytes from the same
//! claim or every verification fails (or, far worse, two different claims collide onto one
//! transcript and a signature is valid for something it was never given). Hence:
//!
//!   * **Domain separation.** Every transcript opens with a length-prefixed label naming what is
//!     being signed. A request transcript can therefore never be replayed as an offer transcript,
//!     and neither can be replayed as an MLS-internal signature — those are built by OpenMLS with
//!     its own labels and framing and never look like this.
//!   * **Version in the label.** `…/v2`. The v1 protocol had no signature at all; changing the
//!     label means a v3 signature can never be accepted as a v2 one, whatever the JSON says.
//!   * **Length-prefixed fields, and a field count.** Every field is `u32` big-endian length then
//!     bytes, so no two different field lists can run into each other and produce identical bytes.
//!     Concatenating `("ab","c")` and `("a","bc")` is exactly the confusion this prevents.
//!   * **Fixed-width integers.** An epoch goes in as 8 big-endian bytes, never as its decimal text,
//!     so a JavaScript number and a Dart `int` and a Rust `u64` cannot disagree about formatting.
//!
//! **This is the one implementation.** The web app reaches it through the WASM binding and the
//! Flutter app through the FRB binding, so "web and mobile canonicalise identically" is structural
//! rather than a pair of hand-written encoders kept in step by hope. The golden vectors in the
//! tests below pin the bytes so a change to this file that silently alters them cannot pass.

use sha2::{Digest, Sha256};

/// Domain-separation label for a history REQUEST. v2 is the first signed version.
pub const REQUEST_LABEL: &str = "pheme/mls/history-request/v2";

/// Domain-separation label for a history OFFER. v2 is the first signed version.
pub const OFFER_LABEL: &str = "pheme/mls/history-offer/v2";

/// The wire version these transcripts belong to. A body carrying anything else is refused: v1 was
/// unsigned, and accepting it would leave the forgery this module exists to close wide open.
pub const VERSION: u32 = 2;

/// SHA-256 of the sealed history blob — what an offer commits to, so the bytes fetched from the
/// server are the bytes the offering member signed for.
pub fn digest(bytes: &[u8]) -> Vec<u8> {
    Sha256::digest(bytes).to_vec()
}

/// `len(label) ‖ label ‖ count ‖ (len(field) ‖ field)*`, all lengths `u32` big-endian.
fn transcript(label: &str, fields: &[&[u8]]) -> Vec<u8> {
    let mut out =
        Vec::with_capacity(8 + label.len() + fields.iter().map(|f| f.len() + 4).sum::<usize>());
    out.extend_from_slice(&(label.len() as u32).to_be_bytes());
    out.extend_from_slice(label.as_bytes());
    out.extend_from_slice(&(fields.len() as u32).to_be_bytes());
    for field in fields {
        out.extend_from_slice(&(field.len() as u32).to_be_bytes());
        out.extend_from_slice(field);
    }
    out
}

/// The bytes a requester signs: "*I*, this leaf, am asking *this* group at *this* epoch for the
/// history of *this* conversation, under *this* nonce."
///
/// The nonce is what an offer later quotes back, so an offer can be tied to the request it answers
/// instead of being a free-floating blob anyone may push at a device.
pub fn request_transcript(
    conversation_id: &str,
    group_id: &[u8],
    epoch: u64,
    requester: &str,
    nonce: &[u8],
) -> Vec<u8> {
    transcript(
        REQUEST_LABEL,
        &[
            conversation_id.as_bytes(),
            group_id,
            &epoch.to_be_bytes(),
            requester.as_bytes(),
            nonce,
        ],
    )
}

/// The bytes an offerer signs.
///
/// Everything the receiver will act on is in here. In particular `ciphertext_digest`: without it a
/// signature would cover only a pointer (`history_id`) and the server — which stores the blob — could
/// swap the bytes behind a perfectly valid signature.
#[allow(clippy::too_many_arguments)]
pub fn offer_transcript(
    conversation_id: &str,
    group_id: &[u8],
    epoch: u64,
    offerer: &str,
    requester: &str,
    history_id: &str,
    salt: &[u8],
    nonce: &[u8],
    request_nonce: &[u8],
    ciphertext_digest: &[u8],
) -> Vec<u8> {
    transcript(
        OFFER_LABEL,
        &[
            conversation_id.as_bytes(),
            group_id,
            &epoch.to_be_bytes(),
            offerer.as_bytes(),
            requester.as_bytes(),
            history_id.as_bytes(),
            salt,
            nonce,
            request_nonce,
            ciphertext_digest,
        ],
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    fn hex(bytes: &[u8]) -> String {
        bytes.iter().map(|b| format!("{b:02x}")).collect()
    }

    // GOLDEN VECTOR — the cross-platform contract.
    //
    // Web (web/src/lib/mls.ts) and mobile (mobile/lib/src/crypto/mls_service.dart) both reach this
    // function through their bindings, and both have a test asserting this same hex. If this
    // assertion has to be updated, every already-deployed client stops being able to verify the
    // new ones' signatures — so updating it means bumping the label to /v3, not editing the string.
    #[test]
    fn request_transcript_is_pinned() {
        let bytes = request_transcript(
            "conv-1",
            b"grp-1",
            7,
            "mimi://test.example/d/alice/phone",
            &[0x01, 0x02, 0x03, 0x04],
        );
        assert_eq!(
            hex(&bytes),
            concat!(
                "0000001c",                                                 // label length: 28
                "7068656d652f6d6c732f686973746f72792d726571756573742f7632", // the label
                "00000005",                                                 // five fields
                "00000006636f6e762d31",                                     // "conv-1"
                "000000056772702d31",                                       // "grp-1"
                "000000080000000000000007", // epoch 7, 8 bytes big-endian
                "00000021",                 // requester: 33 bytes
                "6d696d693a2f2f746573742e6578616d706c652f642f616c6963652f70686f6e65",
                "0000000401020304", // nonce
            )
        );
    }

    #[test]
    fn offer_transcript_is_pinned() {
        let bytes = offer_transcript(
            "conv-1",
            b"grp-1",
            7,
            "mimi://test.example/d/bob/laptop",
            "mimi://test.example/d/alice/phone",
            "hist-1",
            &[0xaa],
            &[0xbb],
            &[0x01, 0x02, 0x03, 0x04],
            &digest(b"sealed"),
        );
        assert_eq!(
            hex(&bytes),
            concat!(
                "0000001a", // label length: 26
                "7068656d652f6d6c732f686973746f72792d6f666665722f7632",
                "0000000a", // ten fields
                "00000006636f6e762d31",
                "000000056772702d31",
                "000000080000000000000007",
                "00000020", // offerer: 32 bytes
                "6d696d693a2f2f746573742e6578616d706c652f642f626f622f6c6170746f70",
                "00000021", // requester: 33 bytes
                "6d696d693a2f2f746573742e6578616d706c652f642f616c6963652f70686f6e65",
                "00000006686973742d31", // "hist-1"
                "00000001aa",           // salt
                "00000001bb",           // nonce
                "0000000401020304",     // request nonce
                "00000020",             // digest: 32 bytes
                "c9d0036bed6744bcdf692fc980d8717d7e5f5a4f4e8266b4a84982602fb1cd09",
            )
        );
    }

    /// The whole reason fields are length-prefixed: without it, moving a byte from one field to the
    /// next would produce identical transcripts, and a signature over one claim would be valid for
    /// the other.
    #[test]
    fn field_boundaries_cannot_be_shifted() {
        let a = request_transcript("ab", b"c", 1, "id", b"n");
        let b = request_transcript("a", b"bc", 1, "id", b"n");
        assert_ne!(a, b);
    }

    /// A request transcript and an offer transcript can never collide, whatever the fields.
    #[test]
    fn the_two_kinds_are_domain_separated() {
        let req = request_transcript("c", b"g", 1, "id", b"n");
        let off = offer_transcript("c", b"g", 1, "id", "id", "h", b"s", b"n", b"n", b"d");
        assert_ne!(req, off);
        assert_ne!(&req[..4], &off[..4]); // even the label lengths differ
    }
}
