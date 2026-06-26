import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/core/jwt.dart';

String _makeToken(Map<String, dynamic> payload) {
  String seg(Map<String, dynamic> m) =>
      base64Url.encode(utf8.encode(jsonEncode(m))).replaceAll('=', '');
  return '${seg({'alg': 'HS256'})}.${seg(payload)}.signature';
}

void main() {
  group('jwt decoding', () {
    test('decodes sub and role', () {
      final token = _makeToken({'sub': 'user-123', 'role': 'admin'});
      expect(decodeUserId(token), 'user-123');
      expect(decodeRole(token), 'admin');
    });

    test('returns null for malformed token', () {
      expect(decodeUserId('not-a-jwt'), isNull);
      expect(decodeRole(''), isNull);
    });

    test('returns null when claim absent', () {
      final token = _makeToken({'sub': 'u1'});
      expect(decodeRole(token), isNull);
    });
  });
}
