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

/// When [accessToken] expires, or null if it carries no `exp`.
///
/// Needed because the server closes the SSE stream the moment the token that opened it expires, and
/// a reconnect that reuses the dead token just gets closed again. The client has to notice *before*
/// connecting, not after.
DateTime? decodeExpiry(String accessToken) {
  final exp = _decode(accessToken)['exp'];
  if (exp is! num) return null;
  return DateTime.fromMillisecondsSinceEpoch(exp.toInt() * 1000, isUtc: true);
}
