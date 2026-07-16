// The same cases as web/src/lib/receipts.test.ts: the two clients must agree about what a tick
// means, or the same conversation reads differently depending on which one you opened.

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/chat/receipts.dart';
import 'package:pheme_mobile/src/models/chat_models.dart';
import 'package:pheme_mobile/src/models/models.dart'
    show ChannelRole, PublicUser;

const me = 'me';
const early = '2026-07-16T10:00:00Z';
const mid = '2026-07-16T11:00:00Z';
const late = '2026-07-16T12:00:00Z';

ConversationMember member(
  String userId, {
  String delivered = '',
  String read = '',
}) => ConversationMember(
  id: 'm-$userId',
  conversationId: 'c1',
  userId: userId,
  role: ChannelRole.user,
  joinedAt: early,
  user: PublicUser(id: userId, displayName: userId),
  deliveredAt: delivered,
  readAt: read,
);

void main() {
  group('messageReceipt', () {
    test('is sent while the other side has not reported at all', () {
      expect(
        messageReceipt(mid, [member(me), member('bob')], me),
        Receipt.sent,
      );
    });

    test('is delivered once they have received up to it', () {
      final members = [member(me), member('bob', delivered: late)];
      expect(messageReceipt(mid, members, me), Receipt.delivered);
    });

    test('is read once they have read up to it', () {
      final members = [member(me), member('bob', delivered: late, read: late)];
      expect(messageReceipt(mid, members, me), Receipt.read);
    });

    test(
      'is still only delivered for a message newer than what they have read',
      () {
        final members = [
          member(me),
          member('bob', delivered: late, read: early),
        ];
        expect(messageReceipt(mid, members, me), Receipt.delivered);
      },
    );

    test('never waits on yourself', () {
      // We have read nothing; they have read everything. It is read.
      final members = [member(me), member('bob', delivered: late, read: late)];
      expect(messageReceipt(mid, members, me), Receipt.read);
    });

    group('groups wait for the slowest member', () {
      test('is read only once every member has read it', () {
        final members = [
          member(me),
          member('bob', delivered: late, read: late),
          member('carol', delivered: late, read: late),
        ];
        expect(messageReceipt(mid, members, me), Receipt.read);
      });

      test('is delivered, not read, while one member has only received it', () {
        final members = [
          member(me),
          member('bob', delivered: late, read: late),
          member('carol', delivered: late, read: early),
        ];
        expect(messageReceipt(mid, members, me), Receipt.delivered);
      });

      test('is sent while one member has not received it at all', () {
        final members = [
          member(me),
          member('bob', delivered: late, read: late),
          member('carol'),
        ];
        expect(messageReceipt(mid, members, me), Receipt.sent);
      });
    });

    test('claims nothing in a conversation with nobody else left in it', () {
      expect(messageReceipt(mid, [member(me)], me), Receipt.sent);
    });
  });

  group('applyReceipt', () {
    test('moves a member forward', () {
      final members = [
        member(me),
        member('bob', delivered: early, read: early),
      ];
      final next = applyReceipt(
        members,
        const ConversationReceipt(
          userId: 'bob',
          deliveredAt: late,
          readAt: mid,
        ),
      );
      expect(next[1].deliveredAt, late);
      expect(next[1].readAt, mid);
    });

    test('never moves a member backwards', () {
      final members = [member(me), member('bob', delivered: late, read: late)];
      final next = applyReceipt(
        members,
        const ConversationReceipt(
          userId: 'bob',
          deliveredAt: early,
          readAt: early,
        ),
      );
      expect(next[1].deliveredAt, late);
      expect(next[1].readAt, late);
    });

    test('leaves everyone else untouched', () {
      final members = [
        member(me),
        member('bob', delivered: early),
        member('carol', delivered: early),
      ];
      final next = applyReceipt(
        members,
        const ConversationReceipt(userId: 'bob', readAt: late),
      );
      expect(next[2].deliveredAt, early);
      expect(next[2].readAt, '');
    });
  });
}
