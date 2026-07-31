// Signing out, signing back in, and what happens to the history — end to end.
//
// Real devices, the real Rust MLS library, a real server. This is the file that covers the thing the
// unit tests could not: the unit suite drives ChatCache and MlsStore against a FAKE seal, so it can
// prove the rules those classes enforce, and it proved them. It never ran a restore. The sequence
// that actually broke — a Settings restore destroys the data key, which orphans every file the body
// cache sealed under it, which makes load() mark each conversation unreadable, which makes the
// import refuse — spans three classes and the Rust library, and no test in the suite crossed more
// than one of them. A healthy twenty-three-message backup restored ONE conversation.
//
// So the scenarios here are stated the way a person experiences them, not the way the code is
// structured, and each one asserts on what is on the screen: readable, or "Not available".
//
// Run it:
//
//   cd api && PHEME_APP_ADDR=:8099 PHEME_JWT_SECRET=e2e-test-secret PHEME_MAIL_DRIVER=log \
//     PHEME_SEED_ADMIN_EMAIL=admin@pheme.test PHEME_SEED_ADMIN_PASSWORD=Admin12345 \
//     PHEME_TURN_URLS=direct go run ./cmd/app
//
//   cd mobile && flutter test integration_test/recovery_e2e_test.dart -d <device>

import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'package:pheme_mobile/src/models/chat_models.dart';
import 'package:pheme_mobile/src/rust/frb_generated.dart';

import 'harness.dart';

/// A conversation with three messages in it that Alice has read, and a recovery code covering them.
///
/// Returned as the state a person is actually in before they sign out: chats they can read, and a
/// code written down somewhere.
class _Established {
  _Established({
    required this.alice,
    required this.bob,
    required this.conversation,
    required this.code,
    required this.sent,
  });

  final Device alice;
  final Device bob;
  final Conversation conversation;
  final String code;

  /// Message id → the text, in the order they were sent.
  final Map<String, String> sent;
}

Future<_Established> _establish(String run) async {
  final alice = await Device.signUp('alice$run', email('alice$run'));
  final bob = await Device.signUp('bob$run', email('bob$run'));
  await alice.publishKeys();
  await bob.publishKeys();

  final conversation = await alice.repo.createDirectChat(bob.userId);

  final sent = <String, String>{};
  for (final text in ['first', 'second', 'third']) {
    final message = await alice.mls.sendMessage(
      conversation,
      alice.userId,
      text,
    );
    sent[message.id] = text;
  }

  // Bob joins and reads, so the conversation is genuinely two-sided before anything is torn down.
  await bob.read(conversation.id);

  // Alice reads her own history, which is what puts the bodies in her cache — the transcript in the
  // backup is a copy of that cache and nothing else.
  await alice.read(conversation.id);

  // The code, sealed over the keys AND the transcript.
  final code = await alice.mls.regenerateRecoveryCode(alice.userId);

  return _Established(
    alice: alice,
    bob: bob,
    conversation: conversation,
    code: code,
    sent: sent,
  );
}

/// Everything a device can read in a conversation, as texts — null for "Not available on this
/// device", which is what the bubble says.
Future<List<String?>> _readable(Device device, String conversationId) async {
  final read = await device.read(conversationId);
  return read.values.toList();
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  setUpAll(() async {
    await RustLib.init();
  });

  group('signing out and back in on the same handset', () {
    // SCENARIO 1. The promise the recovery code makes, and the one that was broken: restoring brings
    // the history back — all of it, not the first conversation of it.
    testWidgets('entering the backup code restores the chats', (_) async {
      final s = await _establish('1');

      await s.alice.signOut();
      final back = await s.alice.signInAgain();

      final restored = await back.mls.restoreWithSecret(back.userId, s.code);
      expect(restored, isTrue, reason: 'the code was rejected');

      final read = await back.read(s.conversation.id);
      for (final entry in s.sent.entries) {
        expect(
          read[entry.key],
          entry.value,
          reason: 'the restore did not bring back "${entry.value}"',
        );
      }
    });

    // SCENARIO 2. The other half of the same promise, and the rule the user set: refusing the code
    // means starting empty. A restore that "works" by leaving the old plaintext lying around is not
    // a restore, it is a device that never wiped.
    testWidgets('declining the backup code leaves the chats unreadable', (
      _,
    ) async {
      final s = await _establish('2');

      await s.alice.signOut();
      final back = await s.alice.signInAgain();
      await back.mls.acceptFreshIdentity();

      final read = await _readable(back, s.conversation.id);
      expect(
        read.where((body) => body != null),
        isEmpty,
        reason:
            'starting fresh must leave the old history unreadable — these bodies '
            'were still on screen after a sign-out',
      );
    });

    // SCENARIO 5. Declining must not leave the account CRIPPLED. The conversation is still there and
    // still hers; she just cannot read what came before. Sending has to work, and Bob has to be able
    // to read it — a fresh identity that cannot rejoin its own group is a worse failure than a blank
    // history, because it is silent and permanent.
    testWidgets('after declining, a new message still sends and arrives', (
      _,
    ) async {
      final s = await _establish('5');

      await s.alice.signOut();
      final back = await s.alice.signInAgain();
      await back.mls.acceptFreshIdentity();

      // Her new identity has to get back into the group before it can send.
      await back.mls.ensureGroup(
        await back.repo.getConversation(s.conversation.id),
        back.userId,
      );

      final fresh = await back.mls.sendMessage(
        await back.repo.getConversation(s.conversation.id),
        back.userId,
        'still here',
      );

      final hers = await back.read(s.conversation.id);
      expect(
        hers[fresh.id],
        'still here',
        reason: 'a fresh identity could not read its own outgoing message',
      );
      for (final id in s.sent.keys) {
        expect(
          hers[id],
          isNull,
          reason: 'the pre-wipe history must stay unreadable',
        );
      }

      final his = await s.bob.read(s.conversation.id);
      expect(
        his[fresh.id],
        'still here',
        reason:
            'Bob could not read the message Alice sent after starting fresh — '
            'her new leaf never made it into the group',
      );
    });
  });

  group('a message written just before signing out', () {
    // THE ONE THE OTHERS ALL MISS, and they miss it for the same reason: every other test in this
    // file mints the recovery code AFTER the messages exist, and minting seals a backup
    // synchronously. That is not how the app works. The code is generated once, long ago, and every
    // message after it relies on the DEBOUNCED auto-backup — twenty seconds.
    //
    // wipeLocalKeys() cancels that timer and then wipes. So a message written within twenty seconds
    // of signing out never reaches the backup, and for a message this device SENT that is fatal:
    // MLS destroyed the key on encrypt, so the local cache was the only copy of it in existence.
    // Restoring afterwards correctly returns a transcript that never contained it, and the message
    // is unreadable for good.
    testWidgets('survives the sign-out and comes back with the code', (
      _,
    ) async {
      final s = await _establish('7');

      // A message sent AFTER the code exists — the ordinary case, and the one that only the
      // debounced auto-backup covers.
      final last = await s.alice.mls.sendMessage(
        s.conversation,
        s.alice.userId,
        'written just before signing out',
      );

      // No pause. Signing out promptly is not misuse; it is what a person does.
      await s.alice.signOut();
      final back = await s.alice.signInAgain();

      final restored = await back.mls.restoreWithSecret(back.userId, s.code);
      expect(restored, isTrue, reason: 'the code was rejected');

      final read = await back.read(s.conversation.id);
      expect(
        read[last.id],
        'written just before signing out',
        reason:
            'the message never reached the backup — signing out cancelled the '
            'pending one, and the sender cannot decrypt its own message twice',
      );
    });
  });

  group('a message the device never got to checkpoint', () {
    // The case a flush-on-sign-out cannot help with, and the reason the tail exists.
    //
    // Signing out politely is the easy half. The hard half is the handset that is dropped, drowned,
    // stolen or simply out of battery seconds after a message is written — no sign-out runs, no
    // debounce fires, and the body was on exactly one device because MLS destroyed the message key
    // on encrypt. Nothing the client does AT sign-out can cover that; the copy has to exist the
    // moment the message does.
    //
    // Modelled by never signing out at all: the device is abandoned mid-session and a new one is
    // brought up with the recovery code, which is precisely what replacing a lost phone looks like.
    testWidgets('is recoverable on a replacement handset', (_) async {
      final s = await _establish('8');

      final last = await s.alice.mls.sendMessage(
        s.conversation,
        s.alice.userId,
        'the last thing written on the phone that fell in the river',
      );

      // No sign-out, no checkpoint, no debounce — the handset is simply gone from here on.
      final replacement = await Device.signIn(
        'alice8-replacement',
        s.alice.email,
      );
      final restored = await replacement.mls.restoreWithSecret(
        replacement.userId,
        s.code,
      );
      expect(restored, isTrue, reason: 'the code was rejected');

      final read = await replacement.read(s.conversation.id);
      expect(
        read[last.id],
        'the last thing written on the phone that fell in the river',
        reason:
            'the message existed only on the lost handset — the tail is the one '
            'thing that could have carried it off in time',
      );
      // And the history the snapshot did hold is still there: the tail adds to the backup, it does
      // not stand in for it.
      for (final entry in s.sent.entries) {
        expect(read[entry.key], entry.value, reason: 'lost "${entry.value}"');
      }
    });
  });

  group('backing up on demand', () {
    // "Back up now" has to mean it. Somebody presses this before wiping a phone or handing one
    // over, reads that it worked, and acts on that — so a success message that arrives while the
    // most recent messages are still queued locally would be worse than no button at all.
    testWidgets('carries everything written since the last checkpoint', (
      _,
    ) async {
      final s = await _establish('9');

      final recent = await s.alice.mls.sendMessage(
        s.conversation,
        s.alice.userId,
        'written a moment before pressing the button',
      );

      await s.alice.mls.backUpNow(s.alice.userId);

      // A replacement handset with nothing but the code must find it — which is the promise the
      // button makes, stated the way the person understands it.
      final replacement = await Device.signIn(
        'alice9-replacement',
        s.alice.email,
      );
      expect(
        await replacement.mls.restoreWithSecret(replacement.userId, s.code),
        isTrue,
      );

      final read = await replacement.read(s.conversation.id);
      expect(
        read[recent.id],
        'written a moment before pressing the button',
        reason:
            'the button reported success without carrying the newest message',
      );
    });

    // It must never force. force means "replace a stored backup holding MORE history than this
    // device has", which is how a freshly restored phone once erased a full history. Pressing
    // "back up now" is a request to save what is here, not permission to destroy what is there.
    testWidgets('does not overwrite a fuller backup made elsewhere', (_) async {
      final s = await _establish('10');

      // A second handset comes up with nothing but the code and does NOT restore — so it holds no
      // history at all, which is exactly the device that must not be able to flatten the backup.
      final empty = await Device.signIn('alice10-empty', s.alice.email);
      await empty.mls.acceptFreshIdentity();

      // It has no recovery code of its own, so the button is refused outright rather than
      // uploading an empty transcript over a full one.
      await expectLater(
        empty.mls.backUpNow(empty.userId),
        throwsA(anything),
        reason:
            'a device with nothing to seal under must not be able to check in an empty backup',
      );

      // And the stored history is untouched: a third handset restores everything.
      final third = await Device.signIn('alice10-third', s.alice.email);
      expect(await third.mls.restoreWithSecret(third.userId, s.code), isTrue);
      final read = await third.read(s.conversation.id);
      for (final entry in s.sent.entries) {
        expect(read[entry.key], entry.value, reason: 'lost "${entry.value}"');
      }
    });
  });

  group('restoring from Settings, after having started fresh', () {
    // THE ONE THAT ACTUALLY HAPPENED, and the reason the other five in this file are not enough on
    // their own: every one of them restores onto a device whose body cache is EMPTY, so there is
    // nothing on disk for the import to trip over and they pass with the bug still in.
    //
    // The real sequence has a step in between. Start fresh, then use the app — read a chat, send a
    // message — and the fresh identity seals bodies under the data key it just minted. NOW restore
    // from Settings: that destroys the data key, orphaning those files, and every one of them made
    // load() mark its conversation unreadable, which made the import refuse to write. A healthy
    // twenty-three-message backup restored one conversation and reported "Not available on this
    // device" for the rest.
    testWidgets('brings the whole history back, not the first conversation', (
      _,
    ) async {
      final s = await _establish('6');

      await s.alice.signOut();
      final back = await s.alice.signInAgain();
      await back.mls.acceptFreshIdentity();

      // She carries on using the app on the fresh identity. This is the step that matters: it puts
      // files in the body cache, sealed under a key the restore is about to destroy.
      await back.mls.ensureGroup(
        await back.repo.getConversation(s.conversation.id),
        back.userId,
      );
      final afterFresh = await back.mls.sendMessage(
        await back.repo.getConversation(s.conversation.id),
        back.userId,
        'sent while starting over',
      );
      await back.read(s.conversation.id);

      // She closes the app and opens it again — which is the step that arms the bug, and the reason
      // a test that skipped it passed with the bug present. A running app holds every conversation
      // it has opened in memory, and an import merges into THAT rather than reading the disk. Only
      // after a restart is the cache cold, and only then does the import go to the files the restore
      // is about to orphan.
      final reopened = await back.signInAgain();

      // Only now does she find the code and restore from Settings.
      final restored = await reopened.mls.restoreWithSecret(
        reopened.userId,
        s.code,
        replaceExisting: true,
      );
      expect(restored, isTrue, reason: 'the code was rejected');

      final read = await reopened.read(s.conversation.id);
      for (final entry in s.sent.entries) {
        expect(
          read[entry.key],
          entry.value,
          reason:
              'the restore did not bring back "${entry.value}" — the orphaned '
              'body cache vetoed the import',
        );
      }

      // And what she wrote while starting over is still there. It was never in the backup, and the
      // key that sealed it is gone, so preserving it means reading it out BEFORE the wipe — the
      // alternative is a restore that recovers the history by destroying the newest part of it.
      expect(
        read[afterFresh.id],
        'sent while starting over',
        reason:
            'the restore threw away a message this device had decrypted itself',
      );
    });
  });

  group('a second handset', () {
    // SCENARIO 3. The same restore, but on hardware that has never held this account. Nothing local
    // to merge with, nothing local to be vetoed by — the pure transcript path.
    testWidgets('restores the chats when given the backup code', (_) async {
      final s = await _establish('3');

      final second = await Device.signIn('alice3-second', s.alice.email);
      final restored = await second.mls.restoreWithSecret(
        second.userId,
        s.code,
      );
      expect(restored, isTrue, reason: 'the code was rejected');

      final read = await second.read(s.conversation.id);
      for (final entry in s.sent.entries) {
        expect(
          read[entry.key],
          entry.value,
          reason: 'the second handset did not receive "${entry.value}"',
        );
      }
    });

    // SCENARIO 4. And without the code it gets nothing — which is the property that makes the code
    // worth anything. A new phone that could read the history without it would mean the history was
    // never protected by it.
    testWidgets('sees no history without the backup code', (_) async {
      final s = await _establish('4');

      final second = await Device.signIn('alice4-second', s.alice.email);
      await second.mls.acceptFreshIdentity();

      final read = await _readable(second, s.conversation.id);
      expect(
        read.where((body) => body != null),
        isEmpty,
        reason: 'a device with no code read a history it was never given',
      );
    });
  });
}
