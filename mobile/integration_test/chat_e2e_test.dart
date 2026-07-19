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

import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'package:pheme_mobile/src/crypto/mls_errors.dart';
import 'package:pheme_mobile/src/models/chat_models.dart';
import 'package:pheme_mobile/src/rust/frb_generated.dart';

import 'harness.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  setUpAll(() async {
    // The real Rust MLS library, on a real device. If this throws, nothing else in this file means
    // anything — and it is exactly the failure a host test cannot catch.
    await RustLib.init();
  });

  group('a message', () {
    testWidgets('is written by one device and read by the other', (_) async {
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));

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
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));
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
      final alice = await Device.signUp('alice', email('alice'));
      final ghost = await Device.signUp('ghost', email('ghost'));
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
      final bobEmail = email('bob');
      final alice = await Device.signUp('alice', email('alice'));
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

    // External join: a new device joins an EXISTING group with nobody online to admit it, and does so
    // WITHOUT resetting — so the offline party is not stranded on a group nobody else is in. This is
    // the scenario that broke in the field, and the reason external join exists. See docs/external-join.md.
    testWidgets('external-joins with no one online, and strands nobody', (
      _,
    ) async {
      final bobEmail = email('bob');
      final alice = await Device.signUp('alice', email('alice'));
      final bobPhone = await Device.signUp('bobphone', bobEmail);
      await alice.publishKeys();
      await bobPhone.publishKeys();

      final conversation = await alice.repo.createDirectChat(bobPhone.userId);
      // Alice establishes the group and, in doing so, publishes GroupInfo. Then everyone goes quiet.
      await alice.mls.sendMessage(
        conversation,
        alice.userId,
        'the group exists',
      );

      // Bob's laptop: a brand new device, not in the group. It opens the chat — and NOTHING admits it.
      // Alice never reconciles; bobPhone does nothing. It joins entirely on its own.
      final bobLaptop = await Device.signIn('boblaptop', bobEmail);
      await bobLaptop.publishKeys();

      final groupId = await bobLaptop.mls.ensureGroup(
        await bobLaptop.repo.getConversation(conversation.id),
        bobLaptop.userId,
      );
      expect(
        groupId,
        isNotNull,
        reason:
            'a new device must external-join the existing group with nobody online, not wait to be '
            'admitted',
      );

      // The laptop is really in: it sends, and Alice — still on the SAME group, never reset out from
      // under her — reads it once she opens the chat. This is what a reset would have broken.
      final sent = await bobLaptop.mls.sendMessage(
        await bobLaptop.repo.getConversation(conversation.id),
        bobLaptop.userId,
        'joined without help, and without stranding you',
      );
      final aliceRead = await alice.read(conversation.id);
      expect(
        aliceRead[sent.id],
        'joined without help, and without stranding you',
        reason:
            'the external join added a leaf to the existing group; Alice must still be in it and able '
            'to read the newcomer',
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
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));
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
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));
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
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));
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

  group('a recovery code', () {
    // THE PARALLEL-DEVICE BUG, AS A TEST.
    //
    // A user backs up on one device, then restores on a second while the first is still live. The
    // wrong way to restore is to ADOPT the backed-up identity — then both devices hold the SAME leaf,
    // MLS advances one ratchet between them, and each message one sends leaves the other unable to
    // decrypt ("not available on this device", on both). The right way is a FRESH leaf plus the old
    // transcript for history. This test asserts exactly that: a distinct identity, the history back,
    // and both devices reading each other afterwards.
    testWidgets('restores history to a FRESH device that works alongside the original', (
      _,
    ) async {
      final aliceEmail = email('alice');
      final alice1 = await Device.signUp('alice1', aliceEmail);
      final bob = await Device.signUp('bob', email('bob'));
      await alice1.publishKeys();
      await bob.publishKeys();

      final conversation = await alice1.repo.createDirectChat(bob.userId);
      final m1 = await alice1.mls.sendMessage(
        conversation,
        alice1.userId,
        'first',
      );
      expect((await bob.read(conversation.id))[m1.id], 'first');
      final m2 = await bob.mls.sendMessage(
        await bob.repo.getConversation(conversation.id),
        bob.userId,
        'second',
      );
      expect((await alice1.read(conversation.id))[m2.id], 'second');

      // Alice sets up her recovery backup — this seals BOTH her key state and her transcript.
      final code = await alice1.mls.ensureRecoveryBackup(alice1.userId);
      expect(
        code,
        isNotNull,
        reason: 'first setup must return a one-time code to show',
      );
      final alice1Device = (await alice1.mls.session(alice1.userId)).deviceId;

      // A new phone. It restores from the code BEFORE it has any identity of its own.
      final alice2 = await Device.signIn('alice2', aliceEmail);
      final restored = await alice2.mls.restoreKeys(alice2.userId, code!);
      expect(restored, isTrue);

      // FRESH identity, not a clone of the backed-up one.
      final alice2Device = (await alice2.mls.session(alice2.userId)).deviceId;
      expect(
        alice2Device,
        isNot(alice1Device),
        reason:
            'restore must mint a fresh leaf — adopting the backed-up device id is the clone that '
            'breaks both devices',
      );

      // The old history came across, sealed in the transcript and re-opened here.
      await alice2.cache.load(conversation.id);
      expect(
        alice2.cache.content(conversation.id, m1.id)?.body,
        'first',
        reason:
            'the transcript backup must carry pre-restore history to the new device',
      );
      expect(alice2.cache.content(conversation.id, m2.id)?.body, 'second');

      // The new phone joins the live group (external join) and reads what follows.
      await alice2.publishKeys();
      final m3 = await bob.mls.sendMessage(
        await bob.repo.getConversation(conversation.id),
        bob.userId,
        'third',
      );
      expect(
        (await alice2.read(conversation.id))[m3.id],
        'third',
        reason: 'the restored device is a real member and reads new messages',
      );

      // THE CRUX: the new phone sends, and the ORIGINAL device — still live — reads it. A clone would
      // make this impossible, because the two would be fighting over one ratchet.
      final m4 = await alice2.mls.sendMessage(
        await alice2.repo.getConversation(conversation.id),
        alice2.userId,
        'fourth',
      );
      expect(
        (await alice1.read(conversation.id))[m4.id],
        'fourth',
        reason:
            'the original device must read the restored device — two independent leaves, not one '
            'shared one',
      );
      expect((await bob.read(conversation.id))[m4.id], 'fourth');
    });
  });

  group('history sync', () {
    // A new device joins a conversation and gets its PRE-JOIN history from a co-member who is
    // online — device to device, sealed under a group-derived key the server never has. This is
    // the request -> offer -> import handshake, driven here through the service directly (the app
    // wires the same calls to the live stream's responder election).
    testWidgets('hands a fresh device its pre-join history from a co-member', (
      _,
    ) async {
      final bobEmail = email('bob');
      final alice = await Device.signUp('alice', email('alice'));
      final bobPhone = await Device.signUp('bobphone', bobEmail);
      await alice.publishKeys();
      await bobPhone.publishKeys();

      final conversation = await alice.repo.createDirectChat(bobPhone.userId);
      final past = await alice.mls.sendMessage(
        conversation,
        alice.userId,
        'said before the laptop existed',
      );
      // Bob's phone reads it, so the phone holds this history to hand out later.
      expect((await bobPhone.read(conversation.id))[past.id], isNotNull);

      // Bob's laptop: a brand-new device. It external-joins and, holding no transcript, cannot read
      // the pre-join message.
      final bobLaptop = await Device.signIn('boblaptop', bobEmail);
      await bobLaptop.publishKeys();
      final beforeSync = await bobLaptop.read(conversation.id);
      expect(
        beforeSync[past.id],
        isNull,
        reason: 'a device that just joined holds none of the history yet',
      );

      // The laptop asks for the history. The phone catches up to the laptop's join epoch (so the
      // key it derives matches), then answers as the elected responder.
      await bobLaptop.mls.requestHistory(conversation.id, bobLaptop.userId);
      await bobPhone.read(conversation.id); // catch up to the laptop's leaf
      final laptopIdentity = await bobLaptop.mls.myIdentity(bobLaptop.userId);
      await bobPhone.mls.offerHistory(
        conversation.id,
        bobPhone.userId,
        laptopIdentity,
      );

      // The laptop receives the offer (a control message) and opens it.
      final page = await bobLaptop.repo.listChatMessages(conversation.id);
      final offer = page.messages.firstWhere(
        (m) => m.contentType == ContentType.mlsHistoryOffer,
      );
      final imported = await bobLaptop.mls.receiveHistoryOffer(
        conversation.id,
        bobLaptop.userId,
        offer.ciphertext,
      );
      expect(
        imported,
        isTrue,
        reason: 'the offer was addressed to this device',
      );

      // The pre-join message is now readable on the laptop — device-to-device, never via the server.
      expect(
        bobLaptop.cache.content(conversation.id, past.id)?.body,
        'said before the laptop existed',
      );
    });
  });
}
