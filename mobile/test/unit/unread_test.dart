import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/chat/chat_providers.dart';
import 'package:pheme_mobile/src/models/chat_models.dart';

LastChatMessage msg({
  String senderId = 'them',
  String contentType = ContentType.application,
  String createdAt = '2026-07-19T10:00:00.000Z',
}) => LastChatMessage(
  id: 'm1',
  senderId: senderId,
  ciphertext: Uint8List(0),
  contentType: contentType,
  createdAt: createdAt,
);

void main() {
  group('isConversationUnread', () {
    test('an empty conversation is not unread', () {
      expect(
        isConversationUnread(last: null, myUserId: 'me', seenAt: null),
        isFalse,
      );
    });

    test('a message from someone else, never seen, is unread', () {
      expect(
        isConversationUnread(last: msg(), myUserId: 'me', seenAt: null),
        isTrue,
      );
    });

    // The reported bug: a message arrives while the app is closed, you open the chat and read it,
    // and the dot stays lit because nothing marked it read. The rule itself is fine — what was
    // missing is the watermark ever advancing on open. This pins the rule so a regression is loud.
    test('a message that has been seen is not unread', () {
      expect(
        isConversationUnread(
          last: msg(createdAt: '2026-07-19T10:00:00.000Z'),
          myUserId: 'me',
          seenAt: '2026-07-19T10:00:00.000Z',
        ),
        isFalse,
      );
    });

    test('a message newer than the watermark is unread', () {
      expect(
        isConversationUnread(
          last: msg(createdAt: '2026-07-19T10:00:01.000Z'),
          myUserId: 'me',
          seenAt: '2026-07-19T10:00:00.000Z',
        ),
        isTrue,
      );
    });

    test('a message older than the watermark is not unread', () {
      expect(
        isConversationUnread(
          last: msg(createdAt: '2026-07-19T09:59:59.000Z'),
          myUserId: 'me',
          seenAt: '2026-07-19T10:00:00.000Z',
        ),
        isFalse,
      );
    });

    test('our own message is never unread, seen or not', () {
      expect(
        isConversationUnread(
          last: msg(senderId: 'me'),
          myUserId: 'me',
          seenAt: null,
        ),
        isFalse,
      );
    });

    // Protocol traffic is not a message: nobody wrote it and there is nothing to read.
    test('control traffic never lights the dot', () {
      for (final type in ContentType.control) {
        expect(
          isConversationUnread(
            last: msg(contentType: type),
            myUserId: 'me',
            seenAt: null,
          ),
          isFalse,
          reason: '$type should not be unread',
        );
      }
    });

    // Nor does a line the conversation says about itself.
    test('a membership note never lights the dot', () {
      expect(
        isConversationUnread(
          last: msg(contentType: ContentType.membership),
          myUserId: 'me',
          seenAt: null,
        ),
        isFalse,
      );
    });

    // The timestamps are compared as strings, which is only safe because the server writes one
    // fixed shape. If that ever changes, this is where it shows up.
    test(
      'lexical comparison holds across a second, minute and day boundary',
      () {
        final pairs = [
          ('2026-07-19T10:00:00.000Z', '2026-07-19T10:00:00.001Z'),
          ('2026-07-19T10:00:59.999Z', '2026-07-19T10:01:00.000Z'),
          ('2026-07-19T23:59:59.999Z', '2026-07-20T00:00:00.000Z'),
        ];
        for (final (earlier, later) in pairs) {
          expect(
            isConversationUnread(
              last: msg(createdAt: later),
              myUserId: 'me',
              seenAt: earlier,
            ),
            isTrue,
            reason: '$later should be unread against $earlier',
          );
          expect(
            isConversationUnread(
              last: msg(createdAt: earlier),
              myUserId: 'me',
              seenAt: later,
            ),
            isFalse,
          );
        }
      },
    );
  });

  _baselineGroup();
}

/// The baseline that stops a fresh install declaring an entire account unread.
///
/// Read state is per-device and does not sync, so a newly installed app has no record of any
/// conversation. Treating that as "unread" lit up every chat the account had ever had, which is not
/// information — it is noise, and it buries the one conversation that genuinely does have something
/// new in it.
void _baselineGroup() {
  group('isConversationUnread with a fresh-install baseline', () {
    const baseline = '2026-07-20T10:00:00.000Z';

    test('history older than the baseline is treated as read', () {
      expect(
        isConversationUnread(
          last: msg(createdAt: '2026-07-19T09:00:00.000Z'),
          myUserId: 'me',
          seenAt: null,
          baseline: baseline,
        ),
        isFalse,
      );
    });

    test('a message that arrives after the baseline is unread', () {
      expect(
        isConversationUnread(
          last: msg(createdAt: '2026-07-20T11:00:00.000Z'),
          myUserId: 'me',
          seenAt: null,
          baseline: baseline,
        ),
        isTrue,
      );
    });

    test('a real seenAt still wins over the baseline', () {
      // The baseline is only a fallback for conversations this device has no record of. Once it has
      // displayed something, that is the better answer and must not be overridden.
      expect(
        isConversationUnread(
          last: msg(createdAt: '2026-07-20T12:00:00.000Z'),
          myUserId: 'me',
          seenAt: '2026-07-20T12:00:00.000Z',
          baseline: baseline,
        ),
        isFalse,
      );
    });

    test('without a baseline an unseen message is still unread', () {
      // A device that has been signed in all along, where no record genuinely means unseen.
      expect(
        isConversationUnread(
          last: msg(createdAt: '2026-07-20T11:00:00.000Z'),
          myUserId: 'me',
          seenAt: null,
        ),
        isTrue,
      );
    });

    test('our own message is never unread, baseline or not', () {
      expect(
        isConversationUnread(
          last: msg(senderId: 'me', createdAt: '2026-07-20T11:00:00.000Z'),
          myUserId: 'me',
          seenAt: null,
          baseline: baseline,
        ),
        isFalse,
      );
    });
  });
}
