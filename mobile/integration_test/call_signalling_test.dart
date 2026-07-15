// Call signalling, end to end, between two real devices — everything about a call EXCEPT the WebRTC
// media and the push that wakes a sleeping phone.
//
// A call is not sealed with a message key. It is sealed with a secret EXPORTED from the group's
// current MLS epoch, under a label and a per-call, per-sender context. That is the one piece of the
// call path that depends on MLS, and it is the one no host test can reach — so it has run only in the
// Rust unit tests, never against a real group established over a real server.
//
// This runs it. Two devices join one group; one derives a call key and seals an invite; it goes
// through the real mailbox; the other reads it, derives the SAME key from its own copy of the group,
// and opens it. AES-GCM is the judge: if the two secrets differ by a single bit, openSignal throws,
// and the call would have rung out in production with nothing anywhere to say why.
//
// What this does NOT cover, and cannot without a device and credentials: the WebRTC offer/answer
// actually negotiating media, the CallKit/full-screen ringer, the VoIP push, and the answer-lock
// across a user's second device.
//
// Boot the API exactly as for chat_e2e_test.dart, then:
//   flutter test integration_test/call_signalling_test.dart -d <device>

import 'package:dio/dio.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'package:pheme_mobile/src/calls/call_envelope.dart';
import 'package:pheme_mobile/src/core/api_client.dart';
import 'package:pheme_mobile/src/core/token_store.dart';
import 'package:pheme_mobile/src/crypto/chat_cache.dart';
import 'package:pheme_mobile/src/crypto/mls_service.dart';
import 'package:pheme_mobile/src/data/pheme_repository.dart';
import 'package:pheme_mobile/src/rust/frb_generated.dart';

const _apiBase = String.fromEnvironment(
  'PHEME_API',
  defaultValue: 'http://localhost:8099',
);
const _adminEmail = 'admin@pheme.test';
const _adminPassword = 'Admin12345';
const _password = 'Correct12345';

Future<void> _createUser(String email) async {
  final dio = Dio(BaseOptions(baseUrl: _apiBase));
  final session = await dio.post<Map<String, dynamic>>(
    '/v1/auth/login',
    data: {'email': _adminEmail, 'password': _adminPassword},
  );
  await dio.post<void>(
    '/v1/admin/users',
    data: {'email': email, 'password': _password, 'role': 'user'},
    options: Options(
      headers: {'Authorization': 'Bearer ${session.data!['accessToken']}'},
    ),
  );
}

/// One device: its own account, storage namespace, and MLS identity.
class Device {
  Device._(this.userId, this.identity, this.repo, this.mls, this.cache);

  final String userId;

  /// This device's leaf identity, `userId:deviceId` — what the other end derives a key from.
  final String identity;
  final PhemeRepository repo;
  final MlsService mls;
  final ChatCache cache;

  static Future<Device> signUp(String label, String email) async {
    await _createUser(email);
    return signIn(label, email);
  }

  static Future<Device> signIn(String label, String email) async {
    const storage = FlutterSecureStorage();
    final tokens = TokenStore(storage);
    final dio = buildDio(
      baseUrl: _apiBase,
      tokenStore: tokens,
      onAuthFailure: () {},
    );
    final repo = PhemeRepository(dio);
    final s = await repo.login(email, _password);
    await tokens.save(
      Tokens(accessToken: s.accessToken, refreshToken: s.refreshToken),
    );

    final cache = ChatCache(storage, namespace: '.$label');
    final mls = MlsService(
      repository: repo,
      storage: storage,
      cache: cache,
      namespace: '.$label',
    );
    final session = await mls.session(s.userId);
    return Device._(s.userId, session.identity, repo, mls, cache);
  }

  Future<void> publishKeys() async {
    final session = await mls.session(userId);
    final minted = await session.mintKeyPackages(count: 5, lastResort: true);
    await repo.publishKeyPackages(
      session.deviceId,
      minted.packages,
      lastResortKeyPackage: minted.lastResort,
    );
  }

  /// Opens the conversation the way the app does — which is what joins this device to the group.
  Future<void> open(String conversationId) async {
    await cache.load(conversationId);
    await mls.primeGroup(conversationId);
    await mls.confirmGroup(conversationId, userId);
    await mls.ensureGroup(await repo.getConversation(conversationId), userId);
  }
}

String _email(String who) =>
    '$who-${DateTime.now().microsecondsSinceEpoch}@pheme.test';

const _callId = 'c0ffee00-1111-4222-8333-444455556666';
const _offerSdp =
    'v=0\r\no=alice 1 1 IN IP4 127.0.0.1\r\nm=audio 9 UDP/TLS\r\n';
const _answerSdp = 'v=0\r\no=bob 2 2 IN IP4 127.0.0.1\r\nm=audio 9 UDP/TLS\r\n';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  setUpAll(() async => RustLib.init());

  /// Stands up two devices already sharing an established group. Returns them plus the conversation.
  Future<(Device, Device, String)> _pair() async {
    final alice = await Device.signUp('alice', _email('alice'));
    final bob = await Device.signUp('bob', _email('bob'));
    await alice.publishKeys();
    await bob.publishKeys();

    final conversation = await alice.repo.createDirectChat(bob.userId);
    // A first message is the simplest way to force the group into existence; then Bob joins by opening.
    await alice.mls.sendMessage(conversation, alice.userId, 'ping');
    await bob.open(conversation.id);
    // And Alice reconciles, so both hold the same epoch before anyone derives a call key.
    await alice.mls.ensureGroup(conversation, alice.userId);
    return (alice, bob, conversation.id);
  }

  group('a call invite', () {
    testWidgets('is sealed by the caller and opened by the callee', (_) async {
      final (alice, bob, convo) = await _pair();

      // Caller: catch up to the current epoch FIRST — the exporter only exports from the epoch the
      // group is actually at — then derive the key for this call, sealed to Alice's own identity.
      await alice.mls.catchUpToLatest(convo, alice.userId);
      final key = await alice.mls.callKeyFor(
        convo,
        alice.userId,
        _callId,
        alice.identity,
      );
      expect(key, isNotNull, reason: 'the caller could not derive a call key');

      final wire = await sealSignal(
        key!.secret,
        CallHeader(
          callId: _callId,
          epoch: key.epoch,
          from: alice.identity,
          seq: 1,
        ),
        const CallBody(kind: CallKind.invite, sdp: _offerSdp),
      );
      await alice.repo.callSignal(convo, _callId, wire, ring: true);

      // Callee: read the mailbox, learn from the header which epoch and which sender to derive for,
      // catch up to that epoch, derive the SAME key, and open.
      final signals = await bob.repo.callSignals(convo, _callId);
      expect(
        signals,
        hasLength(1),
        reason: 'the invite never reached the mailbox',
      );

      final incoming = signals.first.ciphertext;
      final header = openHeader(incoming);
      expect(header, isNotNull);
      expect(header!.from, alice.identity);

      await bob.mls.catchUpToEpoch(convo, bob.userId, header.epoch);
      final bobKey = await bob.mls.callKeyFor(
        convo,
        bob.userId,
        _callId,
        header.from,
      );
      expect(bobKey, isNotNull);

      final body = await openSignal(bobKey!.secret, incoming);
      expect(body.kind, CallKind.invite);
      expect(
        body.sdp,
        _offerSdp,
        reason:
            'the two devices derived different secrets from the same group — the call would have '
            'rung out with no error anywhere',
      );
    });

    testWidgets('is answered, and the caller opens the answer', (_) async {
      final (alice, bob, convo) = await _pair();

      // Alice invites (abbreviated — the previous test proves the invite path).
      await alice.mls.catchUpToLatest(convo, alice.userId);
      final aliceKey = (await alice.mls.callKeyFor(
        convo,
        alice.userId,
        _callId,
        alice.identity,
      ))!;
      await alice.repo.callSignal(
        convo,
        _callId,
        await sealSignal(
          aliceKey.secret,
          CallHeader(
            callId: _callId,
            epoch: aliceKey.epoch,
            from: alice.identity,
            seq: 1,
          ),
          const CallBody(kind: CallKind.invite, sdp: _offerSdp),
        ),
        ring: true,
      );

      // Bob answers, sealing under HIS identity.
      await bob.open(convo);
      await bob.mls.catchUpToLatest(convo, bob.userId);
      final bobKey = (await bob.mls.callKeyFor(
        convo,
        bob.userId,
        _callId,
        bob.identity,
      ))!;
      await bob.repo.callSignal(
        convo,
        _callId,
        await sealSignal(
          bobKey.secret,
          CallHeader(
            callId: _callId,
            epoch: bobKey.epoch,
            from: bob.identity,
            seq: 1,
          ),
          const CallBody(kind: CallKind.answer, sdp: _answerSdp),
        ),
      );

      // Alice reads the mailbox and finds Bob's answer — derived for Bob's identity, not her own.
      final signals = await alice.repo.callSignals(convo, _callId);
      final answer = signals.map((s) => s.ciphertext).firstWhere((w) {
        final h = openHeader(w);
        return h != null && h.from == bob.identity;
      });
      final header = openHeader(answer)!;
      final key = (await alice.mls.callKeyFor(
        convo,
        alice.userId,
        _callId,
        header.from,
      ))!;
      final body = await openSignal(key.secret, answer);

      expect(body.kind, CallKind.answer);
      expect(body.sdp, _answerSdp);
    });

    testWidgets('cannot be opened with a key derived for the wrong sender', (
      _,
    ) async {
      final (alice, bob, convo) = await _pair();

      await alice.mls.catchUpToLatest(convo, alice.userId);
      final key = (await alice.mls.callKeyFor(
        convo,
        alice.userId,
        _callId,
        alice.identity,
      ))!;
      final wire = await sealSignal(
        key.secret,
        CallHeader(
          callId: _callId,
          epoch: key.epoch,
          from: alice.identity,
          seq: 1,
        ),
        const CallBody(kind: CallKind.invite, sdp: _offerSdp),
      );

      // Bob derives a key for the WRONG sender (himself, not Alice). The context differs, so the key
      // differs, so AES-GCM authentication must fail — the seal is bound to who sent it.
      await bob.mls.catchUpToLatest(convo, bob.userId);
      final wrong = (await bob.mls.callKeyFor(
        convo,
        bob.userId,
        _callId,
        bob.identity,
      ))!;

      await expectLater(
        openSignal(wrong.secret, wire),
        throwsA(anything),
        reason:
            'a signal opened with a key derived for the wrong sender must fail, not silently '
            'return the wrong plaintext',
      );
    });
  });
}
