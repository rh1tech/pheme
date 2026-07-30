//! The canonical history transcripts, checked against the SHARED cross-platform vectors.
//!
//! The unit tests in `src/history.rs` pin these bytes inline, which stops this crate changing them
//! by accident. This file does something different and complementary: it reads the same values out
//! of `test/fixtures/mls_history_vectors.json`, the file the web and Flutter suites also read.
//!
//! That is what makes "web and mobile canonicalise identically" checkable rather than asserted.
//! Both clients reach THIS function through their bindings, so the transcript is structurally one
//! implementation — and the fixture is the single written-down statement of what that implementation
//! produces, with each suite asserting the half it actually owns:
//!
//!   * here, the transcript bytes and the blob digest;
//!   * in web/test/mlsHistory.test.ts and mobile/test/unit/history_handoff_test.dart, the control
//!     message body those bytes are drawn from.
//!
//! Changing a value in that file means every already-deployed client stops being able to verify the
//! new ones' signatures. The way to make that change is a `/v3` label, never an edit.

use pheme_mls::history::{
    digest, offer_transcript, request_transcript, OFFER_LABEL, REQUEST_LABEL, VERSION,
};
use serde_json::Value;

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

fn unhex(s: &str) -> Vec<u8> {
    (0..s.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(&s[i..i + 2], 16).expect("hex"))
        .collect()
}

fn vectors() -> Value {
    let path = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../test/fixtures/mls_history_vectors.json"
    );
    let raw = std::fs::read_to_string(path).expect("the shared vectors file must be readable");
    serde_json::from_str(&raw).expect("the shared vectors file must be valid JSON")
}

fn s<'a>(v: &'a Value, key: &str) -> &'a str {
    v.get(key)
        .and_then(Value::as_str)
        .unwrap_or_else(|| panic!("vectors: missing string {key}"))
}

fn u(v: &Value, key: &str) -> u64 {
    v.get(key)
        .and_then(Value::as_u64)
        .unwrap_or_else(|| panic!("vectors: missing number {key}"))
}

#[test]
fn the_labels_and_version_match_the_shared_vectors() {
    let v = vectors();
    assert_eq!(u(&v, "version"), VERSION as u64);
    assert_eq!(s(&v, "requestLabel"), REQUEST_LABEL);
    assert_eq!(s(&v, "offerLabel"), OFFER_LABEL);
}

#[test]
fn a_request_transcript_matches_the_shared_vector() {
    let v = vectors();
    let r = &v["request"];
    let bytes = request_transcript(
        s(r, "conversationId"),
        s(r, "groupId").as_bytes(),
        u(r, "epoch"),
        s(r, "requester"),
        &unhex(s(r, "nonceHex")),
    );
    assert_eq!(
        hex(&bytes),
        s(r, "transcriptHex"),
        "the canonical request transcript no longer matches the cross-platform vector; \
         every deployed client would refuse the new signatures. Bump the label to /v3 instead."
    );
}

#[test]
fn an_offer_transcript_matches_the_shared_vector() {
    let v = vectors();
    let o = &v["offer"];
    let ciphertext = s(o, "ciphertextUtf8").as_bytes();
    assert_eq!(
        hex(&digest(ciphertext)),
        s(o, "ciphertextDigestHex"),
        "the blob digest is what an offer commits to; if it moves, a signed offer no longer \
         covers the bytes the server stores"
    );
    let bytes = offer_transcript(
        s(o, "conversationId"),
        s(o, "groupId").as_bytes(),
        u(o, "epoch"),
        s(o, "offerer"),
        s(o, "requester"),
        s(o, "historyId"),
        &unhex(s(o, "saltHex")),
        &unhex(s(o, "nonceHex")),
        &unhex(s(o, "requestNonceHex")),
        &digest(ciphertext),
    );
    assert_eq!(
        hex(&bytes),
        s(o, "transcriptHex"),
        "the canonical offer transcript no longer matches the cross-platform vector; \
         every deployed client would refuse the new signatures. Bump the label to /v3 instead."
    );
}

/// The fields a client puts in the wire BODY are exactly the fields that go into the transcript.
///
/// The two clients build those bodies themselves (this crate never sees one), so this is the seam
/// where they could drift: a body that carried a different nonce encoding, or dropped `from`, would
/// produce a signature over a claim nobody could reconstruct. Pinning the correspondence here means
/// the fixture is one contract rather than two lists that happen to sit in the same file.
#[test]
fn the_wire_bodies_carry_exactly_what_the_transcripts_bind() {
    let v = vectors();
    let r = &v["request"];
    let rb = &r["body"];
    assert_eq!(u(rb, "v"), VERSION as u64);
    assert_eq!(s(rb, "id"), s(r, "requester"));
    assert_eq!(u(rb, "epoch"), u(r, "epoch"));
    assert_eq!(s(rb, "nonce"), s(r, "nonceBase64"));

    let o = &v["offer"];
    let ob = &o["body"];
    assert_eq!(u(ob, "v"), VERSION as u64);
    assert_eq!(s(ob, "from"), s(o, "offerer"));
    assert_eq!(s(ob, "to"), s(o, "requester"));
    assert_eq!(u(ob, "epoch"), u(o, "epoch"));
    assert_eq!(s(ob, "historyId"), s(o, "historyId"));
    assert_eq!(s(ob, "reqNonce"), s(r, "nonceBase64"));
}
