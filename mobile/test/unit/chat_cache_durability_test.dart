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

/// A conversation's sealed file, at the path the cache actually uses.
File bodyFile(String conversationId) =>
    File('${tempRoot.path}/bodies/$conversationId.json');

/// Replaces the data key with a different one, as MlsStore.wipe() followed by minting a fresh
/// identity does. Everything already on disk becomes permanently unopenable.
void rotateDataKey() {
  FlutterSecureStorage.setMockInitialValues({
    'pheme.mlsDataKey': List<int>.generate(32, (i) => 200 - i).join(','),
  });
}

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

  group('restoring onto a device that already had an identity', () {
    // THE ONE THAT MADE A GOOD BACKUP UNRESTORABLE.
    //
    // A Settings restore destroys the MLS store, and the store owns `pheme.mlsDataKey` — the key
    // this cache seals bodies with. Every .json left in the cache directory is sealed under a key
    // that no longer exists, so load() cannot open it and marks it unreadable; _flush then refuses
    // to write, correctly, because normally an unreadable file is a file worth protecting.
    //
    // Here it is not. The key was destroyed on purpose, seconds ago, by the restore itself. The
    // refusal protects nothing and blocks the import — which is the entire point of the restore.
    // On a real account this delivered ONE conversation out of the backup and abandoned the other
    // twenty-two, reported as "Not available on this device" for every message.
    test(
      'a body cache whose key was destroyed does not block the import that replaces it',
      () async {
        final before = newCache();
        await before.cacheContent(
          'c1',
          'old-1',
          body('under the old key'),
          author,
        );
        await before.cacheContent(
          'c2',
          'old-2',
          body('also the old key'),
          author,
        );

        // What restoreKeys does: MlsStore.wipe() deletes the data key, session() mints a new one.
        rotateDataKey();

        // A cache that has not opened these conversations this session — which is every
        // conversation, right after a restore. load() cannot open them under the new key.
        final after = newCache();

        // The transcript out of the backup is the authority here, and says so.
        await after.importContents({
          'c1': {'m1': '{"body":"first"}'},
          'c2': {'m2': '{"body":"second"}'},
          'c3': {'m3': '{"body":"third"}'},
        }, authoritative: true);

        final reopened = newCache();
        expect((await reopened.load('c1')).length, 1);
        expect((await reopened.load('c2')).length, 1);
        expect(
          (await reopened.load('c3')).length,
          1,
          reason:
              'every conversation in the backup must land, not just the first',
        );
      },
    );

    // wipe() cleared the content maps but not the unreadable set, so a conversation marked
    // unreadable before the wipe stayed blocked for the life of the process — nothing could ever be
    // written to it again, including the restore's own import.
    test('wiping clears the record of what could not be read', () async {
      final cache = newCache();
      await cache.cacheContent('c1', 'm1', body('before'), author);

      vault.openThrows = true;
      final blocked = newCache();
      await blocked.load('c1'); // marks c1 unreadable
      vault.openThrows = false;

      await blocked.wipe();

      // The file is gone; there is nothing left to protect, so the write must be allowed.
      await blocked.importContents({
        'c1': {'m2': '{"body":"after"}'},
      });
      expect((await newCache().load('c1')).length, 1);
    });

    // One conversation that genuinely cannot be written must not cost the other twenty-two. The
    // loop abandoned everything after the first failure, so which history survived depended on
    // directory iteration order.
    test('one unwritable conversation does not abandon the rest', () async {
      final cache = newCache();
      // c1 is unreadable and stays that way — a real protective refusal.
      await bodyFile('c1').create(recursive: true);
      await bodyFile('c1').writeAsBytes([9, 9, 9]);
      await cache.load('c1');

      await expectLater(
        cache.importContents({
          'c1': {'m1': '{"body":"refused"}'},
          'c2': {'m2': '{"body":"must still land"}'},
          'c3': {'m3': '{"body":"and this one"}'},
        }),
        throwsA(isA<ChatCacheWriteException>()),
        reason: 'the caller must still be told that something was refused',
      );

      final fresh = newCache();
      expect((await fresh.load('c2')).length, 1);
      expect((await fresh.load('c3')).length, 1);
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

  group('the conversation list has previews before any chat is opened', () {
    // The list cannot decrypt — it only ever sees ciphertext — so a preview comes from this cache
    // and nowhere else. The cache was filled lazily by opening a chat, so a fresh launch showed a
    // column of "Encrypted message" that corrected itself only once each conversation had been
    // visited, with the plaintext on disk the whole time.
    test(
      'warmPreviews loads previews without opening the conversation',
      () async {
        final a = newCache();
        await a.cacheContent('c1', 'm1', body('the last thing said'), author);
        await a.cacheContent('c2', 'm2', body('and in the other one'), author);

        // A new instance stands for a fresh launch: nothing in memory yet.
        final fresh = newCache();
        expect(
          fresh.preview('c1'),
          isNull,
          reason: 'nothing loaded yet — this is the state the list opened in',
        );

        await fresh.warmPreviews(['c1', 'c2']);

        expect(fresh.preview('c1'), 'the last thing said');
        expect(fresh.preview('c2'), 'and in the other one');
      },
    );

    test('warming is skipped for a conversation already in memory', () async {
      final cache = newCache();
      await cache.cacheContent('c1', 'm1', body('already here'), author);

      // Would throw if it re-read and re-opened, since the vault now refuses.
      vault.openThrows = true;
      await cache.warmPreviews(['c1']);

      expect(cache.preview('c1'), 'already here');
    });

    // One unreadable conversation must not stop the rest of the list from showing anything.
    test('an unreadable conversation does not block the others', () async {
      final a = newCache();
      await a.cacheContent('c1', 'm1', body('readable'), author);

      final fresh = newCache();
      // c2 has a file that will not open; c1 is fine. Warming must survive the bad one.
      await bodyFile('c2').create(recursive: true);
      await bodyFile('c2').writeAsBytes([9, 9, 9]);

      await fresh.warmPreviews(['c2', 'c1']);

      expect(fresh.preview('c1'), 'readable');
    });
  });
}
