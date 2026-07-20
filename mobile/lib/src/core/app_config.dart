import 'package:flutter/material.dart';

/// Default API base — the hosted production App API. Override in Settings to
/// point at a local backend (e.g. `http://10.0.2.2:8080` from the Android
/// emulator, or `http://localhost:8080` from the iOS simulator).
///
/// A compile-time `--dart-define=PHEME_API=...` takes precedence over this, so a
/// dev build can point at a local server without a saved setting — which is the
/// only way to reach a backend before the first screen, and how the integration
/// tests already select their API. An empty define (the default) falls through
/// to production.
const String _envBaseUrl = String.fromEnvironment('PHEME_API');

/// The release this build came from, for the About screen.
///
/// Passed at build time the same way the web app takes it, so the two report the same string for
/// the same release: --dart-define=PHEME_VERSION=v1.2.3. A local build says "dev", which is the
/// honest answer for something that did not come from a tag.
const String appVersion = String.fromEnvironment(
  'PHEME_VERSION',
  defaultValue: 'dev',
);

/// Shown in About. Not translated: a name is a name.
const String appCopyright = '© 2026 Mikhail Matveev';
const String appWebsite = 'https://app.example.com';
const String kDefaultBaseUrl = _envBaseUrl == ''
    ? 'https://pheme-prod-api.rh1.tech'
    : _envBaseUrl;

/// App-wide initial state loaded from persistent storage at startup. Provided
/// via an override in main(); the in-app controllers seed themselves from it.
class InitialAppState {
  const InitialAppState({
    required this.themeMode,
    required this.locale,
    required this.baseUrl,
    required this.deviceId,
  });

  final ThemeMode themeMode;
  final Locale? locale;
  final String baseUrl;

  /// Locally-registered push device id, or null if the device hasn't been
  /// registered for notifications yet.
  final String? deviceId;
}
