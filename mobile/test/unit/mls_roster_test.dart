import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/crypto/mls_device.dart';

void main() {
  const members = [
    (userId: 'alice', domain: ''), // local
    (userId: 'bob', domain: 'b.example'), // remote, no leaf yet
    (userId: 'carol', domain: 'c.example'), // remote, already a leaf
  ];

  group('remoteMemberRefs', () {
    test('returns only remote members with no leaf yet', () {
      final refs = remoteMemberRefs(members, [
        'mimi://c.example/d/carol/phone',
      ], 'alice');
      expect(refs, [(userId: 'bob', domain: 'b.example')]);
    });

    test('skips ourselves and local members', () {
      expect(
        remoteMemberRefs([(userId: 'me', domain: 'b.example')], const [], 'me'),
        isEmpty,
      );
      expect(
        remoteMemberRefs([(userId: 'x', domain: '')], const [], 'me'),
        isEmpty,
      );
    });

    test('is empty when nobody carries a domain (single-host)', () {
      expect(
        remoteMemberRefs(
          [(userId: 'a', domain: ''), (userId: 'b', domain: '')],
          const [],
          'a',
        ),
        isEmpty,
      );
    });
  });

  test('domainsByUser maps only the remote members', () {
    expect(domainsByUser(members), {'bob': 'b.example', 'carol': 'c.example'});
  });

  test('deviceIdentity qualifies a remote member under their own domain', () {
    expect(
      deviceIdentity('bob', 'phone', 'b.example'),
      'mimi://b.example/d/bob/phone',
    );
    expect(domainOf('mimi://b.example/d/bob/phone'), 'b.example');
  });
}
