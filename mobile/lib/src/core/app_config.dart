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
/// A build with no define has NO server, and that is deliberate.
///
/// There used to be a hardcoded fallback here — the Android emulator's route to a
/// developer machine. It made the app look like it knew where to go when it did
/// not, and on a product that is self-hosted as often as not, guessing an address
/// is worse than admitting there isn't one: the sign-in screen shows an empty
/// server field and asks, which is the honest question.
///
/// The define still works and is how a build is pointed at a deployment. It seeds
/// the field rather than hiding it; the user can see the address they are about
/// to sign in against, and correct it.
///
/// For a local backend, type it once on the sign-in screen — `http://10.0.2.2:8080`
/// from the Android emulator, `http://localhost:8080` from the iOS simulator. It
/// persists, so it is once per install rather than once per launch.
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
const String appWebsite = 'https://github.com/rh1tech/pheme';

/// What the sign-in screen's server field starts with: the compiled address, or
/// nothing at all.
const String kDefaultBaseUrl = _envBaseUrl;

/// App-wide initial state loaded from persistent storage at startup. Provided
/// via an override in main(); the in-app controllers seed themselves from it.
class InitialAppState {
  const InitialAppState({
    required this.themeMode,
    required this.locale,
    required this.baseUrl,
    required this.savedBaseUrl,
    required this.deviceId,
  });

  final ThemeMode themeMode;
  final Locale? locale;

  /// What the app talks to: the address this install has been pointed at, or the compiled one.
  final String baseUrl;

  /// The address the USER chose, or null if they never have.
  ///
  /// Distinct from [baseUrl] on purpose. A build compiled with PHEME_API still has somewhere to
  /// talk to, but nobody has told this install where their server is — and the sign-in screen must
  /// not present a compiled-in address as though it were the user's answer. An empty field asks the
  /// question; a filled one pretends it has already been answered.
  final String? savedBaseUrl;

  /// Locally-registered push device id, or null if the device hasn't been
  /// registered for notifications yet.
  final String? deviceId;
}
