// Decrypting a message in a push handler, to show what was actually said.
//
// The server ships the ciphertext — it cannot read it, and never could — and this opens it on the
// device, just long enough to draw a notification.
//
// ------------------------------------------------------------------------------------------------
// THIS RUNS WHERE THE APP IS NOT, AND SO IT NEVER WRITES.
//
// On Android this is called from the FCM background isolate: a separate Dart heap, sharing one
// process — and therefore one Rust `static CLIENT` — with the app that may be running in the
// foreground. That is exactly the situation the single-client rule exists to prevent. A handler that
// loaded the client here would swap it out from under the foreground isolate mid-operation, and the
// two would race to disk; whichever landed last would save a ratchet that had moved as one that had
// not, which is every message after that point permanently unreadable.
//
// So nothing here loads the client. `mlsDecryptPreview` takes the state blob by value, builds a
// throwaway reader from it, and drops it — it cannot reach `CLIENT` and cannot export state. The
// app's own client is untouched and still holds an unconsumed key for this message, and decrypts it
// again for real when the user opens the chat.
//
// Every failure is silent and falls back to the server's generic text. A notification that says
// "New message" is a working notification; one that never appears because a decrypt threw is not.
// ------------------------------------------------------------------------------------------------

import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../crypto/attribution.dart';
import '../crypto/chat_cache.dart';
import '../crypto/mls_store.dart';
import '../rust/api/mls.dart' as rust;
import '../rust/frb_generated.dart';

/// Whether this isolate has loaded the Rust library yet.
///
/// `RustLib.init()` runs in main(), and a background isolate does not share that — it has its own
/// memory and starts with nothing. Without this every decrypt here fails on the very first FFI
/// call, which is caught and degrades to a generic notification: previews would simply never work
/// in the background, which is the only place they matter.
bool _rustReady = false;

Future<bool> _ensureRust() async {
  if (_rustReady) return true;
  try {
    await RustLib.init();
    _rustReady = true;
  } on Object {
    // Already initialised by this isolate (the foreground case), which is a success. Anything
    // else and the decrypt below will fail on its own and fall back.
    _rustReady = true;
  }
  return _rustReady;
}

/// What a push may show: the decrypted line and the identity **MLS authenticated** as its signer.
///
/// The sender is here because a notification is titled with a NAME, and the only other source for
/// that name is the push payload — which the server composes, and the server is the untrusted
/// Delivery Service. It can attach any name to any ciphertext it relays, and a lock screen is
/// exactly where nobody looks twice.
class NotificationPreview {
  const NotificationPreview({required this.body, required this.senderUserId});

  final String body;

  /// The bare user id from the authenticated credential. Empty when it could not be established —
  /// a body served from the legacy cache, where no sender was ever recorded.
  final String senderUserId;

  /// Whether the preview must discard the payload's sender identity.
  ///
  /// A name/avatar is retained only when both sides are non-empty and exactly agree. The decrypted
  /// body is still worth showing when either identity is absent, but only under a neutral title.
  bool contradicts(String? claimedSenderId) {
    final claimed = claimedSenderId ?? '';
    return claimed.isEmpty || senderUserId.isEmpty || claimed != senderUserId;
  }
}

/// The message carried by a push, or null when there is nothing to show.
///
/// [conversationId] and [ciphertextBase64] come from the push's data payload; the ciphertext is
/// absent unless the recipient asked for previews, so a null return here is the ordinary case for
/// everybody else.
Future<NotificationPreview?> decryptNotificationPreview({
  required String conversationId,
  required String? ciphertextBase64,
  String? groupIdsCsv,
  String? messageId,
}) async {
  if (ciphertextBase64 == null || ciphertextBase64.isEmpty) return null;
  if (conversationId.isEmpty) return null;

  try {
    // A store of its own. In the background isolate the app's instance does not exist, and in the
    // foreground it belongs to a session this code must not join. Reading is safe from either:
    // the file is opened, not held, and the data key is `first_unlock` so it is readable even
    // before the user has unlocked since a reboot.
    final store = MlsStore(const FlutterSecureStorage());

    // Which groups to try. The push names them, because the ONLY other source is a mapping this
    // device writes when it opens a chat — so a freshly installed one knows nothing, and every
    // preview fell back to "New message" until the user had visited each conversation in turn.
    //
    // The server's list is not trusted, only used: a group id is a routing label it already holds,
    // and a wrong one fails to decrypt exactly as no id would. The locally learned mapping is still
    // the fallback, for pushes sent by a server that predates this.
    var groupIds = (groupIdsCsv ?? '')
        .split(',')
        .where((id) => id.isNotEmpty)
        .toList();
    if (groupIds.isEmpty) {
      groupIds = await store.groupIds(conversationId);
    }
    if (groupIds.isEmpty) {
      debugPrint('Pheme: no preview, no known MLS group for $conversationId');
      return null;
    }

    // BEFORE reading the state, not after. Opening the key store is itself a Rust call — the blob
    // is sealed with vaultOpen — so a readState() on an uninitialised isolate throws "flutter_rust
    // _bridge has not been initialized", which this catches and reports as no key material at all.
    // The keys were always there; the library that opens them was not.
    await _ensureRust();

    final state = await store.readState();
    if (state == null) {
      debugPrint('Pheme: no preview, this device holds no MLS key material');
      return null;
    }

    final outcome = await rust.mlsDecryptPreview(
      state: state,
      groupIds: groupIds
          .map((id) => Uint8List.fromList(utf8.encode(id)))
          .toList(),
      ciphertext: base64Decode(ciphertextBase64),
    );
    final plaintext = outcome.plaintext;
    // Control traffic, or a message this device cannot read — and which of those matters, so say
    // it. Holding NONE of the offered groups means this device never joined the group the message
    // was sent in. Holding one and still failing means the key material is there but this snapshot
    // cannot open THIS message: the state on disk lags the epoch it was sent in, or the app has
    // already consumed the key.
    if (plaintext == null) {
      debugPrint(
        'Pheme: no preview — held ${outcome.groupsHeld} of '
        '${outcome.groupsOffered} offered group(s), none could read it',
      );
      // Holding the group and still failing usually means the APP GOT THERE FIRST. A running app
      // stays connected, receives the message over SSE, decrypts it for real — which destroys the
      // message key, because that is what forward secrecy is — and writes the advanced ratchet to
      // disk. This snapshot then has nothing left to decrypt with.
      //
      // Which is not a loss, because the app wrote the body down. It has to: a message decrypts
      // exactly once, so the cache is the only copy that will ever exist. Reading it back is
      // strictly better than showing "New message" about a message this device has already read.
      //
      // Same sealed-at-rest data key as the state, and still a pure read.
      if (outcome.groupsHeld > 0) {
        return await _cachedBody(conversationId, messageId);
      }
      return null;
    }

    final body = _bodyOf(plaintext);
    if (body == null) return null;
    return NotificationPreview(
      body: body,
      // The credential MLS authenticated as the signer, reduced to the user id the payload's
      // claimed senderId can be compared against.
      senderUserId: userOfIdentity(outcome.sender ?? ''),
    );
  } on Object catch (e) {
    // Deliberately broad. Anything at all here — no key material, a state blob from a newer build,
    // a malformed payload — must degrade to the generic notification rather than lose it.
    debugPrint('Pheme: notification preview unavailable: $e');
    return null;
  }
}

/// The body this device already decrypted and wrote down, if it is there.
///
/// Returns null for a photo with no caption, for the same reason _bodyOf does: a preview is one
/// line on a lock screen, and inventing the word "Photo" is a claim about content this path should
/// not be making.
Future<NotificationPreview?> _cachedBody(
  String conversationId,
  String? messageId,
) async {
  if (messageId == null || messageId.isEmpty) return null;
  try {
    final cache = ChatCache(const FlutterSecureStorage());
    final bodies = await cache.load(conversationId);
    final serialised = bodies[messageId];
    if (serialised == null) {
      debugPrint(
        'Pheme: no preview — the app has not read that message either',
      );
      return null;
    }
    // The cache carries the sender the app authenticated when it read this message, so a preview
    // served from it is attributed exactly as well as one decrypted here. An entry written before
    // senders were stored yields '', which the caller treats as "nothing to compare" rather than as
    // agreement with whatever the payload claims.
    final entry = decodeCacheEntry(serialised);
    if (entry.content.body.isEmpty) return null;
    debugPrint('Pheme: preview served from the body the app already decrypted');
    return NotificationPreview(
      body: entry.content.body,
      senderUserId: entry.attribution.isLegacy ? '' : entry.attribution.userId,
    );
  } on Object catch (e) {
    debugPrint('Pheme: cached body unavailable: $e');
    return null;
  }
}

/// The body text out of a decrypted message, or null if there is none worth showing.
///
/// Mirrors the codec in crypto/chat_content.dart, but reads only `body`: a preview is one line on a
/// lock screen, and photos and reply references are not it. A photo with no caption yields null and
/// the caller falls back to the generic text — "Photo" would read better, but it is also a claim
/// about content, and this path should make as few of those as it can.
String? _bodyOf(Uint8List plaintext) {
  try {
    final decoded = jsonDecode(utf8.decode(plaintext));
    if (decoded is! Map) return null;
    final body = decoded['body'];
    return (body is String && body.isNotEmpty) ? body : null;
  } on Object {
    return null;
  }
}
