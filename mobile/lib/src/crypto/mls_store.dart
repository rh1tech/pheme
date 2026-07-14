// Where this device's MLS keys live at rest.
//
// The blob is the whole key store — the identity keypair and every group's ratchet state — and it is
// rewritten on every single message. That rules out flutter_secure_storage: Keychain and
// EncryptedSharedPreferences are for small secrets, not for tens of kilobytes rewritten constantly.
//
// So: a random 32-byte data key in the platform keystore, and the blob itself sealed under it in a
// file. The file is in the app's private container, but that is not enough on its own — a rooted
// Android device can read it, and an iOS backup can carry it off the phone. The key is what those
// two do not reach.
//
// THE ACCESSIBILITY SETTING IS NOT A DETAIL. A VoIP push wakes the app to ring an incoming call, and
// that can happen after a reboot before the user has unlocked the phone even once. With the default
// Keychain accessibility the data key is unreadable at that moment, so the state cannot be opened,
// the call cannot be decrypted, and the very first call after every reboot fails. `first_unlock` is
// what makes that work, and it is why it is spelled out here rather than left to the default.

import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:path_provider/path_provider.dart';

import '../rust/api/vault.dart';

class MlsStore {
  MlsStore(this._storage);

  static const _dataKeyKey = 'pheme.mlsDataKey';
  static const _ownerKey = 'pheme.mlsOwner';
  static const _freshKey = 'pheme.mlsFreshAccepted';
  static const _stateFile = 'mls.state';
  static const _keyLength = 32;

  /// Readable once the user has unlocked the device at least since boot — not "while unlocked". An
  /// incoming call has to be answerable from the lock screen.
  static const _iosOptions = IOSOptions(
    accessibility: KeychainAccessibility.first_unlock,
  );

  final FlutterSecureStorage _storage;
  File? _file;

  Future<File> _stateFileHandle() async {
    final cached = _file;
    if (cached != null) return cached;
    final dir = await getApplicationSupportDirectory();
    return _file = File('${dir.path}/$_stateFile');
  }

  /// The sealed state, or null when this device holds no keys.
  ///
  /// A blob that will not open is treated as no blob at all. That is deliberate: a truncated or
  /// key-mismatched file is not something a retry fixes, and crashing the app on launch over it
  /// would be worse than starting fresh — which is a path the user can already recover from with
  /// their backup.
  Future<Uint8List?> readState() async {
    final file = await _stateFileHandle();
    if (!await file.exists()) return null;

    final key = await _dataKey();
    if (key == null) return null;

    try {
      return await vaultOpen(key: key, sealed: await file.readAsBytes());
    } on Object {
      return null;
    }
  }

  /// Seals and writes the state.
  ///
  /// Written to a temporary file and renamed, because rename is atomic and a plain write is not. A
  /// process killed halfway through a direct write leaves a truncated key store, which is every
  /// group on this device gone.
  Future<void> writeState(Uint8List state) async {
    final key = await _dataKey() ?? await _mintDataKey();
    final sealed = await vaultSeal(key: key, plaintext: state);

    final file = await _stateFileHandle();
    final temp = File('${file.path}.tmp');
    await temp.writeAsBytes(sealed, flush: true);
    await temp.rename(file.path);
  }

  /// The account the stored state belongs to.
  ///
  /// Without this, state left behind by a previous account on a shared device would be silently
  /// adopted by the next one — who would then encrypt under someone else's MLS identity and publish
  /// key packages under their name.
  Future<String?> owner() =>
      _storage.read(key: _ownerKey, iOptions: _iosOptions);

  Future<void> setOwner(String userId) =>
      _storage.write(key: _ownerKey, value: userId, iOptions: _iosOptions);

  /// Whether the user chose to start fresh on this device rather than restore their backup. Without
  /// it we would refuse to mint an identity forever.
  Future<bool> freshAccepted() async =>
      await _storage.read(key: _freshKey, iOptions: _iosOptions) == 'true';

  Future<void> acceptFresh() =>
      _storage.write(key: _freshKey, value: 'true', iOptions: _iosOptions);

  /// Erases the keys and everything derived from them. Logout.
  Future<void> wipe() async {
    final file = await _stateFileHandle();
    if (await file.exists()) await file.delete();
    await _storage.delete(key: _dataKeyKey, iOptions: _iosOptions);
    await _storage.delete(key: _ownerKey, iOptions: _iosOptions);
    await _storage.delete(key: _freshKey, iOptions: _iosOptions);
  }

  Future<Uint8List?> _dataKey() async {
    final encoded = await _storage.read(
      key: _dataKeyKey,
      iOptions: _iosOptions,
    );
    if (encoded == null) return null;
    final bytes = Uint8List.fromList(
      encoded.split(',').map(int.parse).toList(growable: false),
    );
    return bytes.length == _keyLength ? bytes : null;
  }

  Future<Uint8List> _mintDataKey() async {
    final key = await randomBytes(length: BigInt.from(_keyLength));
    await _storage.write(
      key: _dataKeyKey,
      value: key.join(','),
      iOptions: _iosOptions,
    );
    return key;
  }
}
