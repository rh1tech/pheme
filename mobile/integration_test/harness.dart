// The shared harness for the integration suite: real devices, the real Rust MLS library, a real
// server.
//
// -------------------------------------------------------------------------------------------------
// HOW SEVERAL DEVICES FIT IN ONE PROCESS
//
// They should not. There is exactly one MLS client per process, deliberately, because one device has
// one ratchet. But a test with one device can prove nothing: an MLS group needs somebody to talk to,
// and a fake peer would only be testing the fake.
//
// So each device gets its own STORAGE NAMESPACE — its own key store, its own MLS device id, its own
// body cache — and MlsSession claims the single Rust client before every operation, re-importing its
// own state if another device has taken it. In the app that check is an integer comparison that
// always passes. Here it is what lets Alice, Bob and Carol be genuinely separate devices, with
// separate leaves and separate private keys, exactly as they would be on separate phones.
// -------------------------------------------------------------------------------------------------
//
// Users are created through the seeded admin, not through /auth/register, because registration mails
// a six-digit code that a test cannot read. This is the same route web/e2e takes.
//
// Run the API it talks to:
//
//   cd api && PHEME_APP_ADDR=:8099 PHEME_JWT_SECRET=e2e-test-secret PHEME_MAIL_DRIVER=log \
//     PHEME_SEED_ADMIN_EMAIL=admin@pheme.test PHEME_SEED_ADMIN_PASSWORD=Admin12345 \
//     PHEME_TURN_URLS=direct go run ./cmd/app

import 'package:dio/dio.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

import 'package:pheme_mobile/src/core/api_client.dart';
import 'package:pheme_mobile/src/core/token_store.dart';
import 'package:pheme_mobile/src/crypto/chat_cache.dart';
import 'package:pheme_mobile/src/crypto/chat_envelope_cache.dart';
import 'package:pheme_mobile/src/crypto/mls_service.dart';
import 'package:pheme_mobile/src/data/pheme_repository.dart';

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

  // The auth endpoints are rate limited, and a suite that signs several accounts in per test walks
  // straight into it — a 429 that has nothing to say about the code under test. Backing off is the
  // honest response: the limiter is behaving correctly and the test simply has to wait its turn.
  final session = await _withBackoff(
    () => dio.post<Map<String, dynamic>>(
      '/v1/auth/login',
      data: {'email': _adminEmail, 'password': _adminPassword},
    ),
  );
  final token = session.data!['accessToken'] as String;

  await _withBackoff(
    () => dio.post<void>(
      '/v1/admin/users',
      data: {'email': email, 'password': _password, 'role': 'user'},
      options: Options(headers: {'Authorization': 'Bearer $token'}),
    ),
  );
}

/// Retries a request that was rate limited, and only that. Any other failure is a real one and is
/// rethrown immediately — a blanket retry would turn a genuine bug into a slow test.
Future<T> _withBackoff<T>(Future<T> Function() send) async {
  for (var attempt = 0; ; attempt++) {
    try {
      return await send();
    } on DioException catch (e) {
      if (e.response?.statusCode != 429 || attempt >= 6) rethrow;
      await Future<void>.delayed(Duration(milliseconds: 500 * (attempt + 1)));
    }
  }
}

/// One device: its own account session, its own storage namespace, its own MLS identity.
class Device {
  Device._({
    required this.userId,
    required this.label,
    required this.email,
    required this.repo,
    required this.mls,
    required this.cache,
  });

  final String userId;

  /// The storage namespace, which is what makes this device a device. Re-signing in under the SAME
  /// label is the same handset; a different label is a different one.
  final String label;

  /// The account this device is signed in as, so a test can sign the same person in elsewhere.
  final String email;

  final PhemeRepository repo;
  final MlsService mls;
  final ChatCache cache;

  /// Signs out the way AuthController.logout does: the keys go, and everything sealed under them
  /// goes with them.
  ///
  /// Deliberately not just `wipeLocalKeys()`. The body cache and the envelope cache are sealed with
  /// the SAME `pheme.mlsDataKey` the store owns, so a sign-out that dropped the key and left them
  /// behind would leave a device full of files nothing can ever open — and a test that modelled
  /// sign-out as key-wiping alone would never see what those files do to the restore that follows.
  Future<void> signOut() async {
    await mls.wipeLocalKeys();
    await cache.wipe();
    await ChatEnvelopeCache(
      const FlutterSecureStorage(),
      namespace: '.$label',
    ).wipe();
  }

  /// Signs the same account back in on the SAME handset — same namespace, so whatever survived the
  /// sign-out is still there to be found.
  Future<Device> signInAgain() => Device.signIn(label, email);

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
      refreshCoordinator: TokenRefreshCoordinator(
        baseUrl: _apiBase,
        tokenStore: tokens,
      ),
      onAuthFailure: () {},
    );
    final repo = PhemeRepository(dio);

    final session = await _withBackoff(() => repo.login(email, _password));
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
      label: label,
      email: email,
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
      out[message.id] = content?.content.body;
    }
    return out;
  }
}

/// What this device can read, or null if it has no access to the conversation AT ALL.
///
/// Removal is enforced on the server as well as in the crypto: a removed member's requests are
/// refused outright, so `read` throws rather than returning an empty transcript. Both outcomes mean
/// the same thing to a test — "cannot read this" — and this distinguishes them from a message that
/// merely failed to decrypt.
extension DeviceAccess on Device {
  Future<Map<String, String?>?> tryRead(String conversationId) async {
    try {
      return await read(conversationId);
    } on Object {
      return null;
    }
  }
}

/// A fresh address per run, so the suite survives a re-run against a live server.
String email(String who) =>
    '$who-${DateTime.now().microsecondsSinceEpoch}@pheme.test';
