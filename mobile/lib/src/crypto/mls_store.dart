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

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:path_provider/path_provider.dart';

import '../rust/api/vault.dart';

class MlsStore {
  /// [namespace] separates one device's storage from another's IN THE SAME PROCESS.
  ///
  /// Empty in the app, where there is one device and the plain key names are the right ones. The
  /// integration tests use it to stand up two devices at once, which is the only way to exercise the
  /// group choreography — establish, welcome, commit, admit — against a real server, and the only way
  /// to test what happens to a device that joins late.
  MlsStore(this._storage, {String namespace = ''}) : _ns = namespace;

  final String _ns;

  /// The generation of the key material in THIS store. Bumped by every wipe and every restore.
  ///
  /// A session is built on one generation of one store, and dies when that generation moves under it —
  /// which is what stops it writing its keys back over the ones that replaced them.
  ///
  /// It belongs to the store and not to the process, because "the keys were replaced" is a fact about
  /// a particular key store. A process-wide counter says the same thing in the app, where there is one
  /// store — and says something false the moment there are two, where one device minting its identity
  /// would declare every other device's keys destroyed.
  int get generation => _generation;
  int _generation = 0;

  void invalidate() => _generation++;

  String get _dataKeyKey => 'pheme.mlsDataKey$_ns';
  String get _ownerKey => 'pheme.mlsOwner$_ns';
  String get _freshKey => 'pheme.mlsFreshAccepted$_ns';
  String get _groupsKey => 'pheme.mlsGroups.v1$_ns';
  String get _stateFile => 'mls$_ns.state';

  static const _keyLength = 32;

  /// Bound into the seal as additional data, so a blob sealed for something else — the chat body
  /// cache, which uses the same key — cannot be substituted for the key store and opened as if it
  /// belonged here.
  static const _domain = 'pheme.mls.state.v1';

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
      return await vaultOpen(
        domain: _domain,
        key: key,
        sealed: await file.readAsBytes(),
      );
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
    final sealed = await vaultSeal(domain: _domain, key: key, plaintext: state);

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

  // --- which group each conversation is ---------------------------------------------------------
  //
  // The one thing about a conversation's encrypted group that this device CANNOT work out for itself:
  // the group's id. Everything else it already knows — it is holding the ratchet.
  //
  // And the id never changes. The server sets it once, on a compare-and-set, and the only thing that
  // can move it is a reset, which is rare, deliberate, and remembers the old id anyway.
  //
  // So asking the server for it every time a chat is opened is asking a question we already know the
  // answer to, and paying three network round trips to hear it — during which the app cannot decrypt a
  // single message and tells the user encryption is "still being set up". It is not. It is just
  // waiting for the post.

  /// Every group id a conversation has ever had, current one first. Empty when we have never settled
  /// this conversation on this device.
  Future<List<String>> groupIds(String conversationId) async {
    final all = await _groups();
    return all[conversationId] ?? const [];
  }

  /// Remembers what a conversation's groups are, so the next open needs no network at all.
  Future<void> rememberGroupIds(
    String conversationId,
    List<String> groupIds,
  ) async {
    if (groupIds.isEmpty) return;

    final all = await _groups();
    final existing = all[conversationId];
    if (existing != null && _sameList(existing, groupIds)) return;

    all[conversationId] = groupIds;
    await _storage.write(
      key: _groupsKey,
      value: jsonEncode(all),
      iOptions: _iosOptions,
    );
  }

  Map<String, List<String>>? _groupsCache;

  Future<Map<String, List<String>>> _groups() async {
    final cached = _groupsCache;
    if (cached != null) return cached;

    final raw = await _storage.read(key: _groupsKey, iOptions: _iosOptions);
    final out = <String, List<String>>{};

    if (raw != null) {
      try {
        (jsonDecode(raw) as Map<String, dynamic>).forEach((id, groups) {
          if (groups is List) {
            out[id] = groups.whereType<String>().toList();
          }
        });
      } on FormatException {
        // Corrupt. The worst that costs is one round trip to the server to learn them again.
      }
    }
    return _groupsCache = out;
  }

  static bool _sameList(List<String> a, List<String> b) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i] != b[i]) return false;
    }
    return true;
  }

  /// Erases the keys and everything derived from them. Logout.
  Future<void> wipe() async {
    _groupsCache = null;
    await _storage.delete(key: _groupsKey, iOptions: _iosOptions);
    await _wipeKeys();
  }

  Future<void> _wipeKeys() async {
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
