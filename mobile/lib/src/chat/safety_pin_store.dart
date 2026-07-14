// Trust-on-first-use pinning for safety numbers.
//
// The number is derived from the group's ratchet tree, so it legitimately CHANGES whenever a member
// adds or removes a device — that is not noise, it is the mechanism working. But a device nobody
// recognises appearing in the group looks exactly the same from here, and that is precisely what this
// is for. So: remember the number the user last accepted, and tell them when it moves.

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../core/providers.dart';
import 'chat_providers.dart';

class SafetyPinStore {
  SafetyPinStore(this._storage);

  static const _key = 'pheme.safetyPins.v1';

  final FlutterSecureStorage _storage;
  Map<String, String>? _cache;

  Future<Map<String, String>> _load() async {
    final cached = _cache;
    if (cached != null) return cached;

    final raw = await _storage.read(key: _key);
    final pins = <String, String>{};
    if (raw != null) {
      try {
        (jsonDecode(raw) as Map<String, dynamic>).forEach((id, number) {
          if (number is String) pins[id] = number;
        });
      } on FormatException {
        // Corrupt. Every number reads as first-seen, which is safe: it prompts rather than reassures.
      }
    }
    return _cache = pins;
  }

  /// The number the user last accepted for this conversation, if any.
  Future<String?> pinned(String conversationId) async =>
      (await _load())[conversationId];

  Future<void> pin(String conversationId, String number) async {
    final pins = await _load();
    pins[conversationId] = number;
    await _storage.write(key: _key, value: jsonEncode(pins));
  }

  Future<void> wipe() async {
    _cache = null;
    await _storage.delete(key: _key);
  }
}

final safetyPinStoreProvider = Provider<SafetyPinStore>(
  (ref) => SafetyPinStore(ref.watch(secureStorageProvider)),
);

class SafetyNumberState {
  const SafetyNumberState({required this.number, required this.changed});

  /// Empty when the group is not established, or this device is not in it yet.
  final String number;

  /// The number moved since the user last accepted one. Worth interrupting them for.
  ///
  /// Deliberately false on FIRST sight: the user has nothing to compare against yet, and crying wolf
  /// on a number they have never seen would teach them to dismiss the warning that matters.
  final bool changed;
}

final safetyNumberProvider = FutureProvider.family<SafetyNumberState, String>((
  ref,
  conversationId,
) async {
  final number = await ref
      .read(mlsServiceProvider)
      .conversationSafetyNumber(conversationId, ref.read(myUserIdProvider));

  if (number.isEmpty) {
    return const SafetyNumberState(number: '', changed: false);
  }

  final store = ref.read(safetyPinStoreProvider);
  final pinned = await store.pinned(conversationId);

  if (pinned == null) {
    // First sight. Pin it silently — there is nothing to warn about yet.
    await store.pin(conversationId, number);
    return SafetyNumberState(number: number, changed: false);
  }

  return SafetyNumberState(number: number, changed: pinned != number);
});
