// When a message is allowed on screen.
//
// The feed paints the cached transcript from disk BEFORE the decrypt loop runs over it, so for a
// beat every bubble has no content. Rendering something in that gap is worse than rendering
// nothing, because the only thing a bodyless bubble can say is "not available on this device" —
// which is PERMANENT. Flashing a permanent verdict a moment before contradicting it teaches people
// not to believe it on the day it is true.
//
// So the rule is "show nothing until we know", and the danger of that rule is the opposite failure:
// a message that is never attempted would become invisible rather than merely unreadable, which
// hides history instead of labelling it. These cases keep both halves honest.

import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/chat/message_feed_controller.dart';
import 'package:pheme_mobile/src/crypto/attribution.dart';
import 'package:pheme_mobile/src/models/chat_models.dart';

ChatMessage message({
  String id = 'm1',
  String contentType = ContentType.application,
}) => ChatMessage(
  id: id,
  conversationId: 'c1',
  senderId: 'u1',
  ciphertext: Uint8List(0),
  contentType: contentType,
  createdAt: '2026-07-31T00:00:00Z',
);

/// The feed as it is the instant the cached transcript is painted: messages on screen, nothing
/// decrypted yet.
MessageFeedState feed({Map<String, CachedEntry?>? contents}) =>
    MessageFeedState(contents: contents ?? const <String, CachedEntry?>{});

void main() {
  group('a message waits until this device knows what it is', () {
    test('an undecrypted message is not shown', () {
      expect(
        feed().isReadyToShow(message()),
        isFalse,
        reason:
            'showing it now can only say "not available on this device", which is permanent',
      );
    });

    test('once decryption is attempted it is shown, readable or not', () {
      // A null value is the decrypt loop's real answer: attempted, cannot read. It must be shown
      // and labelled — hiding it would turn "I cannot read this" into "this never happened", which
      // is a worse lie than the flash this rule removes.
      expect(
        feed(
          contents: const <String, CachedEntry?>{'m1': null},
        ).isReadyToShow(message()),
        isTrue,
      );
    });
  });

  group('what must never be hidden by the wait', () {
    // THE ONE THAT WOULD SILENTLY EMPTY THE SCREEN. The decrypt loop SKIPS system lines — the
    // server writes them in plaintext and they have no key — so they never reach the attempted map.
    // A rule keyed purely off that map would hide them forever.
    test('a membership line shows even though it is never decrypted', () {
      expect(
        feed().isReadyToShow(message(contentType: ContentType.membership)),
        isTrue,
        reason: 'nobody sent it and there is nothing to decrypt',
      );
    });
  });

  group('the wait is per message, not per feed', () {
    test('a decrypted message shows while its neighbour is still pending', () {
      final state = feed(contents: const <String, CachedEntry?>{'m1': null});
      expect(state.isReadyToShow(message(id: 'm1')), isTrue);
      expect(
        state.isReadyToShow(message(id: 'm2')),
        isFalse,
        reason:
            'the transcript fills in as it resolves; one slow message must not hold up the rest',
      );
    });
  });
}
