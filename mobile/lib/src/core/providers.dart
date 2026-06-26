import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import '../auth/auth_controller.dart';
import '../data/pheme_repository.dart';
import '../push/device_controller.dart';
import '../push/push_service.dart';
import '../settings/settings_controller.dart';
import 'api_client.dart';
import 'app_config.dart';
import 'settings_store.dart';
import 'token_store.dart';

/// Loaded-at-startup state; overridden in main() after reading persisted values.
final initialAppStateProvider = Provider<InitialAppState>(
  (ref) => throw UnimplementedError(
    'initialAppStateProvider must be overridden in main()',
  ),
);

final secureStorageProvider = Provider<FlutterSecureStorage>(
  (ref) => const FlutterSecureStorage(),
);

final tokenStoreProvider = Provider<TokenStore>(
  (ref) => TokenStore(ref.watch(secureStorageProvider)),
);

final settingsStoreProvider = Provider<SettingsStore>(
  (ref) => SettingsStore(ref.watch(secureStorageProvider)),
);

final settingsControllerProvider =
    NotifierProvider<SettingsController, SettingsState>(SettingsController.new);

final authControllerProvider = NotifierProvider<AuthController, AuthState>(
  AuthController.new,
);

/// The Dio client. Rebuilt when the configured base URL changes; refresh
/// failures route to [AuthController.onSessionExpired].
final dioProvider = Provider<Dio>((ref) {
  final baseUrl = ref.watch(
    settingsControllerProvider.select((s) => s.baseUrl),
  );
  final tokenStore = ref.watch(tokenStoreProvider);
  final dio = buildDio(
    baseUrl: baseUrl,
    tokenStore: tokenStore,
    onAuthFailure: () =>
        ref.read(authControllerProvider.notifier).onSessionExpired(),
  );
  ref.onDispose(dio.close);
  return dio;
});

final repositoryProvider = Provider<PhemeRepository>(
  (ref) => PhemeRepository(ref.watch(dioProvider)),
);

/// Single FCM/push facade. Overridden in main() with an already-initialized
/// instance so push handlers are wired before the first frame.
final pushServiceProvider = Provider<PushService>((ref) => PushService());

/// The locally-registered push device id (null until notifications are enabled).
final deviceControllerProvider = NotifierProvider<DeviceController, String?>(
  DeviceController.new,
);
