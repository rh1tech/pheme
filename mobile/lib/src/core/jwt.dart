import 'dart:convert';

/// Minimal JWT payload decoding for display/identity only — the server is the
/// authority on validity. Mirrors web/src/lib/jwt.ts.
Map<String, dynamic> _decode(String accessToken) {
  try {
    final parts = accessToken.split('.');
    if (parts.length < 2) return const {};
    var payload = parts[1].replaceAll('-', '+').replaceAll('_', '/');
    switch (payload.length % 4) {
      case 2:
        payload += '==';
        break;
      case 3:
        payload += '=';
        break;
    }
    final json = utf8.decode(base64.decode(payload));
    return jsonDecode(json) as Map<String, dynamic>;
  } catch (_) {
    return const {};
  }
}

String? decodeUserId(String accessToken) =>
    _decode(accessToken)['sub'] as String?;

String? decodeRole(String accessToken) =>
    _decode(accessToken)['role'] as String?;
