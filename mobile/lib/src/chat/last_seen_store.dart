// When the user last read each conversation.
//
// Local to this device, and it has to be. Read receipts would need the server to know which message
// is the newest one you have seen — and it cannot read a single one of them. The web client makes
// the same call, with the same reasoning: an unread dot that does not sync across devices is a
// smaller lie than a count that cannot be computed at all.

import 'dart:convert';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

class LastSeenStore {
  LastSeenStore(this._storage);

  static const _key = 'pheme.lastSeen.v1';

  final FlutterSecureStorage _storage;
  Map<String, String>? _cache;

  Future<Map<String, String>> _load() async {
    final cached = _cache;
    if (cached != null) return cached;

    final raw = await _storage.read(key: _key);
    final seen = <String, String>{};
    if (raw != null) {
      try {
        (jsonDecode(raw) as Map<String, dynamic>).forEach((id, at) {
          if (at is String) seen[id] = at;
        });
      } on FormatException {
        // Corrupt. Everything reads as unread, which is recoverable by opening the chats.
      }
    }
    return _cache = seen;
  }

  /// Every conversation's last-seen timestamp.
  Future<Map<String, String>> all() async => Map.unmodifiable(await _load());

  /// The ISO timestamp of the last message the user has seen in [conversationId].
  Future<String?> lastSeen(String conversationId) async =>
      (await _load())[conversationId];

  Future<void> markRead(String conversationId, String at) async {
    final seen = await _load();
    if (seen[conversationId] == at) return;
    seen[conversationId] = at;
    await _storage.write(key: _key, value: jsonEncode(seen));
  }

  Future<void> forget(String conversationId) async {
    final seen = await _load();
    if (seen.remove(conversationId) == null) return;
    await _storage.write(key: _key, value: jsonEncode(seen));
  }

  Future<void> wipe() async {
    _cache = null;
    await _storage.delete(key: _key);
  }
}
