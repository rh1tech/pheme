import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/models/models.dart';

void main() {
  group('SubscriptionMode', () {
    test('parses open from wire', () {
      expect(SubscriptionMode.fromWire('open'), SubscriptionMode.open);
    });

    test('defaults to approval for unknown or null', () {
      expect(SubscriptionMode.fromWire(null), SubscriptionMode.approval);
      expect(SubscriptionMode.fromWire('bogus'), SubscriptionMode.approval);
    });

    test('round-trips via wire', () {
      expect(SubscriptionMode.open.wire, 'open');
      expect(SubscriptionMode.approval.wire, 'approval');
    });
  });

  group('subscriptionStatusFromWire', () {
    test('maps known statuses', () {
      expect(subscriptionStatusFromWire('active'), SubscriptionStatus.active);
      expect(subscriptionStatusFromWire('pending'), SubscriptionStatus.pending);
    });

    test('falls back to none', () {
      expect(subscriptionStatusFromWire(null), SubscriptionStatus.none);
      expect(subscriptionStatusFromWire('blocked'), SubscriptionStatus.none);
    });
  });

  group('Channel.fromJson', () {
    test('reads all fields', () {
      final c = Channel.fromJson({
        'id': 'c1',
        'publicId': 'pub_abc',
        'ownerId': 'u1',
        'name': 'Alerts',
        'subscriptionMode': 'open',
        'status': 'active',
        'createdAt': '2026-01-01T00:00:00Z',
      });
      expect(c.id, 'c1');
      expect(c.publicId, 'pub_abc');
      expect(c.name, 'Alerts');
      expect(c.subscriptionMode, SubscriptionMode.open);
      expect(c.status, ChannelStatus.active);
    });

    test('tolerates missing fields', () {
      final c = Channel.fromJson({});
      expect(c.id, '');
      expect(c.subscriptionMode, SubscriptionMode.approval);
    });
  });

  group('Message.fromJson', () {
    test('reads fields and optional data map', () {
      final m = Message.fromJson({
        'id': 'm1',
        'channelId': 'c1',
        'title': 'Deploy',
        'body': 'done',
        'createdAt': '2026-01-01T00:00:00Z',
        'data': {'k': 'v'},
      });
      expect(m.id, 'm1');
      expect(m.title, 'Deploy');
      expect(m.data, {'k': 'v'});
    });

    test('data is null when absent', () {
      expect(Message.fromJson({'id': 'm1'}).data, isNull);
    });

    test('parses images with dimensions', () {
      final m = Message.fromJson({
        'id': 'm1',
        'images': [
          {'id': 'img1', 'width': 1000, 'height': 562},
          {'id': 'img2', 'width': 800, 'height': 800},
        ],
      });
      expect(m.images, hasLength(2));
      expect(m.images.first.id, 'img1');
      expect(m.images.first.width, 1000);
      expect(m.images.first.aspectRatio, closeTo(1000 / 562, 0.001));
      expect(m.images[1].aspectRatio, 1);
    });

    test('images defaults to empty list when absent', () {
      expect(Message.fromJson({'id': 'm1'}).images, isEmpty);
    });
  });

  group('ApiKey', () {
    test('revoked reflects revokedAt presence', () {
      final live = ApiKey.fromJson({'id': 'k1'});
      final dead = ApiKey.fromJson({
        'id': 'k2',
        'revokedAt': '2026-01-02T00:00:00Z',
      });
      expect(live.revoked, isFalse);
      expect(dead.revoked, isTrue);
    });
  });

  group('LiveEvent.fromJson', () {
    test('parses a channel broadcast', () {
      final e = LiveEvent.fromJson({
        'channelId': 'c1',
        'message': {'id': 'm1', 'title': 'hi'},
      });
      expect(e.channelId, 'c1');
      expect(e.message?.id, 'm1');
      expect(e.message?.title, 'hi');
      expect(e.chatMessage, isNull);
      expect(e.callSignal, isNull);
    });

    // The stream puts every shape on one event name and tells them apart by which fields are set.
    // This used to require `message`, and SseClient swallowed the resulting exception — so every
    // chat message and every incoming call was silently dropped. These three pin that shut.
    test('parses a conversation message without a channel message', () {
      final e = LiveEvent.fromJson({
        'conversationId': 'v1',
        'chatMessage': {
          'id': 'cm1',
          'conversationId': 'v1',
          'senderId': 'u1',
          'contentType': 'application/mls',
          'ciphertext': 'aGVsbG8=',
        },
      });
      expect(e.conversationId, 'v1');
      expect(e.chatMessage?.id, 'cm1');
      expect(e.chatMessage?.ciphertext, utf8.encode('hello'));
      expect(e.message, isNull);
    });

    test('parses a call nudge', () {
      final e = LiveEvent.fromJson({
        'conversationId': 'v1',
        'callSignal': {'callId': 'call-1', 'seq': 3, 'fromUserId': 'u2'},
      });
      expect(e.callSignal?.callId, 'call-1');
      expect(e.callSignal?.seq, 3);
      expect(e.callSignal?.fromUserId, 'u2');
      expect(e.message, isNull);
    });

    test('parses a conversation deletion', () {
      final e = LiveEvent.fromJson({
        'conversationId': 'v1',
        'conversationDeleted': true,
      });
      expect(e.conversationId, 'v1');
      expect(e.conversationDeleted, isTrue);
      expect(e.message, isNull);
    });
  });

  group('Channel.alias', () {
    test('reads alias and exposes joinRef preferring it over publicId', () {
      final withAlias = Channel.fromJson({
        'id': 'c1',
        'publicId': 'ch_abc',
        'name': 'A',
        'alias': 'skg_news',
      });
      expect(withAlias.alias, 'skg_news');
      expect(withAlias.joinRef, 'skg_news');

      final noAlias = Channel.fromJson({'id': 'c2', 'publicId': 'ch_def'});
      expect(noAlias.alias, isNull);
      expect(noAlias.joinRef, 'ch_def');
    });
  });

  group('ChannelMember.fromJson', () {
    test('reads role, status and the public identity', () {
      final m = ChannelMember.fromJson({
        'id': 'm1',
        'channelId': 'c1',
        'userId': 'u1',
        'username': 'ada',
        'displayName': 'Ada Lovelace',
        'role': 'admin',
        'status': 'pending',
      });
      expect(m.userId, 'u1');
      expect(m.username, 'ada');
      expect(m.displayName, 'Ada Lovelace');
      expect(m.role, ChannelRole.admin);
      expect(m.status, MemberStatus.pending);
    });

    // The label is what every subscriber row, action sheet and confirmation prints. It used to be
    // the email address; a row with nothing to print cannot be acted on, so it must never be empty.
    test(
      'labels a member by name, then handle, then a stub — never nothing',
      () {
        ChannelMember at(Map<String, dynamic> extra) =>
            ChannelMember.fromJson({'userId': 'abcdef123456', ...extra});

        expect(at({'displayName': 'Ada', 'username': 'ada'}).label, 'Ada');
        expect(at({'username': 'ada'}).label, '@ada');
        expect(at({}).label, 'User abcdef');
      },
    );

    // The promise the server now keeps — see withProfiles. Pinned here too, because this is the
    // model that would happily surface it again if the field came back.
    test('carries no email even when the server sends one', () {
      final m = ChannelMember.fromJson({
        'userId': 'u1',
        'username': 'ada',
        'email': 'ada@example.com',
      });
      expect(m.label, '@ada');
      expect(m.toString(), isNot(contains('example.com')));
    });
  });

  group('JoinedChannel.fromJson', () {
    test('reads memberStatus (not the channel status) and role', () {
      final j = JoinedChannel.fromJson({
        'id': 'c1',
        'publicId': 'ch_abc',
        'name': 'A',
        'status': 'active',
        'role': 'user',
        'memberStatus': 'pending',
      });
      expect(j.channel.id, 'c1');
      expect(j.role, ChannelRole.user);
      expect(j.memberStatus, MemberStatus.pending);
    });
  });
}
