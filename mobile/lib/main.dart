import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:intl/date_symbol_data_local.dart';

import 'src/app.dart';
import 'src/calls/call_service.dart';
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

  // Date/time symbols for every locale the app ships.
  //
  // intl throws LocaleDataException the moment a DateFormat is built with an EXPLICIT locale that has
  // not been initialised — and the chat builds one for every timestamp it draws. Without this, opening
  // a conversation in Russian throws on the first message. English happens to work by accident,
  // because it is the fallback baked into intl, which is exactly why this was easy to miss.
  await initializeDateFormatting();

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

  // Best-effort push init. It must never hold up the first frame — not just when Firebase is
  // unconfigured (that path already returns), but when it is configured and its backend is
  // UNREACHABLE. Firebase.initializeApp() and getInitialMessage() await the messaging service, and on
  // a device launched with no connectivity — airplane mode, a dead zone, a broken emulator — that
  // wait does not fail, it hangs, and a hang is not something the try/catch inside init() can catch.
  // Awaited unguarded, it strands the app on the splash screen with no way forward.
  //
  // So cap it. If push is not ready in a few seconds, start without it; init() keeps running in the
  // background and its result is picked up when it arrives. The one thing lost by not waiting is the
  // notification that cold-started the app, and a user launching offline did not arrive by tapping a
  // push.
  final push = PushService();
  await push.init().timeout(const Duration(seconds: 5), onTimeout: () {});

  final container = ProviderContainer(
    overrides: [
      initialAppStateProvider.overrideWithValue(initial),
      secureStorageProvider.overrideWithValue(storage),
      tokenStoreProvider.overrideWithValue(tokenStore),
      settingsStoreProvider.overrideWithValue(settingsStore),
      pushServiceProvider.overrideWithValue(push),
    ],
  );

  // Listens for the platform ringer BEFORE the first frame, and outside the widget tree.
  //
  // A call has to be answerable when the app was not running at all — cold-launched in the background
  // by a VoIP push, with no route mounted and nothing on screen. If this lived in a widget, the accept
  // event would arrive before there was a widget to hear it, and the user would tap Answer on a call
  // screen wired to nothing.
  container.read(callServiceProvider).start();

  runApp(
    UncontrolledProviderScope(container: container, child: const PhemeApp()),
  );
}
