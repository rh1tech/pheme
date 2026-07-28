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
  static const _registeredPreviewKey = 'pheme.registeredCanRenderPreview';

  /// When this device first looked at its chats.
  ///
  /// Read state is per-device and does not sync, so a freshly installed app knows nothing about
  /// what has already been read elsewhere — and treating "no record" as unread lit up every
  /// conversation the account had ever had. This is the line before which history is taken as
  /// already dealt with.
  static const _readBaselineKey = 'pheme.readBaseline';

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

  Future<String?> loadReadBaseline() => _storage.read(key: _readBaselineKey);

  Future<void> saveReadBaseline(String iso) =>
      _storage.write(key: _readBaselineKey, value: iso);

  Future<bool> loadRegisteredMlsIdentity() async =>
      await _storage.read(key: _registeredMlsKey) == 'true';

  Future<void> saveRegisteredMlsIdentity(bool linked) =>
      _storage.write(key: _registeredMlsKey, value: linked ? 'true' : 'false');

  /// Whether the server was last told this device can render its own previews.
  ///
  /// A BUILD fact, not a device one, which is why it has to be remembered: the capability changes
  /// when the app is upgraded, and an upgrade is exactly the moment nothing else about the
  /// registration changes. iOS gained the NotificationServiceExtension and reported `true` from
  /// then on — but every device already registered kept its old `false` on the server, and went on
  /// receiving generic text with the extension sitting there unused.
  ///
  /// Absent means false, which is the safe default: it means an install that predates this asks
  /// once, on its next launch, rather than assuming the server already knows.
  Future<bool> loadRegisteredCanRenderPreview() async =>
      await _storage.read(key: _registeredPreviewKey) == 'true';

  Future<void> saveRegisteredCanRenderPreview(bool can) => _storage.write(
    key: _registeredPreviewKey,
    value: can ? 'true' : 'false',
  );
}
