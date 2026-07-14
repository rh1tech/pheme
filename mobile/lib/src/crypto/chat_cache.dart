// The decrypted bodies of messages this device has read.
//
// THIS IS NOT A CACHE IN THE USUAL SENSE — it is the only copy.
//
// MLS gives forward secrecy by destroying the message key as it goes, which has two consequences
// that between them make this file mandatory rather than an optimisation:
//
//   * a message decrypts EXACTLY ONCE. Read it, and the key is gone. Scroll away and back and there
//     is nothing left to decrypt with.
//   * a sender can NEVER decrypt its own message. The key was destroyed on encrypt.
//
// So every body is written here the first (and only) time it is seen, and a sent body is written
// here at send time. Without it the history renders as a column of blanks — including, absurdly,
// everything the user typed themselves.
//
// Sealed at rest under the same data key as the MLS state: this is the plaintext of end-to-end
// encrypted messages, and leaving it lying in the clear would give away exactly what the encryption
// was for.

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:path_provider/path_provider.dart';

import '../rust/api/vault.dart';

class ChatCache {
  ChatCache(this._storage);

  static const _dataKeyKey =
      'pheme.mlsDataKey'; // the same key that seals the MLS state

  /// Its OWN domain, bound into the seal. The key store and this cache share a key, so without it a
  /// body cache would open cleanly in the key store's place — handing arbitrary bytes to import_state.
  static const _domain = 'pheme.chat.bodies.v1';
  static const _iosOptions = IOSOptions(
    accessibility: KeychainAccessibility.first_unlock,
  );

  final FlutterSecureStorage _storage;

  /// conversationId -> (messageId -> body). Loaded lazily, written through.
  final _bodies = <String, Map<String, String>>{};

  /// The newest body per conversation, for the conversation-list preview. The list cannot decrypt
  /// anything — it only ever sees ciphertext — so a preview can only come from here.
  final _previews = <String, String>{};

  Future<Directory> _dir() async {
    final support = await getApplicationSupportDirectory();
    final dir = Directory('${support.path}/bodies');
    if (!await dir.exists()) await dir.create(recursive: true);
    return dir;
  }

  Future<File> _file(String conversationId) async =>
      File('${(await _dir()).path}/$conversationId.json');

  Future<Uint8List?> _dataKey() async {
    final encoded = await _storage.read(
      key: _dataKeyKey,
      iOptions: _iosOptions,
    );
    if (encoded == null) return null;
    return Uint8List.fromList(
      encoded.split(',').map(int.parse).toList(growable: false),
    );
  }

  /// Every body known for a conversation. Empty is a legitimate answer, not a failure.
  Future<Map<String, String>> load(String conversationId) async {
    final cached = _bodies[conversationId];
    if (cached != null) return cached;

    final bodies = <String, String>{};
    final file = await _file(conversationId);
    final key = await _dataKey();

    if (await file.exists() && key != null) {
      try {
        final opened = await vaultOpen(
          domain: _domain,
          key: key,
          sealed: await file.readAsBytes(),
        );
        final json = jsonDecode(utf8.decode(opened)) as Map<String, dynamic>;
        json.forEach((id, body) {
          if (body is String) bodies[id] = body;
        });
      } on Object {
        // Unreadable is the same as absent. The bodies are gone either way, and there is no second
        // chance to decrypt them — so there is nothing to do but carry on with what we have.
      }
    }

    _bodies[conversationId] = bodies;
    final newest = bodies.values.isNotEmpty ? bodies.values.last : null;
    if (newest != null) _previews.putIfAbsent(conversationId, () => newest);
    return bodies;
  }

  /// Records a body on first sight. Also becomes the conversation's preview.
  Future<void> cacheBody(
    String conversationId,
    String messageId,
    String body,
  ) async {
    final bodies = await load(conversationId);
    if (bodies[messageId] == body) return;
    bodies[messageId] = body;
    _previews[conversationId] = body;
    await _flush(conversationId, bodies);
  }

  /// The body of a message, if this device ever managed to read it.
  String? body(String conversationId, String messageId) =>
      _bodies[conversationId]?[messageId];

  /// The newest body seen for a conversation — the list preview.
  String? preview(String conversationId) => _previews[conversationId];

  Future<void> _flush(String conversationId, Map<String, String> bodies) async {
    final key = await _dataKey();
    if (key == null) return; // no identity yet; nothing to protect it with

    final sealed = await vaultSeal(
      domain: _domain,
      key: key,
      plaintext: Uint8List.fromList(utf8.encode(jsonEncode(bodies))),
    );
    final file = await _file(conversationId);
    final temp = File('${file.path}.tmp');
    await temp.writeAsBytes(sealed, flush: true);
    await temp.rename(file.path);
  }

  /// Forgets a conversation's bodies — it was deleted.
  Future<void> forget(String conversationId) async {
    _bodies.remove(conversationId);
    _previews.remove(conversationId);
    final file = await _file(conversationId);
    if (await file.exists()) await file.delete();
  }

  /// Erases every decrypted body. Logout: this is the plaintext the encryption exists to protect,
  /// and leaving it behind on a shared device would defeat the whole thing.
  Future<void> wipe() async {
    _bodies.clear();
    _previews.clear();
    final dir = await _dir();
    if (await dir.exists()) await dir.delete(recursive: true);
  }
}
