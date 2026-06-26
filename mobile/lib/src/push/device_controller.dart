import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';

/// Holds the locally-registered push device id (null until the user enables
/// notifications). Seeded from persisted state at startup; [register] obtains
/// an FCM token, registers the device with the App API and persists the id.
class DeviceController extends Notifier<String?> {
  @override
  String? build() => ref.read(initialAppStateProvider).deviceId;

  bool get isRegistered => state != null;

  /// Registers this device for push. Throws [PushUnavailableException] when
  /// Firebase isn't configured or permission is denied; callers surface that.
  Future<String> register() async {
    final existing = state;
    if (existing != null) return existing;
    final id = await ref
        .read(pushServiceProvider)
        .registerDevice(ref.read(repositoryProvider));
    await ref.read(settingsStoreProvider).saveDeviceId(id);
    state = id;
    return id;
  }
}
