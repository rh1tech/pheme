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
}
