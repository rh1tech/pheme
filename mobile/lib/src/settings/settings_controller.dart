import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';
import '../core/validators.dart';

class SettingsState {
  const SettingsState({
    required this.themeMode,
    required this.locale,
    required this.baseUrl,
  });

  final ThemeMode themeMode;

  /// null = follow the device language.
  final Locale? locale;
  final String baseUrl;

  SettingsState copyWith({
    ThemeMode? themeMode,
    Locale? locale,
    bool clearLocale = false,
    String? baseUrl,
  }) => SettingsState(
    themeMode: themeMode ?? this.themeMode,
    locale: clearLocale ? null : (locale ?? this.locale),
    baseUrl: baseUrl ?? this.baseUrl,
  );
}

/// Holds theme/locale/baseUrl, seeded from [InitialAppState] and persisted via
/// [SettingsStore] on every change.
class SettingsController extends Notifier<SettingsState> {
  @override
  SettingsState build() {
    final initial = ref.read(initialAppStateProvider);
    return SettingsState(
      themeMode: initial.themeMode,
      locale: initial.locale,
      baseUrl: initial.baseUrl,
    );
  }

  Future<void> setThemeMode(ThemeMode mode) async {
    state = state.copyWith(themeMode: mode);
    await ref.read(settingsStoreProvider).saveThemeMode(mode);
  }

  Future<void> setLocale(Locale? locale) async {
    state = state.copyWith(locale: locale, clearLocale: locale == null);
    await ref.read(settingsStoreProvider).saveLocale(locale);
  }

  Future<void> setBaseUrl(String url) async {
    // Normalised HERE, so it cannot matter which screen the address came from. A bare hostname typed
    // on the sign-in form and one pasted into Settings have to end up as the same stored string, or
    // the app talks to two different addresses depending on where you entered it.
    final trimmed = normalizeServerUrl(url);
    if (trimmed == null) return;
    state = state.copyWith(baseUrl: trimmed);
    // Written even when it matches what is already in force. It used to return early on a match,
    // which is fine as an optimisation and wrong as a record: a build compiled with PHEME_API is
    // ALREADY pointed there, so signing in against that very address stored nothing, and the
    // install could never tell "the user chose this" from "this was baked in". See
    // InitialAppState.savedBaseUrl.
    await ref.read(settingsStoreProvider).saveBaseUrl(trimmed);
  }
}
