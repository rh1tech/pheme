import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/chat/handles.dart';

void main() {
  group('remoteHandle', () {
    test('accepts username@full-domain', () {
      expect(
        remoteHandle('medved@chat.example.com'),
        'medved@chat.example.com',
      );
    });

    test('accepts username@alias (dot-free host)', () {
      expect(remoteHandle('bear@pheme1'), 'bear@pheme1');
    });

    test('trims surrounding whitespace', () {
      expect(remoteHandle('  bear@pheme1  '), 'bear@pheme1');
    });

    test('rejects a plain name with no @', () {
      expect(remoteHandle('medved'), isNull);
    });

    test('rejects a bare @ with no host', () {
      expect(remoteHandle('bear@'), isNull);
    });

    test('rejects a too-short username', () {
      expect(remoteHandle('ab@pheme1'), isNull);
    });
  });
}
