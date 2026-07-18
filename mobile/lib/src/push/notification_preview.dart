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

import '../crypto/mls_store.dart';
import '../rust/api/mls.dart' as rust;

/// The message text carried by a push, or null when there is nothing to show.
///
/// [conversationId] and [ciphertextBase64] come from the push's data payload; the ciphertext is
/// absent unless the recipient asked for previews, so a null return here is the ordinary case for
/// everybody else.
Future<String?> decryptNotificationPreview({
  required String conversationId,
  required String? ciphertextBase64,
}) async {
  if (ciphertextBase64 == null || ciphertextBase64.isEmpty) return null;
  if (conversationId.isEmpty) return null;

  try {
    // A store of its own. In the background isolate the app's instance does not exist, and in the
    // foreground it belongs to a session this code must not join. Reading is safe from either:
    // the file is opened, not held, and the data key is `first_unlock` so it is readable even
    // before the user has unlocked since a reboot.
    final store = MlsStore(const FlutterSecureStorage());

    final groupIds = await store.groupIds(conversationId);
    if (groupIds.isEmpty) return null;

    final state = await store.readState();
    if (state == null) return null;

    final plaintext = await rust.mlsDecryptPreview(
      state: state,
      groupIds: groupIds
          .map((id) => Uint8List.fromList(utf8.encode(id)))
          .toList(),
      ciphertext: base64Decode(ciphertextBase64),
    );
    // Control traffic, or a message this device cannot read.
    if (plaintext == null) return null;

    return _bodyOf(plaintext);
  } on Object catch (e) {
    // Deliberately broad. Anything at all here — no key material, a state blob from a newer build,
    // a malformed payload — must degrade to the generic notification rather than lose it.
    debugPrint('Pheme: notification preview unavailable: $e');
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
