import 'dart:io';

import 'package:flutter/services.dart';

/// The proximity screen-off wake lock a call holds while it is live.
///
/// When the phone is at the ear the screen goes dark — so a cheek does not press the call controls,
/// and to save power. There is no Flutter API for it, so this drives a tiny platform channel that
/// acquires a PROXIMITY_SCREEN_OFF_WAKE_LOCK (see MainActivity.kt). Android only; a no-op elsewhere,
/// and best-effort — a call must never fail because a wake lock could not be taken.
class ProximityLock {
  const ProximityLock._();

  static const _channel = MethodChannel('pheme/proximity');

  static Future<void> acquire() => _invoke('acquire');
  static Future<void> release() => _invoke('release');

  static Future<void> _invoke(String method) async {
    if (!Platform.isAndroid) return;
    try {
      await _channel.invokeMethod<void>(method);
    } on Object {
      // Best effort. A missing sensor, an unsupported lock level, or a channel not yet wired must not
      // take the call down with it.
    }
  }
}
