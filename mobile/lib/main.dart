import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'src/app.dart';
import 'src/core/app_config.dart';
import 'src/core/providers.dart';
import 'src/core/settings_store.dart';
import 'src/core/token_store.dart';
import 'src/push/push_service.dart';
import 'src/rust/frb_generated.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Loads the Rust MLS library. Everything encrypted goes through it, so a failure here is fatal
  // rather than degraded: without it the app cannot read a single message.
  await RustLib.init();

  // Load persisted state before the first frame so controllers seed
  // synchronously (auth identity, theme/locale, server URL, push device).
  const storage = FlutterSecureStorage();
  final tokenStore = TokenStore(storage);
  final settingsStore = SettingsStore(storage);

  await tokenStore.load();
  final initial = InitialAppState(
    themeMode: await settingsStore.loadThemeMode(),
    locale: await settingsStore.loadLocale(),
    baseUrl: await settingsStore.loadBaseUrl() ?? kDefaultBaseUrl,
    deviceId: await settingsStore.loadDeviceId(),
  );

  // Best-effort push init; never blocks startup if Firebase isn't configured.
  final push = PushService();
  await push.init();

  runApp(
    ProviderScope(
      overrides: [
        initialAppStateProvider.overrideWithValue(initial),
        secureStorageProvider.overrideWithValue(storage),
        tokenStoreProvider.overrideWithValue(tokenStore),
        settingsStoreProvider.overrideWithValue(settingsStore),
        pushServiceProvider.overrideWithValue(push),
      ],
      child: const PhemeApp(),
    ),
  );
}
