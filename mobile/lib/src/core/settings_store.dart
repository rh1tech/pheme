import 'package:flutter/material.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Lightweight key/value preferences (theme, locale, API base URL) persisted in
/// the platform store. Not secret, but reuses the same backing storage.
class SettingsStore {
  SettingsStore(this._storage);

  static const _themeKey = 'pheme.themeMode';
  static const _localeKey = 'pheme.locale';
  static const _baseUrlKey = 'pheme.baseUrl';
  static const _deviceIdKey = 'pheme.deviceId';

  /// The push token this device last told the server about.
  ///
  /// Kept so a launch can tell whether the token has changed since. FCM rotates tokens, and a
  /// rotation that happens while the app is not running raises no event it can hear — so without
  /// something to compare against, the app has no way to know the address the server holds for it
  /// has stopped working.
  static const _registeredTokenKey = 'pheme.registeredPushToken';

  /// Whether the registration the server holds carried this device's MLS identity.
  ///
  /// Registration can happen before that identity exists, and the server will not send message
  /// previews to a push address it cannot trace to an MLS device — so a device that registered too
  /// early shows "New message" forever unless it registers again.
  static const _registeredMlsKey = 'pheme.registeredMlsIdentity';

  final FlutterSecureStorage _storage;

  Future<String?> read(String key) => _storage.read(key: key);

  Future<ThemeMode> loadThemeMode() async {
    switch (await _storage.read(key: _themeKey)) {
      case 'light':
        return ThemeMode.light;
      case 'dark':
        return ThemeMode.dark;
      default:
        return ThemeMode.system;
    }
  }

  Future<void> saveThemeMode(ThemeMode mode) =>
      _storage.write(key: _themeKey, value: mode.name);

  Future<Locale?> loadLocale() async {
    final code = await _storage.read(key: _localeKey);
    if (code == null || code.isEmpty) return null;
    return Locale(code);
  }

  Future<void> saveLocale(Locale? locale) async {
    if (locale == null) {
      await _storage.delete(key: _localeKey);
    } else {
      await _storage.write(key: _localeKey, value: locale.languageCode);
    }
  }

  Future<String?> loadBaseUrl() => _storage.read(key: _baseUrlKey);

  Future<void> saveBaseUrl(String url) =>
      _storage.write(key: _baseUrlKey, value: url);

  /// Locally-registered push device id, used to subscribe/unsubscribe channels.
  Future<String?> loadDeviceId() => _storage.read(key: _deviceIdKey);

  Future<void> saveDeviceId(String id) =>
      _storage.write(key: _deviceIdKey, value: id);

  Future<void> clearDeviceId() => _storage.delete(key: _deviceIdKey);

  Future<String?> loadRegisteredPushToken() =>
      _storage.read(key: _registeredTokenKey);

  Future<void> saveRegisteredPushToken(String token) =>
      _storage.write(key: _registeredTokenKey, value: token);

  Future<bool> loadRegisteredMlsIdentity() async =>
      await _storage.read(key: _registeredMlsKey) == 'true';

  Future<void> saveRegisteredMlsIdentity(bool linked) =>
      _storage.write(key: _registeredMlsKey, value: linked ? 'true' : 'false');
}
