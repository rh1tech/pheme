// The wire bodies of the device-to-device history handoff, and the rules for refusing one.
//
// ------------------------------------------------------------------------------------------------
// WHY THIS IS NOT JUST JSON
//
// A device that joins a conversation holds none of what was said before it arrived. Another device
// of the same account hands the transcript over: sealed under the group's exporter secret, uploaded
// as an opaque blob, pointed at by a control message.
//
// The seal proves the sender is A MEMBER of the group. It cannot prove WHICH member, because every
// member derives the same exporter secret — that is what makes it usable at all. So under
// exporter-AEAD alone any member can:
//
//   * mint a request that CLAIMS to come from a device that never asked, and so make a co-member
//     seal a whole conversation to a key derived for somebody else's identity;
//   * mint an offer that claims to come from another member, stuffed with a transcript of their own
//     invention — every message in a conversation, attributed to whoever they like, landing on a
//     fresh device that has nothing to compare it against.
//
// v2 authenticates the signer with a SENDER-authenticated layer over the same bytes: the member signs a
// canonical transcript with the same MLS leaf signature key the group already authenticates it by,
// and the receiver verifies that signature against the leaf key the ratchet tree holds for the
// identity being claimed. The transcript is built in Rust (crates/pheme-mls/src/history.rs), so this
// app and the web app canonicalise identically by construction rather than by two hand-written
// encoders kept in step by hope.
//
// v1 bodies carried no signature. They are REFUSED, not tolerated: accepting one would leave the
// forgery above wide open, and a fallback that quietly downgrades is the same as no signature at
// all. A valid member can still sign invented history as THEMSELVES, so the orchestration accepts a
// provider only when its domain-qualified account matches the requester. A device that gets no
// answer simply re-asks.
// ------------------------------------------------------------------------------------------------
//
// MUST stay in step with web/src/lib/historyHandoff.ts — these bodies cross between the two clients.

import 'dart:convert';
import 'dart:typed_data';

import 'attribution.dart';

/// The wire version these bodies belong to. v1 was unsigned and is refused.
const int historyVersion = 2;

/// "I hold none of this conversation's past — can someone who does send it?"
class HistoryRequestBody {
  const HistoryRequestBody({
    required this.id,
    required this.epoch,
    required this.nonce,
    required this.sig,
  });

  /// The requesting device's MLS credential identity.
  final String id;

  /// The epoch the requester was at, so the offerer derives the matching exporter secret.
  final int epoch;

  /// base64. Fresh per request; quoted back by the offer, which ties the answer to the question.
  final String nonce;

  /// base64 signature over the canonical request transcript, by the requester's MLS leaf key.
  final String sig;

  Map<String, dynamic> toJson() => {
    'v': historyVersion,
    'id': id,
    'epoch': epoch,
    'nonce': nonce,
    'sig': sig,
  };
}

/// "Your history is sealed and waiting at this id."
class HistoryOfferBody {
  const HistoryOfferBody({
    required this.from,
    required this.to,
    required this.epoch,
    required this.historyId,
    required this.salt,
    required this.nonce,
    required this.reqNonce,
    required this.sig,
  });

  /// The OFFERING device's MLS credential identity — whose leaf key [sig] must verify against.
  final String from;

  /// The requesting device's MLS credential identity. An offer is for exactly one device.
  final String to;

  final int epoch;
  final String historyId;

  /// base64 AEAD salt.
  final String salt;

  /// base64 AEAD nonce.
  final String nonce;

  /// base64 — the nonce from the request this answers.
  final String reqNonce;

  /// base64 signature over the canonical offer transcript, by the offerer's MLS leaf key.
  final String sig;

  Map<String, dynamic> toJson() => {
    'v': historyVersion,
    'from': from,
    'to': to,
    'epoch': epoch,
    'historyId': historyId,
    'salt': salt,
    'nonce': nonce,
    'reqNonce': reqNonce,
    'sig': sig,
  };
}

String _str(Map raw, String field) {
  final value = raw[field];
  return value is String ? value : '';
}

Map? _decode(Uint8List bytes) {
  try {
    final parsed = jsonDecode(utf8.decode(bytes));
    return parsed is Map ? parsed : null;
  } on Object {
    return null;
  }
}

/// Reads a history REQUEST off the wire. Null for anything that is not a well-formed **v2** body.
///
/// A v1 body — no `v`, no `sig` — is null here and is therefore never answered. That is the point:
/// answering it means sealing a transcript to a key derived for an identity that may never have
/// asked for it.
HistoryRequestBody? parseRequestBody(Uint8List bytes) {
  final raw = _decode(bytes);
  if (raw == null || raw['v'] != historyVersion) return null;
  final id = _str(raw, 'id');
  final nonce = _str(raw, 'nonce');
  final sig = _str(raw, 'sig');
  final epoch = raw['epoch'];
  if (id.isEmpty || nonce.isEmpty || sig.isEmpty) return null;
  if (epoch is! int || epoch < 0) return null;
  // Not a credential we can resolve to a user; there is nothing to compare against the envelope's
  // authenticated poster, and nothing the ratchet tree holds a leaf for.
  if (userOfIdentity(id).isEmpty) return null;
  return HistoryRequestBody(id: id, epoch: epoch, nonce: nonce, sig: sig);
}

/// Reads a history OFFER off the wire. Null for anything that is not a well-formed **v2** body.
HistoryOfferBody? parseOfferBody(Uint8List bytes) {
  final raw = _decode(bytes);
  if (raw == null || raw['v'] != historyVersion) return null;
  final from = _str(raw, 'from');
  final to = _str(raw, 'to');
  final historyId = _str(raw, 'historyId');
  final salt = _str(raw, 'salt');
  final nonce = _str(raw, 'nonce');
  final reqNonce = _str(raw, 'reqNonce');
  final sig = _str(raw, 'sig');
  final epoch = raw['epoch'];
  if (from.isEmpty ||
      to.isEmpty ||
      historyId.isEmpty ||
      salt.isEmpty ||
      nonce.isEmpty ||
      reqNonce.isEmpty ||
      sig.isEmpty) {
    return null;
  }
  if (epoch is! int || epoch < 0) return null;
  if (userOfIdentity(from).isEmpty || userOfIdentity(to).isEmpty) return null;
  return HistoryOfferBody(
    from: from,
    to: to,
    epoch: epoch,
    historyId: historyId,
    salt: salt,
    nonce: nonce,
    reqNonce: reqNonce,
    sig: sig,
  );
}

/// Whether the identity a control body CLAIMS matches the poster the server authenticated.
///
/// The server is untrusted for message CONTENT, but it does authenticate the session that posted a
/// message and stamps its user id on the envelope. That makes the envelope a second, independent
/// witness: an insider forging a body in somebody else's name has to get past the MLS signature AND
/// post it from that person's account. Checking both costs one comparison.
///
/// A blank [posterId] — an older server, or a listing that does not carry one — is not a failure.
/// The MLS signature is the check that must hold; this one strengthens it where it is available.
bool posterMatchesClaim(String claimedIdentity, String posterId) {
  if (posterId.isEmpty) return true;
  final claimed = userOfIdentity(claimedIdentity);
  return claimed.isNotEmpty && claimed == posterId;
}
