import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/models/chat_models.dart';

Uint8List body(Map<String, Object?> json) =>
    Uint8List.fromList(utf8.encode(jsonEncode(json)));

void main() {
  group('MembershipEvent', () {
    test('parses each action the server writes', () {
      for (final action in ['added', 'removed', 'left']) {
        final event = MembershipEvent.tryParse(
          body({'action': action, 'actorId': 'a', 'userId': 'u'}),
        );
        expect(event, isNotNull, reason: '$action should parse');
        expect(event!.action, action);
        expect(event.actorId, 'a');
        expect(event.userId, 'u');
      }
    });

    test('refuses an unknown action rather than rendering nonsense', () {
      expect(
        MembershipEvent.tryParse(
          body({'action': 'promoted', 'actorId': 'a', 'userId': 'u'}),
        ),
        isNull,
      );
    });

    test('refuses a note with nobody in it', () {
      expect(MembershipEvent.tryParse(body({'action': 'added'})), isNull);
      expect(
        MembershipEvent.tryParse(body({'action': 'added', 'userId': ''})),
        isNull,
      );
    });

    // A conversation must not fail to render because of a note ABOUT it. Every malformed shape
    // yields null and the line is simply not shown.
    test('survives anything that is not a membership note', () {
      for (final bytes in [
        Uint8List(0),
        Uint8List.fromList(utf8.encode('not json at all')),
        Uint8List.fromList(utf8.encode('[]')),
        Uint8List.fromList(utf8.encode('"a string"')),
        Uint8List.fromList([0xff, 0xfe, 0x00]),
      ]) {
        expect(MembershipEvent.tryParse(bytes), isNull);
      }
    });

    test('a leaver is their own actor', () {
      final event = MembershipEvent.tryParse(
        body({'action': 'left', 'actorId': 'u', 'userId': 'u'}),
      );
      expect(event!.actorId, event.userId);
    });
  });

  group('ChatMessage classification', () {
    ChatMessage of(String contentType) => ChatMessage(
      id: 'm',
      conversationId: 'c',
      senderId: 's',
      ciphertext: body({'action': 'added', 'actorId': 'a', 'userId': 'u'}),
      contentType: contentType,
      createdAt: '2026-07-19T00:00:00Z',
    );

    // It must be rendered, so it cannot be control traffic (which is filtered out of the feed)...
    test('a membership note is not control traffic', () {
      expect(of(ContentType.membership).isControl, isFalse);
    });

    // ...and it must never reach the decrypt path, which would burn a lookup and show it as
    // "not available on this device".
    test('a membership note is a system line', () {
      expect(of(ContentType.membership).isSystem, isTrue);
      expect(of(ContentType.application).isSystem, isFalse);
      expect(of(ContentType.mlsCommit).isSystem, isFalse);
    });

    test('an ordinary message carries no membership event', () {
      expect(of(ContentType.application).membershipEvent, isNull);
    });
  });
}
