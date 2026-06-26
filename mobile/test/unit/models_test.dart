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
    test('parses nested message', () {
      final e = LiveEvent.fromJson({
        'channelId': 'c1',
        'message': {'id': 'm1', 'title': 'hi'},
      });
      expect(e.channelId, 'c1');
      expect(e.message.id, 'm1');
      expect(e.message.title, 'hi');
    });
  });
}
