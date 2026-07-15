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
