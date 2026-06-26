import 'package:flutter/material.dart';

/// Default API base — the hosted production App API. Override in Settings to
/// point at a local backend (e.g. `http://10.0.2.2:8080` from the Android
/// emulator, or `http://localhost:8080` from the iOS simulator).
const String kDefaultBaseUrl = 'https://pheme-prod-api.rh1.tech';

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
