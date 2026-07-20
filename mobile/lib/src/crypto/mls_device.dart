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
///
/// [namespace] is empty in the app. The integration tests use it to keep two devices apart in one
/// process — and it has to be here too, because two devices sharing a device id would share a leaf,
/// which is precisely the bug this whole file exists to prevent.
String _mlsDeviceIdKey(String namespace) => 'pheme.mlsDeviceId$namespace';

const _iosOptions = IOSOptions(
  accessibility: KeychainAccessibility.first_unlock,
);

Future<String?> loadMlsDeviceId(
  FlutterSecureStorage storage, {
  String namespace = '',
}) => storage.read(key: _mlsDeviceIdKey(namespace), iOptions: _iosOptions);

Future<void> saveMlsDeviceId(
  FlutterSecureStorage storage,
  String deviceId, {
  String namespace = '',
}) => storage.write(
  key: _mlsDeviceIdKey(namespace),
  value: deviceId,
  iOptions: _iosOptions,
);

Future<void> clearMlsDeviceId(
  FlutterSecureStorage storage, {
  String namespace = '',
}) => storage.delete(key: _mlsDeviceIdKey(namespace), iOptions: _iosOptions);

/// Where this device's recovery code lives, so the user can view it again and so auto-backup can
/// re-unlock itself after a relaunch. NEVER leaves the device — the server sees only ciphertext
/// sealed under a key derived from it, never the code.
String _recoveryCodeKey(String namespace) => 'pheme.mlsRecoveryCode$namespace';

Future<String?> loadRecoveryCode(
  FlutterSecureStorage storage, {
  String namespace = '',
}) => storage.read(key: _recoveryCodeKey(namespace), iOptions: _iosOptions);

Future<void> saveRecoveryCode(
  FlutterSecureStorage storage,
  String code, {
  String namespace = '',
}) => storage.write(
  key: _recoveryCodeKey(namespace),
  value: code,
  iOptions: _iosOptions,
);

Future<void> clearRecoveryCode(
  FlutterSecureStorage storage, {
  String namespace = '',
}) => storage.delete(key: _recoveryCodeKey(namespace), iOptions: _iosOptions);

/// Crockford base32, minus I L O U — the alphabet the recovery code is drawn from.
const _crockford = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';

/// A fresh recovery code: 125 bits of entropy as five dash-separated groups of five Crockford
/// base32 characters (`XXXXX-XXXXX-XXXXX-XXXXX-XXXXX`). Mirrors the web client so a code made on
/// one platform restores on the other.
String generateRecoveryCode() {
  final random = Random.secure();
  final chars = List.generate(25, (_) => _crockford[random.nextInt(256) % 32]);
  final groups = <String>[];
  for (var i = 0; i < chars.length; i += 5) {
    groups.add(chars.sublist(i, i + 5).join());
  }
  return groups.join('-');
}

/// Normalises a typed-in recovery code the way the web client does, so a loosely-entered code opens
/// a backup sealed under its canonical form: upper-cased, the ambiguous I/L→1 and O→0, and every
/// other non-alphanumeric character (dashes, spaces) dropped.
String normalizeRecoveryCode(String input) => input
    .toUpperCase()
    .replaceAll(RegExp('[IL]'), '1')
    .replaceAll('O', '0')
    .replaceAll(RegExp('[^0-9A-Z]'), '');

/// The home domain this client's own MLS credentials are qualified under. It
/// comes from the server (GET /v1/meta) so every client on a host agrees, and is
/// set once before any MLS work. Defaults to 'local', which is correct and
/// consistent for a non-federated host. Domain-qualification is what makes a
/// member on one host distinct from a same-named member on another.
String _homeDomain = 'local';

/// Sets the home domain used to build THIS client's own credential identities.
void setHomeDomain(String domain) {
  if (domain.isNotEmpty) _homeDomain = domain;
}

/// The home domain in effect.
String homeDomain() => _homeDomain;

/// A leaf identity: `mimi://<domain>/d/<userId>/<deviceId>`, matching the bytes
/// the pheme-mls crate puts in a credential. The domain defaults to this
/// client's home domain — this only ever builds the LOCAL client's own
/// identities; a remote member's is parsed from their credential.
String deviceIdentity(String userId, String deviceId, [String? domain]) =>
    'mimi://${domain ?? _homeDomain}/d/$userId/$deviceId';

/// Parses `mimi://<domain>/d/<user>/<device>`, or null if not that form.
({String domain, String user, String device})? _parseIdentity(String identity) {
  if (!identity.startsWith('mimi://')) return null;
  final parts = identity.substring('mimi://'.length).split('/');
  if (parts.length != 4 || parts[1] != 'd') return null;
  return (domain: parts[0], user: parts[2], device: parts[3]);
}

/// The bare, host-local user id of a leaf, or '' if it does not parse. Bare (not
/// qualified) because the roster compares it against the server's membership and
/// key-package directory, both keyed by the host-local id; distinctness across
/// hosts is carried by the domain in the full leaf.
String userOf(String identity) => _parseIdentity(identity)?.user ?? '';

/// The device half of a leaf identity, or '' if it does not parse.
String deviceOf(String identity) => _parseIdentity(identity)?.device ?? '';

/// The qualified user key `mimi://<domain>/u/<user>` — the form the crate's
/// `user_of` returns, and so the form a removal target must take to match a
/// member's credential. Defaults to this client's home domain.
String userKey(String userId, [String? domain]) =>
    'mimi://${domain ?? _homeDomain}/u/$userId';

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
