// The durability of a decrypted message body.
//
// This is the one cache in the app whose contents cannot be rebuilt. MLS destroys the message key
// on decrypt, so a body that reaches this class and does not survive is gone for good — the
// ciphertext is still on the server and no device will ever open it again.
//
// Tested without the Rust library, the same way the history-handoff rules are: the seal is injected
// and the rules around it are what these tests are about. A real backup of a real account was lost
// to one of the paths below, which is why they are here.

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pheme_mobile/src/crypto/attribution.dart';
import 'package:pheme_mobile/src/crypto/chat_cache.dart';
import 'package:pheme_mobile/src/crypto/chat_content.dart';

/// A stand-in for the real seal: reversible, deterministic, and able to fail on demand.
class FakeVault {
  /// Set to make every open throw, as a corrupt or truncated file does.
  bool openThrows = false;

  /// Set to make every seal throw, as a full disk does.
  bool sealThrows = false;

  Future<Uint8List> seal({
    required String domain,
    required List<int> key,
    required List<int> plaintext,
  }) async {
    if (sealThrows) throw StateError('disk full');
    // Domain and key are prefixed so a blob sealed for one cannot be opened for another —
    // the property the real vault has, and one these tests rely on.
    return Uint8List.fromList([
      ...utf8.encode('$domain|${key.join(",")}|'),
      ...plaintext,
    ]);
  }

  Future<Uint8List> open({
    required String domain,
    required List<int> key,
    required List<int> sealed,
  }) async {
    if (openThrows) throw StateError('unreadable');
    final prefix = utf8.encode('$domain|${key.join(",")}|');
    if (sealed.length < prefix.length) throw StateError('truncated');
    for (var i = 0; i < prefix.length; i++) {
      if (sealed[i] != prefix[i]) throw StateError('wrong key or domain');
    }
    return Uint8List.fromList(sealed.sublist(prefix.length));
  }
}

late Directory tempRoot;
late FakeVault vault;

ChatCache newCache() => ChatCache(
  const FlutterSecureStorage(),
  seal: vault.seal,
  open: vault.open,
  supportDirectory: () async => tempRoot,
);

/// The data key the cache seals with, in the encoding the cache stores it in.
void seedDataKey() {
  FlutterSecureStorage.setMockInitialValues({
    'pheme.mlsDataKey': List<int>.generate(32, (i) => i).join(','),
  });
}

ChatContent body(String text) => ChatContent(body: text);

final author = Attribution.authenticated('mimi://test.example/d/alice/dev-a');

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() async {
    vault = FakeVault();
    tempRoot = await Directory.systemTemp.createTemp('pheme-cache-test');
    seedDataKey();
  });

  tearDown(() async {
    if (await tempRoot.exists()) await tempRoot.delete(recursive: true);
  });

  group('a decrypted body survives', () {
    test('round-trips through a fresh cache instance', () async {
      final a = newCache();
      await a.cacheContent('c1', 'm1', body('hello'), author);

      // A NEW instance, so the answer comes off disk rather than out of memory.
      final b = newCache();
      expect(b.content('c1', 'm1'), isNull, reason: 'not loaded yet');
      await b.load('c1');
      expect(b.content('c1', 'm1')?.body, 'hello');
    });

    test('many bodies in one conversation all survive', () async {
      final a = newCache();
      for (var i = 0; i < 50; i++) {
        await a.cacheContent('c1', 'm$i', body('message $i'), author);
      }
      final b = newCache();
      final loaded = await b.load('c1');
      expect(loaded.length, 50);
    });
  });

  group('a body must never be silently dropped', () {
    // THE ONE THAT LOST A REAL HISTORY'S WORTH OF BODIES.
    //
    // load() treats an unreadable file as an empty one. cacheContent then builds on that empty map
    // and flushes it — replacing a file full of bodies with a file holding one. A single failed
    // read, from a half-written file or a hiccup in the keychain, takes the conversation with it.
    test('an unreadable cache file is not overwritten with a fresh one', () async {
      final a = newCache();
      for (var i = 0; i < 10; i++) {
        await a.cacheContent('c1', 'm$i', body('old $i'), author);
      }

      // A new instance that cannot open what is on disk.
      vault.openThrows = true;
      final b = newCache();
      await b.load('c1');

      // Writing one more body must not be allowed to replace the ten it could not read.
      vault.openThrows = false;
      await expectLater(
        b.cacheContent('c1', 'm-new', body('new'), author),
        throwsA(anything),
        reason:
            'writing on top of a cache that failed to load destroys every body it holds',
      );

      final c = newCache();
      final recovered = await c.load('c1');
      expect(
        recovered.length,
        10,
        reason: 'the ten original bodies must still be on disk',
      );
    });

    // The keychain is `first_unlock`: before the first unlock after a reboot the key read fails, so
    // _dataKey() is null. The body has already been decrypted by then and the MLS key is consumed.
    test(
      'a body cached with no data key available is not lost quietly',
      () async {
        FlutterSecureStorage.setMockInitialValues({}); // no data key
        final cache = newCache();

        await expectLater(
          cache.cacheContent('c1', 'm1', body('hello'), author),
          throwsA(anything),
          reason:
              'without a key the body cannot be stored, and the caller must find out — '
              'the plaintext exists nowhere else',
        );
      },
    );

    test('a failed seal is reported rather than swallowed', () async {
      final cache = newCache();
      vault.sealThrows = true;

      await expectLater(
        cache.cacheContent('c1', 'm1', body('hello'), author),
        throwsA(anything),
        reason:
            'a body that did not reach the disk must not look like one that did',
      );
    });
  });

  group('the transcript round trip is lossless', () {
    // This is the backup path: exportAllContents seals into the recovery backup, importContents
    // opens it on the restored device. Anything lost here is lost from the only off-device copy.
    test('export then import preserves every body, byte for byte', () async {
      final a = newCache();
      await a.cacheContent('c1', 'm1', body('first'), author);
      await a.cacheContent('c1', 'm2', body('second'), author);
      await a.cacheContent('c2', 'm3', body('other conversation'), author);

      final exported = await a.exportAllContents();
      expect(exported.keys.toSet(), {'c1', 'c2'});
      expect(exported['c1']!.length, 2);

      // A different device: its own directory, its own cache.
      final otherRoot = await Directory.systemTemp.createTemp('pheme-cache-b');
      addTearDown(() async => otherRoot.delete(recursive: true));
      final b = ChatCache(
        const FlutterSecureStorage(),
        seal: vault.seal,
        open: vault.open,
        supportDirectory: () async => otherRoot,
      );
      await b.importContents(exported);

      expect((await b.load('c1')).length, 2);
      expect(b.content('c1', 'm1')?.body, 'first');
      expect(b.content('c2', 'm3')?.body, 'other conversation');
      // Byte-identical: the export is the raw serialised form, so a re-export must match.
      expect(await b.exportAllContents(), exported);
    });

    test('import never overwrites a body already held', () async {
      final cache = newCache();
      await cache.cacheContent(
        'c1',
        'm1',
        body('mine, decrypted here'),
        author,
      );

      await cache.importContents({
        'c1': {'m1': 'something a handing-over device claimed'},
      });

      expect(
        cache.content('c1', 'm1')?.body,
        'mine, decrypted here',
        reason:
            'a body this device authenticated itself outranks one another device asserted',
      );
    });

    test('an empty export does not erase a stored transcript', () async {
      final cache = newCache();
      await cache.cacheContent('c1', 'm1', body('kept'), author);

      await cache.importContents(const {});

      expect((await cache.load('c1')).length, 1);
    });
  });

  group('forgetting is scoped', () {
    test('forget removes one conversation and leaves the rest', () async {
      final cache = newCache();
      await cache.cacheContent('c1', 'm1', body('one'), author);
      await cache.cacheContent('c2', 'm2', body('two'), author);

      await cache.forget('c1');

      final fresh = newCache();
      expect((await fresh.load('c1')), isEmpty);
      expect((await fresh.load('c2')).length, 1);
    });
  });
}
