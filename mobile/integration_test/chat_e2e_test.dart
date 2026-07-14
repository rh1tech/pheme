// The end-to-end test: two real devices, the real Rust MLS library, and a real server.
//
// Everything else in the suite runs on a host VM, which means it runs against no MLS at all — the
// native library cannot load there. So the group choreography, which is the hardest and most
// dangerous code in this app, has until now executed only in the Rust unit tests, in isolation, with
// no server. This is the file that runs it for real.
//
// -------------------------------------------------------------------------------------------------
// HOW TWO DEVICES FIT IN ONE PROCESS
//
// They should not. There is exactly one MLS client per process, deliberately, because one device has
// one ratchet. But a test with one device can prove nothing: an MLS group needs somebody to talk to,
// and a fake peer would only be testing the fake.
//
// So each device gets its own STORAGE NAMESPACE — its own key store, its own MLS device id, its own
// body cache — and MlsSession claims the single Rust client before every operation, re-importing its
// own state if the other device has taken it. In the app that check is an integer comparison that
// always passes. Here it is what lets Alice and Bob be two genuinely separate devices, with separate
// leaves and separate private keys, exactly as they would be on two phones.
// -------------------------------------------------------------------------------------------------
//
// Users are created through the seeded admin, not through /auth/register, because registration mails
// a six-digit code that a test cannot read. This is the same route web/e2e takes.
//
// Run it:
//
//   cd api && PHEME_APP_ADDR=:8099 PHEME_JWT_SECRET=e2e-test-secret PHEME_MAIL_DRIVER=log \
//     PHEME_SEED_ADMIN_EMAIL=admin@pheme.test PHEME_SEED_ADMIN_PASSWORD=Admin12345 \
//     PHEME_TURN_URLS=direct go run ./cmd/app
//
//   cd mobile && flutter test integration_test/chat_e2e_test.dart -d <device>
//
// The API runs on its in-memory drivers, so there is nothing to install and nothing to clean up.

import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'package:pheme_mobile/src/core/api_client.dart';
import 'package:pheme_mobile/src/core/token_store.dart';
import 'package:pheme_mobile/src/crypto/chat_cache.dart';
import 'package:pheme_mobile/src/crypto/mls_errors.dart';
import 'package:pheme_mobile/src/crypto/mls_service.dart';
import 'package:pheme_mobile/src/data/pheme_repository.dart';
import 'package:pheme_mobile/src/rust/frb_generated.dart';

/// Where the API is. An Android emulator cannot see the host as localhost, so:
/// `--dart-define=PHEME_API=http://10.0.2.2:8099`.
const _apiBase = String.fromEnvironment(
  'PHEME_API',
  defaultValue: 'http://localhost:8099',
);

// Matches the PHEME_SEED_ADMIN_* env above, and web/e2e/constants.ts.
const _adminEmail = 'admin@pheme.test';
const _adminPassword = 'Admin12345';
const _password = 'Correct12345';

/// Creates a user the way the web suite does: as the admin, over the admin API.
///
/// Deliberately a raw Dio rather than a PhemeRepository method — the app's repository is the
/// non-admin surface, and a test is no reason to widen it.
Future<void> _createUser(String email) async {
  final dio = Dio(BaseOptions(baseUrl: _apiBase));

  final session = await dio.post<Map<String, dynamic>>(
    '/v1/auth/login',
    data: {'email': _adminEmail, 'password': _adminPassword},
  );
  final token = session.data!['accessToken'] as String;

  await dio.post<void>(
    '/v1/admin/users',
    data: {'email': email, 'password': _password, 'role': 'user'},
    options: Options(headers: {'Authorization': 'Bearer $token'}),
  );
}

/// One device: its own account session, its own storage namespace, its own MLS identity.
class Device {
  Device._({
    required this.userId,
    required this.repo,
    required this.mls,
    required this.cache,
  });

  final String userId;
  final PhemeRepository repo;
  final MlsService mls;
  final ChatCache cache;

  /// Signs [email] in on a device whose storage is entirely its own.
  ///
  /// The namespace is what makes two devices two devices. Without it they would share a key store and
  /// an MLS device id, and therefore a leaf — the one thing MLS must never let happen.
  static Future<Device> signIn(String label, String email) async {
    const storage = FlutterSecureStorage();
    final tokens = TokenStore(storage);

    final dio = buildDio(
      baseUrl: _apiBase,
      tokenStore: tokens,
      onAuthFailure: () {},
    );
    final repo = PhemeRepository(dio);

    final session = await repo.login(email, _password);
    await tokens.save(
      Tokens(
        accessToken: session.accessToken,
        refreshToken: session.refreshToken,
      ),
    );

    final namespace = '.$label';
    final cache = ChatCache(storage, namespace: namespace);

    return Device._(
      userId: session.userId,
      repo: repo,
      cache: cache,
      mls: MlsService(
        repository: repo,
        storage: storage,
        cache: cache,
        namespace: namespace,
      ),
    );
  }

  /// Creates the account, then signs its first device in.
  static Future<Device> signUp(String label, String email) async {
    await _createUser(email);
    return signIn(label, email);
  }

  /// Mints this device's MLS identity and publishes its key packages, so it is reachable.
  ///
  /// The app does this in the background on launch. The test does it explicitly and waits, because a
  /// device that has published nothing cannot be added to a group — so without the wait the test
  /// would be asserting against a race rather than against the code.
  Future<void> publishKeys() async {
    final session = await mls.session(userId);
    final minted = await session.mintKeyPackages(count: 5, lastResort: true);
    await repo.publishKeyPackages(
      session.deviceId,
      minted.packages,
      lastResortKeyPackage: minted.lastResort,
    );
  }

  /// Opens a conversation the way the app opens it, and returns everything this device can read,
  /// keyed by message id. A null value means "arrived, but could not be decrypted".
  Future<Map<String, String?>> read(String conversationId) async {
    await cache.load(conversationId);

    // The three steps the app takes on open, in the app's order.
    //
    // Prime makes what we already know readable with no round trip. Confirm asks the server which
    // group is current — that is what repairs the cache when a group has been reset underneath us.
    // NEITHER of them joins anything: a device cannot add itself to an MLS group, so a device that is
    // not yet a member reads nothing, and both of these say so honestly by leaving it that way.
    //
    // Settling is what joins. It applies the Commits this device missed — which may be the very
    // Welcome that lets it in — and announces the device if it is still outside. The app runs it in
    // the background, off the critical path, precisely because it can be slow; the test awaits it,
    // because a test that raced it would be asserting against the timing rather than the code.
    await mls.primeGroup(conversationId);
    await mls.confirmGroup(conversationId, userId);
    await mls.ensureGroup(await repo.getConversation(conversationId), userId);

    final page = await repo.listChatMessages(conversationId);
    final out = <String, String?>{};

    for (final message in page.messages.reversed) {
      if (message.isControl) continue;
      final content = await mls.decryptMessage(conversationId, userId, message);
      out[message.id] = content?.body;
    }
    return out;
  }
}

/// A fresh address per run, so the suite survives a re-run against a live server.
String _email(String who) =>
    '$who-${DateTime.now().microsecondsSinceEpoch}@pheme.test';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  setUpAll(() async {
    // The real Rust MLS library, on a real device. If this throws, nothing else in this file means
    // anything — and it is exactly the failure a host test cannot catch.
    await RustLib.init();
  });

  group('a message', () {
    testWidgets('is written by one device and read by the other', (_) async {
      final alice = await Device.signUp('alice', _email('alice'));
      final bob = await Device.signUp('bob', _email('bob'));

      // Bob must be reachable before Alice can build a group with him.
      await alice.publishKeys();
      await bob.publishKeys();

      final conversation = await alice.repo.createDirectChat(bob.userId);

      // Alice establishes the group, claims Bob's key package, commits, and sends. Every one of those
      // is a real round trip against a real compare-and-set.
      final sent = await alice.mls.sendMessage(
        conversation,
        alice.userId,
        'hello from alice',
      );

      // Bob joins from the Welcome and decrypts.
      final read = await bob.read(conversation.id);
      expect(
        read[sent.id],
        'hello from alice',
        reason: 'Bob could not read the message Alice sent him',
      );
    });

    // The invariant that makes MLS MLS, and the one no unit test can prove: encrypting destroys the
    // key, so a sender can never read back what it sent. The plaintext survives only because the app
    // writes it into the local cache at send time — which is why that cache is not an optimisation.
    testWidgets('cannot be decrypted a second time, even by its sender', (
      _,
    ) async {
      final alice = await Device.signUp('alice', _email('alice'));
      final bob = await Device.signUp('bob', _email('bob'));
      await alice.publishKeys();
      await bob.publishKeys();

      final conversation = await alice.repo.createDirectChat(bob.userId);
      final sent = await alice.mls.sendMessage(
        conversation,
        alice.userId,
        'alice speaking',
      );

      // Alice sees her own message — from the cache, which is the only copy that will ever exist.
      expect((await alice.read(conversation.id))[sent.id], 'alice speaking');

      // Take the cache away and it is gone for good. The ciphertext is still on the server, and Alice
      // still holds the group, and she still cannot read it.
      await alice.cache.wipe();
      final page = await alice.repo.listChatMessages(conversation.id);
      final orphan = await alice.mls.decryptMessage(
        conversation.id,
        alice.userId,
        page.messages.firstWhere((m) => m.id == sent.id),
      );
      expect(
        orphan,
        isNull,
        reason: 'a sender must never be able to decrypt its own message',
      );
    });
  });

  group('a peer who has never opened the app', () {
    testWidgets('is reported as such, not as a message that failed to send', (
      _,
    ) async {
      final alice = await Device.signUp('alice', _email('alice'));
      final ghost = await Device.signUp('ghost', _email('ghost'));
      await alice.publishKeys();
      // The ghost publishes nothing. There is no leaf to build a group around.

      final conversation = await alice.repo.createDirectChat(ghost.userId);

      await expectLater(
        alice.mls.sendMessage(conversation, alice.userId, 'anyone there?'),
        throwsA(isA<PeerKeysMissingException>()),
      );
    });
  });

  group("a user's second device", () {
    // The bug the whole design exists to prevent: an MLS leaf is a DEVICE, not a person. A new phone
    // is a new leaf with its own private keys, and it must be admitted to the group before it can
    // read a word — and it must never get the words that came before.
    testWidgets('is admitted, reads what follows, and cannot read the past', (
      _,
    ) async {
      final bobEmail = _email('bob');
      final alice = await Device.signUp('alice', _email('alice'));
      final bobPhone = await Device.signUp('bobphone', bobEmail);
      await alice.publishKeys();
      await bobPhone.publishKeys();

      final conversation = await alice.repo.createDirectChat(bobPhone.userId);
      final before = await alice.mls.sendMessage(
        conversation,
        alice.userId,
        'sent before the laptop existed',
      );

      // Bob signs in on a laptop: same account, brand new MLS identity. It announces itself...
      final bobLaptop = await Device.signIn('boblaptop', bobEmail);
      await bobLaptop.publishKeys();
      await bobLaptop.read(conversation.id);

      // ...and Alice, who holds the group, admits it when she next reconciles.
      await alice.mls.ensureGroup(conversation, alice.userId);

      final after = await alice.mls.sendMessage(
        conversation,
        alice.userId,
        'sent after the laptop joined',
      );

      final read = await bobLaptop.read(conversation.id);
      expect(
        read[after.id],
        'sent after the laptop joined',
        reason: 'the laptop was admitted but still cannot read new messages',
      );
      expect(
        read[before.id],
        isNull,
        reason:
            'a newly admitted device must not be able to read history that predates it — that is '
            'not a bug to fix, it is what forward secrecy means',
      );
    });
  });

  group('a group that was reset underneath us', () {
    // THE ONE THIS SUITE EXISTS FOR.
    //
    // A reset retires the group and starts a new one. Every other device is left holding an id that
    // is still perfectly valid for READING — the old messages open with it — and completely wrong for
    // WRITING. Everyone is on the new group.
    //
    // The failure this guards against is silent, which is what makes it dangerous. A device that
    // trusts its cached id seals its next message flawlessly, to a group nobody is in. No exception,
    // no error, no retry — the message simply arrives and cannot be opened by a single person. The
    // web client did exactly that, because its send path was `groupId || ensureGroup(...)`, and the
    // cached id short-circuited the check.
    //
    // Here, sendMessage goes through ensureGroup unconditionally, which asks the server. This test is
    // what says that is still true.
    testWidgets('does not swallow the next message sent to it', (_) async {
      final alice = await Device.signUp('alice', _email('alice'));
      final bob = await Device.signUp('bob', _email('bob'));
      await alice.publishKeys();
      await bob.publishKeys();

      final conversation = await alice.repo.createDirectChat(bob.userId);
      final before = await alice.mls.sendMessage(
        conversation,
        alice.userId,
        'sent to the group that is about to be retired',
      );
      expect((await bob.read(conversation.id))[before.id], isNotNull);

      // Bob retires the group and builds a new one. Alice is told nothing; her cached id is now a
      // group that has been abandoned by everybody, including her.
      await bob.repo.mlsResetGroup(conversation.id);
      await bob.mls.ensureGroup(
        await bob.repo.getConversation(conversation.id),
        bob.userId,
      );

      // Alice opens the chat, exactly as the app opens it: prime from the stale cache, then confirm
      // against the server. Nothing here is allowed to make her believe the retired id is current.
      await alice.read(conversation.id);

      // And she speaks. The whole hazard lives in this one line.
      final after = await alice.mls.sendMessage(
        conversation,
        alice.userId,
        'sent after the reset',
      );

      expect(
        (await bob.read(conversation.id))[after.id],
        'sent after the reset',
        reason:
            'Alice sealed her message to the RETIRED group. Nobody can read it, and nothing '
            'anywhere reported an error — which is precisely the bug this test exists to catch',
      );
    });
  });

  group('a photo', () {
    testWidgets('is sealed, uploaded, fetched and opened by the other end', (
      _,
    ) async {
      final alice = await Device.signUp('alice', _email('alice'));
      final bob = await Device.signUp('bob', _email('bob'));
      await alice.publishKeys();
      await bob.publishKeys();

      final conversation = await alice.repo.createDirectChat(bob.userId);

      // A 1x1 PNG. The bytes do not matter; what matters is that they come back byte for byte through
      // a server that never sees them in the clear.
      final photo = Uint8List.fromList(<int>[
        0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, //
        0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
        0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
        0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
        0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
        0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
        0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
        0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
        0x42, 0x60, 0x82,
      ]);

      final sent = await alice.mls.sendMessage(
        conversation,
        alice.userId,
        'look at this',
        photos: [photo],
      );

      // Bob opens the chat the way the app does — which is what joins him to the group — then decrypts
      // the message, which carries the key, and fetches the blob, which does not.
      await bob.read(conversation.id);

      final page = await bob.repo.listChatMessages(conversation.id);
      final content = await bob.mls.decryptMessage(
        conversation.id,
        bob.userId,
        page.messages.firstWhere((m) => m.id == sent.id),
      );

      expect(content?.body, 'look at this');
      expect(content?.photos, hasLength(1));

      final opened = await bob.mls.fetchPhoto(
        conversation.id,
        content!.photos.first,
      );
      expect(
        opened,
        photo,
        reason: 'the photo did not survive the round trip through the server',
      );
    });
  });

  group('a reply', () {
    testWidgets('carries the id of the message it answers, not a quote of it', (
      _,
    ) async {
      final alice = await Device.signUp('alice', _email('alice'));
      final bob = await Device.signUp('bob', _email('bob'));
      await alice.publishKeys();
      await bob.publishKeys();

      final conversation = await alice.repo.createDirectChat(bob.userId);
      final original = await alice.mls.sendMessage(
        conversation,
        alice.userId,
        'shall we meet at six',
      );

      await bob.read(conversation.id);
      final reply = await bob.mls.sendMessage(
        await bob.repo.getConversation(conversation.id),
        bob.userId,
        'six works',
        replyTo: original.id,
      );

      final page = await alice.repo.listChatMessages(conversation.id);
      final content = await alice.mls.decryptMessage(
        conversation.id,
        alice.userId,
        page.messages.firstWhere((m) => m.id == reply.id),
      );

      expect(content?.body, 'six works');
      expect(
        content?.replyTo,
        original.id,
        reason:
            'a reply must point at a message id — a quote the sender supplied could say anything',
      );
    });
  });
}
