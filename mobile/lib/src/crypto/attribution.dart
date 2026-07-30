// Who actually wrote a message, and how well we know it.
//
// ------------------------------------------------------------------------------------------------
// THE PROBLEM THIS FILE EXISTS FOR
//
// A conversation message arrives with a `senderId` on its envelope. That field is written by the
// SERVER, which in MLS is the untrusted Delivery Service: it relays opaque bytes and can put any
// user id it likes on them. Rendering a message under that name means end-to-end encryption bought
// confidentiality and nothing else — nobody can read your conversation, and anybody who runs the
// server can write to it as you.
//
// MLS already knows the answer. Every application message is signed by the sending leaf's key, the
// Rust core verifies that signature against the group's ratchet tree, and the credential it hands
// back — `mimi://<domain>/d/<user>/<device>` — is authenticated. `MlsSession.decrypt` carries it
// out; this is where it is recorded, transferred and reduced to something a bubble can render.
// ------------------------------------------------------------------------------------------------
//
// The three kinds are NOT equally trustworthy, and the whole point of the type is that they cannot
// be confused for one another:
//
//   * `mls` — this device decrypted the message itself and MLS authenticated the signer. Also what
//     a message WE sent gets: we wrote it.
//   * `relayed` — imported through the device-to-device history handoff. Another device of the same
//     account signed the whole transfer with its leaf key. The per-message author inside it is
//     still that device's word rather than a signature this device checked. Never presented as
//     verified.
//   * `legacy` — a cache entry written before any of this existed. There is no sender in it and
//     there never can be (the MLS key is long gone), so the envelope's `senderId` is all there is.
//     A compatibility fallback, explicitly unverified, never labelled otherwise.
//
// MUST stay in step with web/src/lib/attribution.ts: the `_s` / `_r` fields below travel between the
// two clients inside a history handoff and inside a key backup.

import 'dart:convert';
import 'dart:typed_data';

import 'chat_content.dart';

/// The sender credential of a cached entry: `mimi://<domain>/d/<user>/<device>`.
const String senderField = '_s';

/// The credential of the member that handed this entry over, when it was imported rather than read.
const String relayField = '_r';

/// How a cached message's author was established.
enum AttributionKind { mls, relayed, legacy }

/// The author of a cached message, and how far the claim can be trusted.
class Attribution {
  const Attribution._(this.kind, this.identity, this.userId, this.relayedBy);

  /// Nothing is known beyond what the envelope says.
  static const legacy = Attribution._(AttributionKind.legacy, '', '', '');

  /// This device authenticated the signer (or wrote the message itself).
  factory Attribution.authenticated(String identity) {
    final user = userOfIdentity(identity);
    if (identity.isEmpty || user.isEmpty) return legacy;
    return Attribution._(AttributionKind.mls, identity, user, '');
  }

  /// Another device of this account handed this over; the author inside is that device's claim.
  factory Attribution.relayed(String identity, String offerer) {
    final user = userOfIdentity(identity);
    if (identity.isEmpty || user.isEmpty || offerer.isEmpty) return legacy;
    return Attribution._(AttributionKind.relayed, identity, user, offerer);
  }

  final AttributionKind kind;

  /// The full credential of the signer, or '' for a legacy entry.
  final String identity;

  /// The bare, host-local user id the roster is keyed by. '' for a legacy entry.
  final String userId;

  /// The credential of the member that relayed this entry, for `relayed` only.
  final String relayedBy;

  bool get isLegacy => kind == AttributionKind.legacy;

  @override
  bool operator ==(Object other) =>
      other is Attribution &&
      other.kind == kind &&
      other.identity == identity &&
      other.userId == userId &&
      other.relayedBy == relayedBy;

  @override
  int get hashCode => Object.hash(kind, identity, userId, relayedBy);
}

/// The bare user id inside `mimi://<domain>/d/<user>/<device>`, or '' if it is not that form.
///
/// Deliberately BARE rather than domain-qualified: the roster, the membership list and the
/// envelope's `senderId` are all keyed by the host-local user id, and this is what gets compared
/// against them. Distinctness across hosts is carried by the full credential.
String userOfIdentity(String identity) {
  if (!identity.startsWith('mimi://')) return '';
  final parts = identity.substring('mimi://'.length).split('/');
  if (parts.length != 4 || parts[1] != 'd') return '';
  return parts[2];
}

/// The home domain inside a device credential, or '' if it is not that form.
String domainOfIdentity(String identity) {
  if (!identity.startsWith('mimi://')) return '';
  final parts = identity.substring('mimi://'.length).split('/');
  if (parts.length != 4 || parts[1] != 'd') return '';
  return parts[0];
}

/// Whether two device credentials belong to the same canonical account.
///
/// Both domain and user are required: user ids are host-local in a federated conversation.
bool sameAccountIdentities(String left, String right) {
  final leftUser = userOfIdentity(left);
  final rightUser = userOfIdentity(right);
  final leftDomain = domainOfIdentity(left);
  final rightDomain = domainOfIdentity(right);
  return leftUser.isNotEmpty &&
      leftDomain.isNotEmpty &&
      leftUser == rightUser &&
      leftDomain == rightDomain;
}

/// A cache entry read back: its content, and how far its authorship can be trusted.
class CachedEntry {
  const CachedEntry(this.content, this.attribution);

  final ChatContent content;
  final Attribution attribution;
}

/// The serialised cache entry for a message: its content plus who signed it.
///
/// The sender rides INSIDE the entry rather than in a table beside it, and that is what makes the
/// history handoff and the key backup carry provenance for free — both copy these strings verbatim
/// (see ChatCache.exportAllContents), so a device that imports a transcript imports the authorship
/// with it instead of re-deriving it from whatever the server says.
///
/// `_s` and `_r` are extra fields on the same object, so an older build reading a newer cache still
/// finds `body`, `replyTo` and `photos` exactly where it expects them.
String encodeCacheEntry(ChatContent content, Attribution attribution) {
  final json = <String, dynamic>{'body': content.body};
  final replyTo = content.replyTo;
  if (replyTo != null && replyTo.isNotEmpty) json['replyTo'] = replyTo;
  if (content.photos.isNotEmpty) {
    json['photos'] = content.photos.map((p) => p.toJson()).toList();
  }
  if (!attribution.isLegacy) json[senderField] = attribution.identity;
  if (attribution.kind == AttributionKind.relayed) {
    json[relayField] = attribution.relayedBy;
  }
  return jsonEncode(json);
}

/// Parses a serialised cache entry.
///
/// An entry with no `_s` is LEGACY, not an error: every message anybody decrypted before this
/// existed is one, and they are the only copy of that plaintext there will ever be.
CachedEntry decodeCacheEntry(String serialised) {
  final content = parseContent(Uint8List.fromList(utf8.encode(serialised)));
  var sender = '';
  var relayedBy = '';
  try {
    final raw = jsonDecode(serialised);
    if (raw is Map) {
      final s = raw[senderField];
      final r = raw[relayField];
      if (s is String) sender = s;
      if (r is String) relayedBy = r;
    }
  } on FormatException {
    // A bare-body entry from a much older build. The body is still what parseContent returned.
  }
  if (sender.isEmpty) return CachedEntry(content, Attribution.legacy);
  if (relayedBy.isNotEmpty) {
    return CachedEntry(content, Attribution.relayed(sender, relayedBy));
  }
  return CachedEntry(content, Attribution.authenticated(sender));
}

/// Marks an entry as having arrived through the history handoff.
///
/// Applied at IMPORT, over what the offering member sent, so an offerer cannot pass its transcript
/// off as something the receiving device authenticated for itself. An entry with no sender at all
/// stays legacy: there is nothing to relay a claim about.
String markRelayed(String serialised, String offerer) {
  if (offerer.isEmpty) return serialised;
  final entry = decodeCacheEntry(serialised);
  if (entry.attribution.isLegacy) return serialised;
  return encodeCacheEntry(
    entry.content,
    Attribution.relayed(entry.attribution.identity, offerer),
  );
}

/// What a bubble needs to know about a message's author.
class AuthorView {
  const AuthorView({
    required this.userId,
    required this.verified,
    required this.tampered,
  });

  /// The user id to render the message under. From MLS wherever there is an MLS answer; from the
  /// envelope only for a legacy entry, which is the compatibility fallback and nothing more.
  final String userId;

  /// True only for `mls`: this device authenticated the signer itself.
  final bool verified;

  /// The envelope names a DIFFERENT user than the cryptography does.
  ///
  /// Not a rendering detail — it is the attack this whole file is about, caught. The UI must say so
  /// rather than silently pick one of the two names.
  final bool tampered;
}

/// Reduces an attribution and the envelope's claim to what to show.
///
/// [serverSenderId] is the envelope's `senderId`. It never overrides an MLS answer; it renders a
/// legacy entry, and otherwise only reveals that it disagrees.
AuthorView resolveAuthor(Attribution? attribution, String serverSenderId) {
  final a = attribution ?? Attribution.legacy;
  if (a.isLegacy) {
    return AuthorView(userId: serverSenderId, verified: false, tampered: false);
  }
  final tampered = serverSenderId.isNotEmpty && serverSenderId != a.userId;
  return AuthorView(
    userId: a.userId,
    verified: a.kind == AttributionKind.mls && !tampered,
    tampered: tampered,
  );
}

/// Whether a message is our own.
///
/// Decided by the AUTHENTICATED sender wherever there is one. Left to the envelope only for a legacy
/// entry and for a message this device could not read at all — in the second case there is no
/// plaintext, so there is no signature either, and the envelope is genuinely all that exists.
bool isOwnMessage(
  Attribution? attribution,
  String serverSenderId,
  String myUserId,
) {
  if (myUserId.isEmpty) return false;
  if (attribution != null && !attribution.isLegacy) {
    return attribution.userId == myUserId;
  }
  return serverSenderId == myUserId;
}
