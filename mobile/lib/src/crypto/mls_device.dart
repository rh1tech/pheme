// This device's MLS device id — and the reason it is not the other device id.
//
// THERE ARE TWO DEVICE IDS AND THEY ARE NOT INTERCHANGEABLE.
//
//   * The PUSH device id is issued by the server from POST /v1/devices. It keys channel
//     subscriptions and, crucially, the call answer-lock. SettingsStore keeps it under
//     'pheme.deviceId'.
//   * The MLS device id is minted by the client and the server never sees it as an identity. It
//     names this device's leaf in every group, as `userId:deviceId`.
//
// The web client conflated them once, and MLS quietly overwrote the push id — which broke push and
// the answer lock together, in a way that looked like neither. They get separate keys here so that
// cannot happen again.

import 'dart:math';
import 'dart:typed_data';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';

/// Where the MLS device id lives. Deliberately NOT 'pheme.deviceId', which is the push one.
const _mlsDeviceIdKey = 'pheme.mlsDeviceId';

const _iosOptions = IOSOptions(
  accessibility: KeychainAccessibility.first_unlock,
);

Future<String?> loadMlsDeviceId(FlutterSecureStorage storage) =>
    storage.read(key: _mlsDeviceIdKey, iOptions: _iosOptions);

Future<void> saveMlsDeviceId(FlutterSecureStorage storage, String deviceId) =>
    storage.write(key: _mlsDeviceIdKey, value: deviceId, iOptions: _iosOptions);

Future<void> clearMlsDeviceId(FlutterSecureStorage storage) =>
    storage.delete(key: _mlsDeviceIdKey, iOptions: _iosOptions);

/// A leaf identity: `userId:deviceId`.
String deviceIdentity(String userId, String deviceId) => '$userId:$deviceId';

/// The user half of a leaf identity.
String userOf(String identity) {
  final sep = identity.indexOf(':');
  return sep == -1 ? '' : identity.substring(0, sep);
}

/// The device half of a leaf identity. Empty for a legacy identity that names only a person.
String deviceOf(String identity) {
  final sep = identity.indexOf(':');
  return sep == -1 ? '' : identity.substring(sep + 1);
}

/// A v4 UUID — a fresh MLS device id, and the id of a call.
///
/// Hand-rolled because Dart has no built-in generator and a package for twenty lines of hex is not
/// worth the dependency. The bytes come from the same CSPRNG the crypto uses.
///
/// A call id is a UUID and not just any unique string because CallKit's call identity IS a UUID: iOS
/// will not report a call under anything else.
String newUuid() {
  final random = Random.secure();
  final b = Uint8List.fromList(List.generate(16, (_) => random.nextInt(256)));
  b[6] = (b[6] & 0x0f) | 0x40; // version 4
  b[8] = (b[8] & 0x3f) | 0x80; // variant 1

  String hex(int from, int to) => b
      .sublist(from, to)
      .map((x) => x.toRadixString(16).padLeft(2, '0'))
      .join();

  return '${hex(0, 4)}-${hex(4, 6)}-${hex(6, 8)}-${hex(8, 10)}-${hex(10, 16)}';
}
