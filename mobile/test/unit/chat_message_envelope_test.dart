// The message envelope is cached to disk and read back to paint a chat instantly on open. That
// round trip rides on ChatMessage.toJson/fromJson, so the two must agree exactly — including the
// base64 encoding of the ciphertext, which is the one field easy to get wrong.

import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/models/chat_models.dart';

void main() {
  group('ChatMessage envelope round-trip', () {
    test('survives toJson -> jsonEncode -> jsonDecode -> fromJson', () {
      final original = ChatMessage(
        id: 'm1',
        conversationId: 'c1',
        senderId: 'u1',
        // Arbitrary bytes, not valid UTF-8 — the ciphertext is opaque and must not be
        // corrupted by the encoding.
        ciphertext: Uint8List.fromList([0x00, 0x01, 0xff, 0xfe, 0x80]),
        contentType: 'application/mls',
        createdAt: '2026-07-15T12:00:00Z',
        mlsEpoch: 7,
      );

      final decoded =
          jsonDecode(jsonEncode(original.toJson())) as Map<String, dynamic>;
      final restored = ChatMessage.fromJson(decoded);

      expect(restored.id, original.id);
      expect(restored.conversationId, original.conversationId);
      expect(restored.senderId, original.senderId);
      expect(restored.ciphertext, original.ciphertext);
      expect(restored.contentType, original.contentType);
      expect(restored.createdAt, original.createdAt);
      expect(restored.mlsEpoch, original.mlsEpoch);
    });

    test('omits mlsEpoch when absent, and reads back as null', () {
      final original = ChatMessage(
        id: 'm2',
        conversationId: 'c1',
        senderId: 'u1',
        ciphertext: Uint8List.fromList([1, 2, 3]),
        contentType: 'application/mls',
        createdAt: '2026-07-15T12:01:00Z',
      );

      expect(original.toJson().containsKey('mlsEpoch'), isFalse);
      final restored = ChatMessage.fromJson(
        jsonDecode(jsonEncode(original.toJson())) as Map<String, dynamic>,
      );
      expect(restored.mlsEpoch, isNull);
    });

    test('a whole ordered list round-trips, preserving order', () {
      final list = [
        for (var i = 0; i < 5; i++)
          ChatMessage(
            id: 'm$i',
            conversationId: 'c1',
            senderId: 'u1',
            ciphertext: Uint8List.fromList([i]),
            contentType: 'application/mls',
            createdAt: '2026-07-15T12:0$i:00Z',
          ),
      ];

      final encoded = jsonEncode(list.map((m) => m.toJson()).toList());
      final restored = (jsonDecode(encoded) as List)
          .map((e) => ChatMessage.fromJson((e as Map).cast<String, dynamic>()))
          .toList();

      expect(restored.map((m) => m.id), list.map((m) => m.id));
      expect(restored.first.ciphertext, list.first.ciphertext);
      expect(restored.last.ciphertext, list.last.ciphertext);
    });
  });
}
