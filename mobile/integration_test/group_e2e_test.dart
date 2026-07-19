// Group conversations, against a real server with the real Rust MLS library.
//
// This file exists because there was NO group coverage of any kind on mobile. Every scenario in
// chat_e2e_test.dart uses createDirectChat, and a direct chat is the easy case: two members, one
// device each, one epoch at a time. Groups are where the protocol gets hard — three parties, a
// roster that changes underneath the crypto, and an admin rule that decides who may commit — and
// that is exactly where four bugs turned up in production that nothing here would have caught.
//
// What these assert is the property that actually matters and that no unit test can prove: after a
// roster change, the RIGHT people can read the NEXT message, and the wrong people cannot.
//
// Run against a live API:
//   flutter test integration_test/group_e2e_test.dart --dart-define=PHEME_API_BASE=http://…

import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:pheme_mobile/src/crypto/mls_errors.dart';
import 'package:pheme_mobile/src/rust/frb_generated.dart';

import 'harness.dart';

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  setUpAll(() async {
    await RustLib.init();
  });

  group('a three-party group', () {
    testWidgets('is readable by every member', (_) async {
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));
      final carol = await Device.signUp('carol', email('carol'));
      for (final d in [alice, bob, carol]) {
        await d.publishKeys();
      }

      final conv = await alice.repo.createGroupChat('Trio', [
        bob.userId,
        carol.userId,
      ]);
      final sent = await alice.mls.sendMessage(conv, alice.userId, 'hello all');

      expect(
        (await bob.read(conv.id))[sent.id],
        'hello all',
        reason: 'Bob is a member and could not read the group message',
      );
      expect(
        (await carol.read(conv.id))[sent.id],
        'hello all',
        reason: 'Carol is a member and could not read the group message',
      );
    });

    // Every member must be able to SEND, not only the one who established the group. A group where
    // only the creator can be understood is not a group.
    testWidgets('carries messages from every member, in both directions', (
      _,
    ) async {
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));
      final carol = await Device.signUp('carol', email('carol'));
      for (final d in [alice, bob, carol]) {
        await d.publishKeys();
      }

      final conv = await alice.repo.createGroupChat('Talkative', [
        bob.userId,
        carol.userId,
      ]);
      await alice.mls.sendMessage(conv, alice.userId, 'from alice');
      // Bob has to join before he can speak, which is what reading does.
      await bob.read(conv.id);
      final fromBob = await bob.mls.sendMessage(conv, bob.userId, 'from bob');
      await carol.read(conv.id);
      final fromCarol = await carol.mls.sendMessage(
        conv,
        carol.userId,
        'from carol',
      );

      final aliceSees = await alice.read(conv.id);
      expect(aliceSees[fromBob.id], 'from bob');
      expect(aliceSees[fromCarol.id], 'from carol');
      expect(
        (await bob.read(conv.id))[fromCarol.id],
        'from carol',
        reason: "Bob could not read a third member's message",
      );
    });
  });

  group('adding a member to a group', () {
    // The reported scenario: a group of two, a third added later. They must be able to read what is
    // said AFTERWARDS — this is the property that broke in production.
    testWidgets('lets them read everything said after they joined', (_) async {
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));
      final carol = await Device.signUp('carol', email('carol'));
      for (final d in [alice, bob, carol]) {
        await d.publishKeys();
      }

      final conv = await alice.repo.createGroupChat('Growing', [bob.userId]);
      final before = await alice.mls.sendMessage(
        conv,
        alice.userId,
        'before carol',
      );
      await bob.read(conv.id);

      await alice.mls.addGroupMember(conv.id, alice.userId, carol.userId);
      final after = await alice.mls.sendMessage(
        conv,
        alice.userId,
        'after carol',
      );

      final carolSees = await carol.read(conv.id);
      expect(
        carolSees[after.id],
        'after carol',
        reason:
            'a newly added member cannot read what was said after they joined',
      );
      // Forward secrecy: what was said before they arrived is not theirs to read. It reaches them
      // only if a co-member deliberately shares history.
      expect(
        carolSees[before.id],
        isNull,
        reason: 'a new member could read a message sent before they joined',
      );
      // And the members who were already there are undisturbed.
      expect((await bob.read(conv.id))[after.id], 'after carol');
    });

    testWidgets('does not break the existing members mid-conversation', (
      _,
    ) async {
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));
      final carol = await Device.signUp('carol', email('carol'));
      for (final d in [alice, bob, carol]) {
        await d.publishKeys();
      }

      final conv = await alice.repo.createGroupChat('Interrupted', [
        bob.userId,
      ]);
      await alice.mls.sendMessage(conv, alice.userId, 'one');
      await bob.read(conv.id);

      await alice.mls.addGroupMember(conv.id, alice.userId, carol.userId);

      // Bob keeps talking across the epoch change, and Alice keeps reading him.
      final fromBob = await bob.mls.sendMessage(conv, bob.userId, 'two');
      expect(
        (await alice.read(conv.id))[fromBob.id],
        'two',
        reason: 'adding a member broke an existing member\'s ability to send',
      );
      expect((await carol.read(conv.id))[fromBob.id], 'two');
    });

    // A member with no published keys cannot be added, and the caller must be told plainly rather
    // than left with a half-built group.
    testWidgets('is refused for someone who has never opened the app', (
      _,
    ) async {
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));
      final ghost = await Device.signUp('ghost', email('ghost'));
      await alice.publishKeys();
      await bob.publishKeys();
      // ghost publishes nothing.

      final conv = await alice.repo.createGroupChat('Ghosted', [bob.userId]);
      await alice.mls.sendMessage(conv, alice.userId, 'hi');

      await expectLater(
        alice.mls.addGroupMember(conv.id, alice.userId, ghost.userId),
        throwsA(isA<PeerKeysMissingException>()),
      );
    });
  });

  group('removing a member from a group', () {
    testWidgets('cuts them off from everything said afterwards', (_) async {
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));
      final carol = await Device.signUp('carol', email('carol'));
      for (final d in [alice, bob, carol]) {
        await d.publishKeys();
      }

      final conv = await alice.repo.createGroupChat('Shrinking', [
        bob.userId,
        carol.userId,
      ]);
      final before = await alice.mls.sendMessage(conv, alice.userId, 'before');
      await bob.read(conv.id);
      await carol.read(conv.id);
      expect((await carol.read(conv.id))[before.id], 'before');

      await alice.mls.removeGroupMember(conv.id, alice.userId, carol.userId);
      final after = await alice.mls.sendMessage(conv, alice.userId, 'after');

      // Bob, who stayed, reads it.
      expect((await bob.read(conv.id))[after.id], 'after');
      // Carol, who was removed, does not. Removal is enforced twice over — the MLS Commit cuts her
      // out of the crypto, and the server refuses her requests — so either she is denied the
      // conversation entirely, or she reaches it and cannot decrypt. Both are "cut off"; asserting
      // only one of them would be asserting an implementation detail.
      final carolSees = await carol.tryRead(conv.id);
      expect(
        carolSees?[after.id],
        isNull,
        reason: 'a removed member could still read the group',
      );
    });

    testWidgets('lets a member leave of their own accord', (_) async {
      final alice = await Device.signUp('alice', email('alice'));
      final bob = await Device.signUp('bob', email('bob'));
      final carol = await Device.signUp('carol', email('carol'));
      for (final d in [alice, bob, carol]) {
        await d.publishKeys();
      }

      final conv = await alice.repo.createGroupChat('Leaving', [
        bob.userId,
        carol.userId,
      ]);
      await alice.mls.sendMessage(conv, alice.userId, 'hello');
      await carol.read(conv.id);

      // Carol removes herself. MLS forbids committing your own removal, so this is a different path
      // from being removed — it drops the membership and destroys the local group state.
      await carol.mls.removeGroupMember(conv.id, carol.userId, carol.userId);

      final after = await alice.mls.sendMessage(
        conv,
        alice.userId,
        'after she left',
      );
      expect(
        (await bob.read(conv.id))[after.id],
        'after she left',
        reason: 'the group stopped working for the members who stayed',
      );
      expect(
        (await carol.tryRead(conv.id))?[after.id],
        isNull,
        reason: 'someone who left could still read the group',
      );
    });
  });

  group('a second device', () {
    testWidgets('reads the group from both devices of the same member', (
      _,
    ) async {
      final alice = await Device.signUp('alice', email('alice'));
      final bobEmail = email('bob');
      final bobPhone = await Device.signUp('bob-phone', bobEmail);
      final carol = await Device.signUp('carol', email('carol'));
      for (final d in [alice, bobPhone, carol]) {
        await d.publishKeys();
      }

      final conv = await alice.repo.createGroupChat('TwoDevices', [
        bobPhone.userId,
        carol.userId,
      ]);
      await alice.mls.sendMessage(conv, alice.userId, 'first');
      await bobPhone.read(conv.id);

      // Bob's laptop: same account, a DIFFERENT MLS device, therefore its own leaf.
      final bobLaptop = await Device.signIn('bob-laptop', bobEmail);
      await bobLaptop.publishKeys();
      // Announce and be admitted — reading is what drives that.
      await bobLaptop.read(conv.id);
      await alice.mls.ensureGroup(
        await alice.repo.getConversation(conv.id),
        alice.userId,
      );

      final after = await alice.mls.sendMessage(conv, alice.userId, 'second');
      expect(
        (await bobLaptop.read(conv.id))[after.id],
        'second',
        reason: "a member's second device cannot read the group",
      );
      expect(
        (await bobPhone.read(conv.id))[after.id],
        'second',
        reason: "admitting a second device broke the member's first one",
      );
    });

    // The single-client rule, at group scale: two devices of the same user are two leaves, never
    // one. If the second cloned the first, removing either would cut off both.
    testWidgets('is its own leaf, so removing the member cuts off both', (
      _,
    ) async {
      final alice = await Device.signUp('alice', email('alice'));
      final bobEmail = email('bob');
      final bobPhone = await Device.signUp('bob-phone', bobEmail);
      final carol = await Device.signUp('carol', email('carol'));
      for (final d in [alice, bobPhone, carol]) {
        await d.publishKeys();
      }

      final conv = await alice.repo.createGroupChat('BothCutOff', [
        bobPhone.userId,
        carol.userId,
      ]);
      await alice.mls.sendMessage(conv, alice.userId, 'hello');
      await bobPhone.read(conv.id);

      final bobLaptop = await Device.signIn('bob-laptop', bobEmail);
      await bobLaptop.publishKeys();
      await bobLaptop.read(conv.id);
      await alice.mls.ensureGroup(
        await alice.repo.getConversation(conv.id),
        alice.userId,
      );

      await alice.mls.removeGroupMember(conv.id, alice.userId, bobPhone.userId);
      final after = await alice.mls.sendMessage(conv, alice.userId, 'after');

      // Cut off twice over: the MLS Commit removes the leaf, and the server refuses a
      // non-member's requests. Either outcome means "cannot read"; asserting only one would be
      // asserting an implementation detail rather than the property.
      expect(
        (await bobPhone.tryRead(conv.id))?[after.id],
        isNull,
        reason: 'removing a member left their first device able to read',
      );
      expect(
        (await bobLaptop.tryRead(conv.id))?[after.id],
        isNull,
        reason:
            'removing a member left their SECOND device able to read — '
            'a removal must cut off every device they have',
      );
      // Carol, untouched, still reads.
      expect((await carol.read(conv.id))[after.id], 'after');
    });
  });

  group('a device with no restored keys', () {
    // A fresh device holds no key material. It must still be able to JOIN and read what follows —
    // and must not be able to read the past, which is what a recovery code is for.
    testWidgets('joins a group and reads forward, but not backward', (_) async {
      final alice = await Device.signUp('alice', email('alice'));
      final bobEmail = email('bob');
      final bobOld = await Device.signUp('bob-old', bobEmail);
      await alice.publishKeys();
      await bobOld.publishKeys();

      final conv = await alice.repo.createGroupChat('FreshDevice', [
        bobOld.userId,
      ]);
      final past = await alice.mls.sendMessage(conv, alice.userId, 'the past');
      await bobOld.read(conv.id);

      // A brand-new device for Bob, with nothing restored.
      final bobNew = await Device.signIn('bob-new', bobEmail);
      await bobNew.publishKeys();
      await bobNew.read(conv.id);
      await alice.mls.ensureGroup(
        await alice.repo.getConversation(conv.id),
        alice.userId,
      );

      final future = await alice.mls.sendMessage(
        conv,
        alice.userId,
        'the future',
      );
      final sees = await bobNew.read(conv.id);
      expect(
        sees[future.id],
        'the future',
        reason: 'a device with no restored keys could not read forward',
      );
      expect(
        sees[past.id],
        isNull,
        reason:
            'a device with no restored keys read history it was never given',
      );
    });
  });
}
