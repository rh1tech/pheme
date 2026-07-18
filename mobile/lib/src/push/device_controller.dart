import 'dart:async';
import 'dart:io' show Platform;

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/providers.dart';

/// This device's server-issued id.
///
/// Two things need it, and they are NOT the same thing, which is why registering a device and enabling
/// push are now separate:
///
///   * PUSH. Needs a token, which needs Firebase and the user's permission. Optional: the app works
///     without it, over the live stream.
///   * THE CALL ANSWER-LOCK. Needs only the id. Every device the user is signed in on rings, and
///     exactly one may pick up — the server decides which, keyed on this id.
///
/// They used to be one call, and the consequence was that a user who declined notifications, or a Mac
/// with no Firebase, had no device id — and therefore could not ANSWER A CALL. The phone rang and the
/// button did nothing. Registering now always happens; the push token is attached when there is one.
class DeviceController extends Notifier<String?> {
  @override
  String? build() => ref.read(initialAppStateProvider).deviceId;

  bool get isRegistered => state != null;

  /// Registers this device with the server, with a push token if we can get one and without if we
  /// cannot. Returns the device id.
  Future<String> register() async {
    // A rotated token has to reach the server, and the only way it does is by registering again.
    // See PushService.onTokenRefresh — without this the app holds a device id forever and never
    // notices that the address behind it stopped working.
    ref.read(pushServiceProvider).onTokenRefresh((_) {
      state =
          null; // force the next ensureRegistered to re-register with the new token
      unawaited(ensureRegistered());
    });

    final existing = state;
    if (existing != null) return existing;

    final id = await ref
        .read(pushServiceProvider)
        .registerDevice(ref.read(repositoryProvider));

    await ref.read(settingsStoreProvider).saveDeviceId(id);
    state = id;
    return id;
  }

  /// Makes sure this device has an id, without asking for notification permission.
  ///
  /// Called before a call can be answered. It must not prompt: being asked for notification permission
  /// by a phone that is already ringing would be absurd, and declining it would silently make the call
  /// unanswerable.
  Future<String?> ensureRegistered() async {
    final existing = state;
    if (existing != null) return existing;

    try {
      return await register();
    } on Object {
      return null;
    }
  }

  /// True where push can work at all. macOS has no FCM in this app and no PushKit anywhere, so a Mac
  /// hears about a call over the live stream — which is the honest arrangement for a machine that is
  /// either open or off.
  bool get pushSupported => !Platform.isMacOS;
}
