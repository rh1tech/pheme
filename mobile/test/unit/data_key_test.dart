// The data key, and the one rule that stops it taking every message on the device with it.
//
// One key seals three things: this store's MLS state, the decrypted body cache, and the envelope
// cache. They are told apart by domain, not by key, so losing it loses all three at once — and
// nothing sealed under it can ever be opened again, because the bodies are the only copy of a
// decrypted message.
//
// The dangerous part was never losing the key. It was REPLACING it. writeState read the key and, on
// null, minted a fresh one — and on iOS the keychain is `first_unlock`, so a read before the first
// unlock after a reboot returns null rather than failing. A background wake in that window would
// mint a new key over the old one and orphan everything, silently.

import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/crypto/mls_store.dart';

late Directory tempRoot;

MlsStore newStore() => MlsStore(
  const FlutterSecureStorage(),
  supportDirectory: () async => tempRoot,
);

/// The sealed state file the store would have written, as it is named on a non-iOS host.
File stateFile() => File('${tempRoot.path}/mls.state');

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() async {
    tempRoot = await Directory.systemTemp.createTemp('pheme-key-test');
  });

  tearDown(() async {
    if (await tempRoot.exists()) await tempRoot.delete(recursive: true);
  });

  group('a missing data key is not the same as a fresh device', () {
    // THE ONE THAT WOULD TAKE EVERYTHING. A locked keychain reads as no key at all; minting over it
    // orphans the state, the bodies and the envelopes in one go.
    test(
      'writeState refuses to mint a new key while sealed state exists',
      () async {
        FlutterSecureStorage.setMockInitialValues({}); // keychain unreadable
        await stateFile().writeAsBytes([
          1,
          2,
          3,
          4,
        ]); // something already sealed

        final store = newStore();
        await expectLater(
          store.writeState(Uint8List.fromList(List<int>.filled(16, 7))),
          throwsA(isA<DataKeyUnavailableException>()),
          reason:
              'the key is unavailable, not gone — minting a replacement destroys '
              'every file sealed under its predecessor',
        );
      },
    );

    // And it must not have written one on the way to refusing: a key quietly stored here is a key
    // that will be used for the NEXT write, which is the same disaster one step later.
    test('a refused write leaves the keychain untouched', () async {
      FlutterSecureStorage.setMockInitialValues({});
      await stateFile().writeAsBytes([1, 2, 3, 4]);

      final store = newStore();
      try {
        await store.writeState(Uint8List.fromList(List<int>.filled(16, 7)));
      } on Object {
        // expected
      }

      const storage = FlutterSecureStorage();
      expect(
        await storage.read(key: 'pheme.mlsDataKey'),
        isNull,
        reason:
            'no key may be minted while something is sealed under the old one',
      );
    });

    // The sealed file itself must survive the refusal — the whole point is that it stays openable
    // once the device is unlocked.
    test('a refused write leaves the sealed state on disk', () async {
      FlutterSecureStorage.setMockInitialValues({});
      await stateFile().writeAsBytes([1, 2, 3, 4]);

      final store = newStore();
      try {
        await store.writeState(Uint8List.fromList(List<int>.filled(16, 7)));
      } on Object {
        // expected
      }

      expect(await stateFile().exists(), isTrue);
      expect(await stateFile().readAsBytes(), [1, 2, 3, 4]);
    });
  });

  group('a genuinely fresh device still works', () {
    // Minting is correct when there is nothing to orphan — otherwise no device could ever start.
    // The write goes on to seal, which needs the Rust library, so this asserts only that it gets
    // PAST the key check: a DataKeyUnavailableException here would mean a fresh install is broken.
    test('with no key and no sealed state, minting is allowed', () async {
      FlutterSecureStorage.setMockInitialValues({});
      expect(await stateFile().exists(), isFalse);

      final store = newStore();
      Object? error;
      try {
        await store.writeState(Uint8List.fromList(List<int>.filled(16, 7)));
      } on Object catch (e) {
        error = e;
      }
      expect(
        error,
        isNot(isA<DataKeyUnavailableException>()),
        reason:
            'a device with nothing sealed must be allowed to mint its first key',
      );
    });
  });
}
