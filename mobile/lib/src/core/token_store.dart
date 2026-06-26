import 'package:flutter_secure_storage/flutter_secure_storage.dart';

class Tokens {
  const Tokens({required this.accessToken, required this.refreshToken});

  final String accessToken;
  final String refreshToken;
}

/// Persists the JWT access/refresh tokens in the platform secure store and keeps
/// an in-memory copy so the Dio interceptor can read them synchronously.
class TokenStore {
  TokenStore(this._storage);

  static const _accessKey = 'pheme.accessToken';
  static const _refreshKey = 'pheme.refreshToken';

  final FlutterSecureStorage _storage;
  Tokens? _cache;

  Tokens? get current => _cache;

  Future<Tokens?> load() async {
    final access = await _storage.read(key: _accessKey);
    final refresh = await _storage.read(key: _refreshKey);
    if (access == null ||
        refresh == null ||
        access.isEmpty ||
        refresh.isEmpty) {
      _cache = null;
      return null;
    }
    _cache = Tokens(accessToken: access, refreshToken: refresh);
    return _cache;
  }

  Future<void> save(Tokens tokens) async {
    _cache = tokens;
    await _storage.write(key: _accessKey, value: tokens.accessToken);
    await _storage.write(key: _refreshKey, value: tokens.refreshToken);
  }

  Future<void> clear() async {
    _cache = null;
    await _storage.delete(key: _accessKey);
    await _storage.delete(key: _refreshKey);
  }
}
