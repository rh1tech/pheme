// What counts as a server address, and what it becomes once stored.
//
// This is the string an operator reads out and a user types on the sign-in screen before they have
// an account, so getting it wrong locks somebody out of a server that is working perfectly well.
// The rule is pure precisely so it can be pinned here rather than discovered on a phone.

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/core/validators.dart';

void main() {
  group('normalizeServerUrl accepts what people actually type', () {
    test('a bare hostname gains https', () {
      expect(
        normalizeServerUrl('pheme.example.com'),
        'https://pheme.example.com',
      );
    });

    test('a bare hostname with the unlisted path prefix keeps the prefix', () {
      // The form a self-host operator hands over, and the reason the scheme cannot be required.
      expect(
        normalizeServerUrl('host.example/a7f3c91e4b2d'),
        'https://host.example/a7f3c91e4b2d',
      );
    });

    test('an explicit scheme is left alone', () {
      expect(
        normalizeServerUrl('https://pheme.example'),
        'https://pheme.example',
      );
    });

    test('http survives, because a local backend is a deliberate act', () {
      expect(
        normalizeServerUrl('http://10.0.2.2:8080'),
        'http://10.0.2.2:8080',
      );
    });

    test('a host:port is a host and a port, not a scheme', () {
      // `10.0.2.2:8080` looks like scheme+path to a naive parser. A scheme starts with a LETTER.
      expect(normalizeServerUrl('10.0.2.2:8080'), 'https://10.0.2.2:8080');
      expect(
        normalizeServerUrl('host.example:8443'),
        'https://host.example:8443',
      );
    });

    test('surrounding whitespace is forgiven', () {
      expect(normalizeServerUrl('  pheme.example  '), 'https://pheme.example');
    });

    test('a trailing slash is dropped', () {
      // A pasted browser URL brings one along, and joining "/v1/..." onto it yields "//v1/...",
      // which fails invisibly.
      expect(
        normalizeServerUrl('https://pheme.example/'),
        'https://pheme.example',
      );
      expect(
        normalizeServerUrl('pheme.example/prefix/'),
        'https://pheme.example/prefix',
      );
    });
  });

  group('normalizeServerUrl refuses what cannot be a server', () {
    test('nothing at all', () {
      expect(normalizeServerUrl(''), isNull);
      expect(normalizeServerUrl('   '), isNull);
    });

    test('a scheme we cannot speak', () {
      expect(normalizeServerUrl('ftp://pheme.example'), isNull);
      expect(normalizeServerUrl('wss://pheme.example'), isNull);
    });

    test('a scheme with no host behind it', () {
      expect(normalizeServerUrl('https://'), isNull);
      expect(normalizeServerUrl('https:///a/path'), isNull);
    });
  });

  group('isValidServerUrl agrees with it', () {
    test('accepts a bare hostname', () {
      expect(isValidServerUrl('pheme.example.com'), isTrue);
    });

    test('rejects an empty field', () {
      expect(isValidServerUrl(''), isFalse);
    });
  });
}
