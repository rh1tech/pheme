import 'package:flutter/material.dart';

/// The API base, supplied at build time.
///
///     flutter build apk --dart-define=PHEME_API=https://host.example/<prefix>
///
/// A server mounts its API under an unlisted path prefix, with a decoy site at
/// the document root, so that nothing on the host behaves like an API to anyone
/// who does not already know the path. The base URL therefore carries that
/// prefix, and the prefix is deliberately in NO committed file — it lives in the
/// deployment's stack.env and nowhere else. So there is no useful default to
/// hardcode here: the bare hostname without a prefix reaches the decoy site, not
/// the API.
///
/// A build with no define falls through to [kFallbackBaseUrl], which is a local
/// backend. That is the right failure: a developer build reaches a developer's
/// server, and a release build that forgot the define fails immediately and
/// obviously rather than quietly talking to the wrong host.
///
/// Users of a self-hosted instance do not need any of this — they scan the QR
/// their operator gives them, or paste the URL, in Settings → Server.
const String _envBaseUrl = String.fromEnvironment('PHEME_API');

/// Where a build with no `PHEME_API` define points: the Android emulator's route
/// to the host machine. `http://localhost:8080` from the iOS simulator.
const String kFallbackBaseUrl = 'http://10.0.2.2:8080';

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
    ? kFallbackBaseUrl
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
