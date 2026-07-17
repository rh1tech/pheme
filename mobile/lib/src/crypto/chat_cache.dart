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
import 'chat_content.dart';

class ChatCache {
  /// [namespace] must match the MlsStore's — the two share a data key, and in a two-device test each
  /// device needs its own. Empty in the app.
  ChatCache(this._storage, {String namespace = ''}) : _ns = namespace;

  final String _ns;

  /// The same key that seals the MLS state.
  String get _dataKeyKey => 'pheme.mlsDataKey$_ns';

  /// Its OWN domain, bound into the seal. The key store and this cache share a key, so without it a
  /// body cache would open cleanly in the key store's place — handing arbitrary bytes to import_state.
  static const _domain = 'pheme.chat.bodies.v1';
  static const _iosOptions = IOSOptions(
    accessibility: KeychainAccessibility.first_unlock,
  );

  final FlutterSecureStorage _storage;

  /// conversationId -> (messageId -> the SERIALISED CONTENT, not just the body).
  ///
  /// The whole content, because a message is not only text: it may carry photos and a reply. Storing
  /// just the body would mean a photo message came back as a bare caption the second time it was
  /// looked at — and there is no second decrypt to recover the rest from.
  final _contents = <String, Map<String, String>>{};

  /// The newest body per conversation, for the conversation-list preview. The list cannot decrypt
  /// anything — it only ever sees ciphertext — so a preview can only come from here.
  final _previews = <String, String>{};

  Future<Directory> _dir() async {
    final support = await getApplicationSupportDirectory();
    final dir = Directory('${support.path}/bodies$_ns');
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

  /// Every message this device has managed to read in a conversation. Empty is a legitimate answer,
  /// not a failure.
  Future<Map<String, String>> load(String conversationId) async {
    final cached = _contents[conversationId];
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

    _contents[conversationId] = bodies;
    final newest = bodies.values.isNotEmpty ? bodies.values.last : null;
    if (newest != null) {
      _previews.putIfAbsent(conversationId, () => _previewOf(newest));
    }
    return bodies;
  }

  /// Records a message's content on first sight. Also becomes the conversation's preview.
  Future<void> cacheContent(
    String conversationId,
    String messageId,
    ChatContent content,
  ) async {
    final serialised = utf8.decode(serializeContent(content));

    final contents = await load(conversationId);
    if (contents[messageId] == serialised) return;

    contents[messageId] = serialised;
    _previews[conversationId] = _previewOf(serialised);
    await _flush(conversationId, contents);
  }

  /// A message's content, if this device ever managed to read it.
  ChatContent? content(String conversationId, String messageId) {
    final serialised = _contents[conversationId]?[messageId];
    if (serialised == null) return null;
    return parseContent(Uint8List.fromList(utf8.encode(serialised)));
  }

  /// What a conversation-list row shows: the caption, or a note that it was a photo.
  String _previewOf(String serialised) {
    final content = parseContent(Uint8List.fromList(utf8.encode(serialised)));
    if (content.body.isNotEmpty) return content.body;
    // A photo with no caption still has to say something. An empty row reads as a bug.
    return content.hasPhotos ? '__photo__' : '';
  }

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

  /// Every conversation's bodies, raw (still-serialised) — the transcript half of the key backup,
  /// and what a history offer seals for a newly-joined device.
  ///
  /// Raw on purpose: this is a copy of the cache, not a reading of it. Round-tripping each entry
  /// through parse/serialise here could only lose information a future content version carried.
  Future<Map<String, Map<String, String>>> exportAllContents() async {
    final out = <String, Map<String, String>>{};
    final dir = await _dir();
    if (!await dir.exists()) return out;
    await for (final entry in dir.list()) {
      if (entry is! File || !entry.path.endsWith('.json')) continue;
      final name = entry.uri.pathSegments.last;
      final conversationId = name.substring(0, name.length - '.json'.length);
      final bodies = await load(conversationId);
      if (bodies.isNotEmpty) {
        out[conversationId] = Map<String, String>.from(bodies);
      }
    }
    return out;
  }

  /// Imports transcripts from a backup or a history offer — a device adopting bodies it holds none
  /// of. Merged UNDER what this device already has: anything decrypted here was read more recently
  /// than the snapshot was taken, so on a collision the local copy wins and is never overwritten.
  Future<void> importContents(Map<String, Map<String, String>> all) async {
    for (final entry in all.entries) {
      final conversationId = entry.key;
      final contents = await load(conversationId);
      var changed = false;
      entry.value.forEach((id, serialised) {
        if (!contents.containsKey(id)) {
          contents[id] = serialised;
          changed = true;
        }
      });
      if (!changed) continue;
      final newest = contents.values.isNotEmpty ? contents.values.last : null;
      if (newest != null) _previews[conversationId] = _previewOf(newest);
      await _flush(conversationId, contents);
    }
  }

  /// Forgets a conversation's bodies — it was deleted.
  Future<void> forget(String conversationId) async {
    _contents.remove(conversationId);
    _previews.remove(conversationId);
    final file = await _file(conversationId);
    if (await file.exists()) await file.delete();
  }

  /// Erases every decrypted body. Logout: this is the plaintext the encryption exists to protect,
  /// and leaving it behind on a shared device would defeat the whole thing.
  Future<void> wipe() async {
    _contents.clear();
    _previews.clear();
    final dir = await _dir();
    if (await dir.exists()) await dir.delete(recursive: true);
  }
}
