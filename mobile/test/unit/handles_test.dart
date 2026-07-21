import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/chat/handles.dart';

void main() {
  group('remoteHandle', () {
    test('accepts username@full-domain', () {
      expect(
        remoteHandle('medved@hub.example.com'),
        'medved@hub.example.com',
      );
    });

    test('accepts username@alias (dot-free host)', () {
      expect(remoteHandle('bear@kn87r'), 'bear@kn87r');
    });

    test('trims surrounding whitespace', () {
      expect(remoteHandle('  bear@kn87r  '), 'bear@kn87r');
    });

    test('rejects a plain name with no @', () {
      expect(remoteHandle('medved'), isNull);
    });

    test('rejects a bare @ with no host', () {
      expect(remoteHandle('bear@'), isNull);
    });

    test('rejects a too-short username', () {
      expect(remoteHandle('ab@kn87r'), isNull);
    });
  });
}
