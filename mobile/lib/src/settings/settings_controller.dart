import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';

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
    final trimmed = url.trim();
    if (trimmed.isEmpty || trimmed == state.baseUrl) return;
    state = state.copyWith(baseUrl: trimmed);
    await ref.read(settingsStoreProvider).saveBaseUrl(trimmed);
  }
}
