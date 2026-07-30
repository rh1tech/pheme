import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/admin/admin_invites_page.dart';
import 'package:pheme_mobile/src/admin/admin_models.dart';

/// The admin payloads are decoded from a server that is allowed to be older than this build, so
/// the decoding is tested for what it does with fields that are missing, empty or unrecognised —
/// which is where an admin screen would otherwise throw inside a list builder.
void main() {
  group('AdminPage', () {
    test('knows whether another page exists', () {
      const full = AdminPage<int>(items: [], total: 45, page: 1, limit: 20);
      expect(full.hasMore, isTrue);
      const last = AdminPage<int>(items: [], total: 45, page: 3, limit: 20);
      expect(last.hasMore, isFalse);
      // Exactly full: page 2 would be empty, so there is no more.
      const exact = AdminPage<int>(items: [], total: 40, page: 2, limit: 20);
      expect(exact.hasMore, isFalse);
    });
  });

  group('AdminUser', () {
    test('reads a full row', () {
      final u = AdminUser.fromJson({
        'id': 'u1',
        'email': 'a@b.com',
        'role': 'admin',
        'status': 'blocked',
        'createdAt': '2026-01-02T03:04:05Z',
        'channelCount': 3,
        'displayName': 'Anna',
      });
      expect(u.isAdmin, isTrue);
      expect(u.isBlocked, isTrue);
      expect(u.channelCount, 3);
      expect(u.label, 'Anna');
    });

    // An account created before the status field existed sends it empty, and the server treats
    // that as active — so must this, or every old account renders as blank.
    test('an empty status reads as active', () {
      final u = AdminUser.fromJson({'email': 'a@b.com', 'status': ''});
      expect(u.status, 'active');
      expect(u.isBlocked, isFalse);
      expect(u.isDisabled, isFalse);
    });

    test('falls back to the email when there is no display name', () {
      expect(AdminUser.fromJson({'email': 'a@b.com'}).label, 'a@b.com');
      expect(
        AdminUser.fromJson({'email': 'a@b.com', 'displayName': ''}).label,
        'a@b.com',
      );
    });

    test('an empty payload decodes rather than throwing', () {
      final u = AdminUser.fromJson(const {});
      expect(u.role, 'user');
      expect(u.status, 'active');
      expect(u.channelCount, 0);
    });
  });

  group('AdminInvite', () {
    test('reads the status and the once-only code', () {
      final i = AdminInvite.fromJson({
        'id': 'i1',
        'prefix': 'AbC123',
        'status': 'used',
        'createdAt': '2026-01-02T03:04:05Z',
        'usedBy': 'u1',
        'code': 'the-code',
      });
      expect(i.status, InviteStatus.used);
      expect(i.code, 'the-code');
      expect(i.usedBy, 'u1');
    });

    // A listing never carries the code — that is the security property, and worth asserting so a
    // future change that starts showing it fails here rather than in production.
    test('a listing row has no code', () {
      final i = AdminInvite.fromJson({'id': 'i1', 'status': 'pending'});
      expect(i.code, isNull);
    });

    test('a status this build does not know reads as pending', () {
      expect(InviteStatus.parse('quarantined'), InviteStatus.pending);
      expect(InviteStatus.parse(null), InviteStatus.pending);
      expect(InviteStatus.parse('expired'), InviteStatus.expired);
    });
  });

  group('AdminStats', () {
    test('reads totals and lists', () {
      final s = AdminStats.fromJson({
        'users': 12,
        'channels': 3,
        'messages': 900,
        'deliveries': 4000,
        'devices': 20,
        'topChannels': [
          {'channelId': 'c1', 'name': 'Alerts', 'count': 500},
        ],
        'recentMessages': [
          {'id': 'm1', 'channelId': 'c1', 'title': 'Deploy', 'body': 'done'},
        ],
      });
      expect(s.users, 12);
      expect(s.topChannels.single.name, 'Alerts');
      expect(s.recentMessages.single.title, 'Deploy');
    });

    test('absent lists decode as empty, not null', () {
      final s = AdminStats.fromJson(const {});
      expect(s.topChannels, isEmpty);
      expect(s.recentMessages, isEmpty);
      expect(s.messages, 0);
    });
  });

  group('invite app link', () {
    test('carries the code and the server, escaped', () {
      final link = inviteAppLink(
        code: 'a+b/c=',
        server: 'https://host.example/x7f',
      );
      final uri = Uri.parse(link);
      expect(uri.scheme, 'pheme');
      expect(uri.host, 'invite');
      // Round-trips: whatever escaping the builder chose, the parser must get the original back.
      expect(uri.queryParameters['code'], 'a+b/c=');
      expect(uri.queryParameters['server'], 'https://host.example/x7f');
    });

    test('omits the server when there is not one', () {
      final uri = Uri.parse(inviteAppLink(code: 'abc', server: ''));
      expect(uri.queryParameters.containsKey('server'), isFalse);
      expect(uri.queryParameters['code'], 'abc');
    });
  });
}
