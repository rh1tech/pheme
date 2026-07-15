// The ordered message ENVELOPE of a conversation — the list of message metadata
// (id, sender, timestamp, content type, ciphertext), oldest-first.
//
// The decrypted bodies are the ChatCache's job; this stores the list they hang off,
// so a chat can paint the transcript that was last on screen the instant it opens,
// straight from disk, instead of behind a spinner while the network is asked for the
// newest page. The server is still asked and the two reconciled — this only ever
// races the network to first paint.
//
// Sealed at rest under the same data key as the bodies and the MLS state. The
// ciphertext is already opaque, but the metadata (who, when) is not, and this is chat
// data on the user's device: it gets the same protection as everything else here.

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:path_provider/path_provider.dart';

import '../models/chat_models.dart';
import '../rust/api/vault.dart';

class ChatEnvelopeCache {
  /// [namespace] must match the MlsStore's / ChatCache's — they share a data key, and
  /// a two-device test gives each device its own. Empty in the app.
  ChatEnvelopeCache(this._storage, {String namespace = ''}) : _ns = namespace;

  final String _ns;

  /// The same key that seals the MLS state and the message bodies.
  String get _dataKeyKey => 'pheme.mlsDataKey$_ns';

  /// Its OWN seal domain, so an envelope file can never open in the key store's or the
  /// body cache's place. Bound into the seal.
  static const _domain = 'pheme.chat.envelope.v1';
  static const _iosOptions = IOSOptions(
    accessibility: KeychainAccessibility.first_unlock,
  );

  /// A cap so a long-lived conversation cannot grow the cache without bound. Only the
  /// newest window is kept; older history stays in the body cache and pages back from
  /// the server on scroll. Comfortably larger than one page (50).
  static const _maxCached = 200;

  final FlutterSecureStorage _storage;

  Future<Directory> _dir() async {
    final support = await getApplicationSupportDirectory();
    final dir = Directory('${support.path}/envelopes$_ns');
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

  /// The cached ordered (oldest-first) envelope, or [] when nothing is cached.
  Future<List<ChatMessage>> load(String conversationId) async {
    final file = await _file(conversationId);
    final key = await _dataKey();
    if (!await file.exists() || key == null) return const [];

    try {
      final opened = await vaultOpen(
        domain: _domain,
        key: key,
        sealed: await file.readAsBytes(),
      );
      final list = jsonDecode(utf8.decode(opened)) as List<dynamic>;
      return list
          .map((e) => ChatMessage.fromJson((e as Map).cast<String, dynamic>()))
          .toList(growable: false);
    } on Object {
      // Unreadable is the same as absent: the network fetch repopulates it.
      return const [];
    }
  }

  /// Persists the newest window of an ordered (oldest-first) message list. Called
  /// whenever the on-screen transcript settles, so the next open paints from it.
  Future<void> save(String conversationId, List<ChatMessage> messages) async {
    final key = await _dataKey();
    if (key == null) return; // no identity yet; nothing to protect it with

    final window = messages.length > _maxCached
        ? messages.sublist(messages.length - _maxCached)
        : messages;
    final sealed = await vaultSeal(
      domain: _domain,
      key: key,
      plaintext: Uint8List.fromList(
        utf8.encode(jsonEncode(window.map((m) => m.toJson()).toList())),
      ),
    );
    final file = await _file(conversationId);
    final temp = File('${file.path}.tmp');
    await temp.writeAsBytes(sealed, flush: true);
    await temp.rename(file.path);
  }

  /// Drops one conversation's cached envelope — on delete, clear-history, or a 404.
  Future<void> forget(String conversationId) async {
    final file = await _file(conversationId);
    if (await file.exists()) await file.delete();
  }

  /// Erases every cached envelope. Logout, alongside the bodies.
  Future<void> wipe() async {
    final dir = await _dir();
    if (await dir.exists()) await dir.delete(recursive: true);
  }
}
