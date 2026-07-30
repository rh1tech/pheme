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

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:path_provider/path_provider.dart';
import 'package:path_provider_foundation/path_provider_foundation.dart';

import '../rust/api/vault.dart';

/// Raised when the data key cannot be read but something is already sealed under it.
///
/// Not a corruption and not a loss: on iOS the keychain is unreadable until the device has been
/// unlocked once after a reboot, and a background wake can land in that window. The only wrong
/// response is to mint a replacement, which would orphan every sealed file on the device — so this
/// says "not now" and the write is retried when the device is available.
class DataKeyUnavailableException implements Exception {
  const DataKeyUnavailableException();

  @override
  String toString() =>
      'DataKeyUnavailableException: the data key is not readable yet and '
      'sealed state already exists; minting a new one would orphan it';
}

class MlsStore {
  /// [namespace] separates one device's storage from another's IN THE SAME PROCESS.
  ///
  /// Empty in the app, where there is one device and the plain key names are the right ones. The
  /// integration tests use it to stand up two devices at once, which is the only way to exercise the
  /// group choreography — establish, welcome, commit, admit — against a real server, and the only way
  /// to test what happens to a device that joins late.
  /// [supportDirectory] exists for tests only, so the rule that protects the data key can be
  /// exercised without a device container. Production always uses the platform's own.
  MlsStore(
    this._storage, {
    String namespace = '',
    Future<Directory> Function()? supportDirectory,
  }) : _ns = namespace,
       _supportDirectory = supportDirectory ?? getApplicationSupportDirectory;

  final Future<Directory> Function() _supportDirectory;

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

  /// The App Group shared by the app and its NotificationServiceExtension.
  ///
  /// The extension is a separate process with its OWN container: it cannot see the app's private
  /// directory and cannot read the app's default keychain group. Both the state file and the data
  /// key therefore live in the shared group instead, or an iPhone could never decrypt a message
  /// preview. Must match the App Group on both Xcode targets' entitlements.
  static const appGroup = 'group.tech.rh1.pheme';

  /// Readable once the user has unlocked the device at least since boot — not "while unlocked". An
  /// incoming call has to be answerable from the lock screen, and a notification preview arrives
  /// at exactly the same awkward moments.
  ///
  /// The app's own access group. Everything is stored here, and it always works.
  static const _iosOptions = IOSOptions(
    accessibility: KeychainAccessibility.first_unlock,
  );

  /// The native bridge that mirrors the data key into the shared App Group keychain.
  ///
  /// This used to be a second [IOSOptions] with `groupId: appGroup`, written through
  /// flutter_secure_storage. It reached the right access group and was still unreadable by the
  /// extension, because the plugin stores a Secure-Enclave-wrapped blob rather than the value —
  /// see [_mirrorDataKeyForExtension] and ios/Runner/SharedKeychain.swift.
  ///
  /// Best-effort in exactly the way that option was: a build whose provisioning profile lacks the
  /// App Group gets errSecMissingEntitlement, the mirror silently does nothing, and the app's own
  /// copy of the key — which is never conditional on an entitlement — carries on as before. The
  /// cost is previews on that device, nothing else.
  static const _sharedKeychain = MethodChannel(
    'tech.rh1.pheme/shared_keychain',
  );

  final FlutterSecureStorage _storage;
  File? _file;

  /// The state file, in the App Group container where the extension can reach it.
  ///
  /// Falls back to the app's private directory when there is no container — Android, which has no
  /// App Groups and does not need them (the background isolate shares the app's own process and
  /// UID), and an iOS build whose entitlement is missing, where a preview simply does not happen.
  Future<File> _stateFileHandle() async {
    final cached = _file;
    if (cached != null) return cached;
    final dir = await _sharedDir() ?? await _supportDirectory();
    return _file = File('${dir.path}/$_stateFile');
  }

  Future<Directory?> _sharedDir() async {
    if (!Platform.isIOS) return null;
    try {
      final path = await PathProviderFoundation().getContainerPath(
        appGroupIdentifier: appGroup,
      );
      if (path == null) return null;
      // The container's root is shared with anything else the group holds; keep our own corner
      // of it so a future addition cannot collide with the key store.
      final dir = Directory('$path/mls');
      if (!await dir.exists()) await dir.create(recursive: true);
      return dir;
    } on Object {
      // No entitlement, or a build where the group is not configured. The store falls back to the
      // app-private directory and previews simply do not happen on this device.
      return null;
    }
  }

  /// The state file's previous home, in the app's private container.
  Future<File> _legacyStateFileHandle() async {
    final dir = await getApplicationSupportDirectory();
    return File('${dir.path}/$_stateFile');
  }

  /// The sealed state, or null when this device holds no keys.
  ///
  /// A blob that will not open is treated as no blob at all. That is deliberate: a truncated or
  /// key-mismatched file is not something a retry fixes, and crashing the app on launch over it
  /// would be worse than starting fresh — which is a path the user can already recover from with
  /// their backup.
  Future<Uint8List?> readState() async {
    final file = await _stateFileHandle();
    // The state moved into the App Group container so the extension could reach it. A device that
    // predates that move still has its keys in the old place, so fall back to it — and note the
    // fallback is a READ, never a delete. The old file is removed only after a successful write to
    // the new one (see writeState), so there is no moment where neither location has the keys.
    var source = file;
    if (!await file.exists()) {
      final legacy = await _legacyStateFileHandle();
      if (legacy.path == file.path || !await legacy.exists()) {
        // No key store at all: a fresh install, or one whose keys have not been restored yet.
        // Said out loud because all three nulls below look identical to a caller, and telling them
        // apart from the outside previously took a device and a guess.
        debugPrint('Pheme: MLS state absent — no key store on this device');
        return null;
      }
      source = legacy;
    }

    final key = await _dataKey();
    if (key == null) {
      // The blob is here but its key is not: the keystore entry was lost, or on iOS the device has
      // not been unlocked since boot and the accessibility class makes it unreadable right now.
      debugPrint('Pheme: MLS state present but the data key is unreadable');
      return null;
    }

    try {
      return await vaultOpen(
        domain: _domain,
        key: key,
        sealed: await source.readAsBytes(),
      );
    } on Object catch (e) {
      debugPrint('Pheme: MLS state will not open: $e');
      return null;
    }
  }

  /// Seals and writes the state.
  ///
  /// Written to a temporary file and renamed, because rename is atomic and a plain write is not. A
  /// process killed halfway through a direct write leaves a truncated key store, which is every
  /// group on this device gone.
  Future<void> writeState(Uint8List state) async {
    final key = await _dataKey() ?? await _mintDataKeyForAFreshStore();
    final sealed = await vaultSeal(domain: _domain, key: key, plaintext: state);

    final file = await _stateFileHandle();
    final temp = File('${file.path}.tmp');
    await temp.writeAsBytes(sealed, flush: true);
    await temp.rename(file.path);

    // Only now is the old copy safe to remove: the new location holds a complete, freshly sealed
    // state, and the rename above was atomic. Deleting it any earlier — at read time, say — would
    // open a window where a crash in between leaves the device with no key store at all, which is
    // every group on it gone.
    await _discardLegacyState(file);
  }

  /// Copies the data key into the shared access group, where the NotificationServiceExtension can
  /// read it. Best-effort and deliberately silent.
  ///
  /// Never the only copy: see [_sharedKeychain]. On a build whose provisioning profile does not
  /// carry the App Group, this throws errSecMissingEntitlement and nothing else changes.
  Future<void> _mirrorDataKeyForExtension(String encoded) async {
    if (!Platform.isIOS) return;
    try {
      // A NATIVE write, not flutter_secure_storage — even though the shared access group is right
      // there in the App Group and this used to write it with the plugin's groupId option.
      //
      // The plugin does not store a value, it stores a wrapping of one: the payload is
      // AES-encrypted and the AES key is sealed under a Secure Enclave ECIES key in a companion
      // `fss.wrapped.<account>` item. So the key WAS reaching the shared group all along, and the
      // extension still could not read it — a plain SecItemCopyMatching returns ciphertext in a
      // format only the plugin understands. That is the whole reason previews never worked on iOS.
      //
      // The alternative, reimplementing the unwrap in Swift, was rejected deliberately: it copies a
      // private detail of somebody else's plugin, and the version that changes it would break
      // previews silently. See ios/Runner/SharedKeychain.swift.
      final bytes = Uint8List.fromList(
        encoded.split(',').map(int.parse).toList(growable: false),
      );
      await _sharedKeychain.invokeMethod<bool>('putDataKey', bytes);
    } on Object {
      // No entitlement, or a keychain that will not share. Previews do not happen on this device
      // and everything else carries on exactly as before.
    }
  }

  /// Removes the pre-App-Group state file, once [current] is known good.
  Future<void> _discardLegacyState(File current) async {
    try {
      final legacy = await _legacyStateFileHandle();
      if (legacy.path == current.path) return;
      if (await legacy.exists()) await legacy.delete();
    } on Object {
      // Leaving it costs nothing but disk: it is sealed, and nothing reads it once the new
      // location exists. Failing the write over it would cost the message being sent.
    }
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
    // The app's own group is the source of truth and is read first. It is never conditional on an
    // entitlement, so this path cannot be broken by how the build was signed.
    final encoded = await _storage.read(
      key: _dataKeyKey,
      iOptions: _iosOptions,
    );
    if (encoded == null) return null;
    // Best-effort mirror into the shared group, so the extension has something to read. Silent on
    // failure by design: no entitlement means no previews, not a broken app.
    unawaited(_mirrorDataKeyForExtension(encoded));
    final bytes = Uint8List.fromList(
      encoded.split(',').map(int.parse).toList(growable: false),
    );
    return bytes.length == _keyLength ? bytes : null;
  }

  /// Mints the data key, but ONLY for a store that has nothing sealed under the old one.
  ///
  /// This used to be an unconditional `?? await _mintDataKey()`, and that was the single most
  /// destructive line in the app. The key is read from the keychain with `first_unlock`
  /// accessibility, and a read before the first unlock after a reboot returns NULL rather than
  /// failing — so a write that happened in the background, woken by a push, would find no key,
  /// mint a fresh one over the top, and orphan everything sealed under its predecessor: this
  /// store's own state, every cached message body, every cached envelope. All of it unopenable,
  /// for good, and in silence.
  ///
  /// A missing key is only safe to replace when there is nothing that key was protecting. If a
  /// sealed state file is already on disk, the key is not gone — it is unavailable — and the right
  /// answer is to fail this write and try again when the device is unlocked.
  Future<Uint8List> _mintDataKeyForAFreshStore() async {
    final file = await _stateFileHandle();
    if (await file.exists()) {
      throw const DataKeyUnavailableException();
    }
    return _mintDataKey();
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
